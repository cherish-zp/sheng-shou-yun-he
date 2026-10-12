// Package kafka implements the KafkaDataSource behaviour on top of franz-go
// (a pure-Go, CGO-free Kafka client).
package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"sheng-shou-yun-he/backend/internal/model"
)

// defaultFetchLimit is the batch size used when a caller does not specify one.
const defaultFetchLimit = 500

// partsTTL 是分区元数据缓存的存活时间:消息浏览热路径上每次都要解析分区,
// TTL 内直接复用上次拉取结果,避免每条浏览都打一次 broker 元数据往返。
const partsTTL = 60 * time.Second

// cachedPartitions 是一个 topic 的分区元数据缓存条目。
type cachedPartitions struct {
	parts   []int32
	expires time.Time
}

// Client is a Kafka data source backed by franz-go. It satisfies the
// service.KafkaDataSource interface.
type Client struct {
	name  string
	kcl   *kgo.Client
	admin *kadm.Client
	opts  []kgo.Opt

	// 浏览 consumer 池,按连接缓存。service 层为每个连接持有独立 Client,
	// 并在断开/删除/更新连接时对池内 Client 调 Close(见 closeBrowsePools),
	// 故本包内以 connKey(bootstrap servers,即连接标识)作 map key;
	// browseMu 保护 map 与池的惰性创建。
	browseMu    sync.Mutex
	browsePools map[string]*browsePool
	connKey     string

	// newBrowseConsumer 构建浏览 consumer,默认 newKgoBrowseConsumer;
	// 独立变量是为测试注入构建计数。
	newBrowseConsumer func(topic string, offsets map[int32]kgo.Offset) (*kgo.Client, error)

	// 分区元数据 TTL 缓存(见 partsTTL):key 为 topic。partsMu 保护 map;
	// listPartitions 默认经 admin 客户端拉取(测试可注入计数),now 默认
	// time.Now(测试注入时钟推进过期)。
	partsMu        sync.Mutex
	partsCache     map[string]cachedPartitions
	listPartitions func(ctx context.Context, topic string) ([]int32, error)
	now            func() time.Time
}

// browsePool 是单个连接的浏览 consumer 小池。每次消息浏览都新建 kgo
// consumer 需要完整的 TCP(+TLS+SASL/Kerberos)握手;池化后顺序浏览复用
// 同一实例,maxIdle=1:并发浏览超出时各自新建,归还时超限即 Close。
type browsePool struct {
	mu sync.Mutex
	// closed 表示池已失效(连接断开/删除/更新):此后归还的 consumer
	// 一律直接 Close,不再回池。
	closed bool
	// idle 是当前空闲的 consumer(maxIdle=1),nil 表示无空闲。
	idle *kgo.Client
	// assigned 记录 idle consumer 当前安装的 direct assignment
	// (topic→分区列表),复用时据此重设消费状态;被取走时置空。
	assigned map[string][]int32
}

// acquire 取出空闲 consumer 及其当前 assignment;无空闲返回 (nil, nil),
// 由调用方按连接选项新建。
func (p *browsePool) acquire() (*kgo.Client, map[string][]int32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cl, prev := p.idle, p.assigned
	p.idle, p.assigned = nil, nil
	return cl, prev
}

// release 归还 consumer:池未失效且无其他空闲时回池等待复用,否则
// (并发浏览超限或池已失效)直接 Close。
func (p *browsePool) release(cl *kgo.Client, assigned map[string][]int32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.idle != nil {
		cl.Close()
		return
	}
	p.idle = cl
	p.assigned = assigned
}

// closeAll 失效整个池:置 closed 并 Close 全部空闲 consumer。
func (p *browsePool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.idle != nil {
		p.idle.Close()
		p.idle = nil
	}
	p.assigned = nil
}

// NewClient builds a Kafka client for the given config. No I/O happens until
// Connect (or another method) is called.
func NewClient(name string, cfg model.KafkaConfig) (*Client, error) {
	opts, err := clientOpts(cfg)
	if err != nil {
		return nil, err
	}
	kcl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("create kafka client: %w", err)
	}
	c := &Client{
		name:        name,
		kcl:         kcl,
		admin:       kadm.NewClient(kcl),
		opts:        opts,
		browsePools: make(map[string]*browsePool),
		partsCache:  make(map[string]cachedPartitions),
		// 连接标识:同一 Client 的浏览池按连接归位;service 层一个连接
		// 对应一个 Client,重复 NewClient 同配置也各自持有独立池。
		connKey: strings.Join(cfg.BootstrapServers, ","),
	}
	c.newBrowseConsumer = c.newKgoBrowseConsumer
	c.listPartitions = c.listPartitionsViaAdmin
	c.now = time.Now
	return c, nil
}

// clientOpts translates a model.KafkaConfig into kgo options, wiring up SASL
// and TLS per the effective security protocol.
func clientOpts(cfg model.KafkaConfig) ([]kgo.Opt, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.BootstrapServers...),
		kgo.RequestTimeoutOverhead(10 * time.Second),
		// ProduceMessage sets an explicit partition; the default partitioner
		// would hash by key and ignore it.
		kgo.RecordPartitioner(kgo.ManualPartitioner()),
	}
	protocol := cfg.EffectiveSecurityProtocol()
	if protocol == model.SecurityProtocolSASLPlain || protocol == model.SecurityProtocolSASLSSL {
		if cfg.SASL == nil || !cfg.SASL.Enabled {
			return nil, fmt.Errorf("security protocol %s 需要 SASL 配置", protocol)
		}
		mech, err := saslMechanism(*cfg.SASL)
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.SASL(mech))
	}
	if protocol == model.SecurityProtocolSASLSSL || protocol == model.SecurityProtocolSSL {
		tlsCfg, err := buildTLSConfig(cfg.TLS)
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.DialTLSConfig(tlsCfg))
	}
	return opts, nil
}

// saslMechanism builds the franz-go SASL mechanism for the configured one.
// GSSAPI defers to the kerberos handshaker (gokrb5 in production).
func saslMechanism(cfg model.SASLConfig) (sasl.Mechanism, error) {
	mech := strings.ToUpper(strings.TrimSpace(cfg.Mechanism))
	switch mech {
	case model.SaslPlain:
		return plain.Auth{User: cfg.Username, Pass: cfg.Password}.AsMechanism(), nil
	case model.SaslScramSha256:
		return scram.Auth{User: cfg.Username, Pass: cfg.Password}.AsSha256Mechanism(), nil
	case model.SaslScramSha512:
		return scram.Auth{User: cfg.Username, Pass: cfg.Password}.AsSha512Mechanism(), nil
	case model.SaslGssapi:
		return &kerberosMechanism{newHandshaker: newGokrb5HandshakerFactory(cfg)}, nil
	default:
		return nil, fmt.Errorf("unsupported SASL mechanism %q", cfg.Mechanism)
	}
}

func buildTLSConfig(t *model.TLSConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: t.InsecureSkipVerify}
	if t.CACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(t.CACert)) {
			return nil, errors.New("failed to parse CA certificate")
		}
		tlsCfg.RootCAs = pool
	}
	return tlsCfg, nil
}

// Connect verifies connectivity to the cluster.
func (c *Client) Connect(ctx context.Context) error {
	if err := c.kcl.Ping(ctx); err != nil {
		return fmt.Errorf("ping kafka cluster: %w", err)
	}
	return nil
}

// Close releases the underlying client and every cached browse consumer.
// service 层在断开连接(CloseConnection)、删除连接(DeleteConnection)与
// 更新连接配置(UpdateConnection 换出旧 Client)时都会调用本方法,浏览池
// 随之失效;应用退出没有统一关闭池内 Client 的出口(bootstrap 仅关 store),
// 随进程结束由 OS 回收。
func (c *Client) Close() error {
	c.closeBrowsePools()
	c.kcl.Close()
	return nil
}

// poolForBrowse 返回当前连接的浏览池,不存在则惰性创建。
func (c *Client) poolForBrowse() *browsePool {
	c.browseMu.Lock()
	defer c.browseMu.Unlock()
	p, ok := c.browsePools[c.connKey]
	if !ok {
		p = &browsePool{}
		c.browsePools[c.connKey] = p
	}
	return p
}

// closeBrowsePools 失效当前连接全部浏览池:Close 池内所有空闲 consumer 并
// 清空 map;已被取走(在途浏览)的 consumer 在归还时因池已失效而关闭。
func (c *Client) closeBrowsePools() {
	c.browseMu.Lock()
	defer c.browseMu.Unlock()
	for k, p := range c.browsePools {
		p.closeAll()
		delete(c.browsePools, k)
	}
}

// GetName returns the connection name.
func (c *Client) GetName() string { return c.name }

// GetType returns the data source type.
func (c *Client) GetType() string { return "kafka" }

// ConsumeMessages fetches up to limit records from a topic. A partition >= 0
// restricts the fetch to that partition; a negative value fetches from every
// partition (used by the "All partitions" filter).
//
// The offset parameter accepts model.OffsetEarliest (-2), model.OffsetLatest
// (-1) or an absolute offset (>= 0). OffsetLatest reads the most recent
// `limit` records per partition (a tail of the log), which is the expected
// behaviour for a message browser.
func (c *Client) ConsumeMessages(ctx context.Context, topic string, partition int32, offset int64, limit int) ([]*model.Message, error) {
	offsets, err := c.partitionOffsets(ctx, topic, partition, offset, limit)
	if err != nil {
		return nil, err
	}
	if len(offsets) == 0 {
		return []*model.Message{}, nil
	}
	return c.fetch(ctx, topic, offsets, limit)
}

// ConsumeMessagesByTimestamp fetches up to limit records whose timestamp is at
// or after timestampMS (unix milliseconds). A negative partition means all.
func (c *Client) ConsumeMessagesByTimestamp(ctx context.Context, topic string, partition int32, timestampMS int64, limit int) ([]*model.Message, error) {
	partitions, err := c.partitionsOf(ctx, topic)
	if err != nil {
		return nil, err
	}
	if partition >= 0 {
		partitions = []int32{partition}
	}
	if len(partitions) == 0 {
		return []*model.Message{}, nil
	}
	offsets := make(map[int32]kgo.Offset, len(partitions))
	for _, p := range partitions {
		offsets[p] = kgo.NewOffset().AfterMilli(timestampMS)
	}
	return c.fetch(ctx, topic, offsets, limit)
}

// partitionsOf lists the partitions of a topic, served from a partsTTL cache
// on the message-browse hot path. 命中直接返回缓存切片(调用方只读,不改写
// 底层数组);过期重拉并刷新;拉取失败不缓存,下次调用立即重试。
func (c *Client) partitionsOf(ctx context.Context, topic string) ([]int32, error) {
	c.partsMu.Lock()
	if cached, ok := c.partsCache[topic]; ok && c.now().Before(cached.expires) {
		parts := cached.parts
		c.partsMu.Unlock()
		return parts, nil
	}
	c.partsMu.Unlock()

	// 拉取在锁外进行:元数据请求可能阻塞整个超时时长,不能长时间占住
	// partsMu 阻塞其他 topic 的缓存命中。
	parts, err := c.listPartitions(ctx, topic)
	if err != nil {
		return nil, err
	}
	c.partsMu.Lock()
	c.partsCache[topic] = cachedPartitions{parts: parts, expires: c.now().Add(partsTTL)}
	c.partsMu.Unlock()
	return parts, nil
}

// invalidatePartsCache 主动清除指定 topic 的分区缓存:删 Topic/扩分区等
// 结构性变更后调用,避免 partsTTL 窗口内浏览命中过期分区列表(扩容新分区
// 暂不可见、已删 topic 返回幽灵列表)。
func (c *Client) invalidatePartsCache(topics ...string) {
	c.partsMu.Lock()
	defer c.partsMu.Unlock()
	for _, topic := range topics {
		delete(c.partsCache, topic)
	}
}

// listPartitionsViaAdmin pulls the partitions of a single topic via the admin
// client. 只拉取目标 topic 的元数据(而非全集群),数千 topic 的大集群上
// 每次消息浏览/按时间消费可省一次全量元数据往返。
func (c *Client) listPartitionsViaAdmin(ctx context.Context, topic string) ([]int32, error) {
	details, err := c.admin.ListTopics(ctx, topic)
	if err != nil {
		return nil, fmt.Errorf("list topic metadata: %w", err)
	}
	td, ok := details[topic]
	if !ok {
		return nil, fmt.Errorf("topic %q not found", topic)
	}
	parts := make([]int32, 0, len(td.Partitions))
	for _, pd := range td.Partitions.Sorted() {
		parts = append(parts, pd.Partition)
	}
	return parts, nil
}

// partitionOffsets resolves the requested offset into per-partition kgo
// offsets for the (optionally single) target partition.
func (c *Client) partitionOffsets(ctx context.Context, topic string, partition int32, offset int64, limit int) (map[int32]kgo.Offset, error) {
	partitions, err := c.partitionsOf(ctx, topic)
	if err != nil {
		return nil, err
	}
	if partition >= 0 {
		partitions = []int32{partition}
	}

	// OffsetLatest means "tail": read the last limit records of each partition.
	if offset == model.OffsetLatest {
		endOffsets, err := c.admin.ListEndOffsets(ctx, topic)
		if err != nil {
			return nil, fmt.Errorf("list end offsets: %w", err)
		}
		out := make(map[int32]kgo.Offset, len(partitions))
		for _, p := range partitions {
			lo, ok := endOffsets.Lookup(topic, p)
			if !ok {
				continue
			}
			start := lo.Offset - int64(limit)
			if start < 0 {
				start = 0
			}
			out[p] = kgo.NewOffset().At(start)
		}
		return out, nil
	}

	var o kgo.Offset
	switch offset {
	case model.OffsetEarliest:
		o = kgo.NewOffset().AtStart()
	default:
		o = kgo.NewOffset().At(offset)
	}
	out := make(map[int32]kgo.Offset, len(partitions))
	for _, p := range partitions {
		out[p] = o
	}
	return out, nil
}

// ProduceMessage publishes a single record. A partition < 0 lets the client
// choose the partition.
func (c *Client) ProduceMessage(ctx context.Context, topic string, partition int32, key, value []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rec := &kgo.Record{Topic: topic, Key: key, Value: value}
	if partition >= 0 {
		rec.Partition = partition
	}
	if err := c.kcl.ProduceSync(ctx, rec).FirstErr(); err != nil {
		return fmt.Errorf("produce record: %w", err)
	}
	return nil
}

// ProduceMessages publishes a batch of records in a single ProduceSync over
// the same writer as ProduceMessage and returns one result per message.
// Errors are reported per item in ProduceResult.Error; the call itself only
// fails when results cannot be collected at all. A partition < 0 lets the
// client choose the partition.
func (c *Client) ProduceMessages(ctx context.Context, topic string, partition int32, messages []model.BatchProduceMessage) ([]*model.ProduceResult, error) {
	// 批量提交:全部 record 构建后单次 ProduceSync,避免逐条提交带来的
	// N 次往返与 N 次独立超时;整体超时与 ProduceMessage 一致取 10s。
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	recs := make([]*kgo.Record, len(messages))
	for i, msg := range messages {
		rec := &kgo.Record{Topic: topic, Key: []byte(msg.Key), Value: []byte(msg.Value)}
		if partition >= 0 {
			rec.Partition = partition
		}
		recs[i] = rec
	}

	results := make([]*model.ProduceResult, len(messages))
	for i := range results {
		results[i] = &model.ProduceResult{Index: i}
	}

	// ProduceSync 按完成顺序返回结果而非输入顺序,故按 record 指针映射回
	// 输入序,保证每个 ProduceResult 归属其原始消息;成功的 record 上
	// partition/offset 已被回填,逐条读出。
	byRec := make(map[*kgo.Record]int, len(recs))
	for i, rec := range recs {
		byRec[rec] = i
	}
	for _, pr := range c.kcl.ProduceSync(ctx, recs...) {
		// kgo 保证每个传入 ProduceSync 的 record 恰好被 promise 一次,
		// ok 检查只是把这一不变量显式化(map 零值索引会误写 results[0])。
		i, ok := byRec[pr.Record]
		if !ok {
			continue
		}
		res := results[i]
		if pr.Err != nil {
			res.Error = fmt.Sprintf("produce record: %v", pr.Err)
			continue
		}
		res.Partition = pr.Record.Partition
		res.Offset = pr.Record.Offset
	}
	return results, nil
}

// fetch collects up to limit records from the given partitions using a
// consumer from the connection-level browse pool. 复用实例时先重设消费状态
// (见 resetBrowseAssignment),保证不残留上一次浏览的 assignment/offset。
func (c *Client) fetch(ctx context.Context, topic string, offsets map[int32]kgo.Offset, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = defaultFetchLimit
	}
	pool := c.poolForBrowse()
	consumer, prev := pool.acquire()
	if consumer == nil {
		var err error
		if consumer, err = c.newBrowseConsumer(topic, offsets); err != nil {
			return nil, fmt.Errorf("create consumer: %w", err)
		}
	} else {
		resetBrowseAssignment(consumer, prev, topic, offsets)
	}
	// 归还:池已失效或已有空闲(并发浏览超限)时在 release 内直接 Close。
	defer pool.release(consumer, map[string][]int32{topic: partitionKeys(offsets)})

	// Bound the fetch so empty or end-of-log partitions return promptly rather
	// than blocking until the caller's deadline.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	msgs := make([]*model.Message, 0, limit)
	for len(msgs) < limit {
		want := limit - len(msgs)
		fetches := consumer.PollRecords(ctx, want)
		if err := fetches.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				break
			}
			return msgs, fmt.Errorf("poll records: %w", err)
		}
		recs := fetches.Records()
		if len(recs) == 0 {
			break
		}
		for _, r := range recs {
			msgs = append(msgs, toMessage(r))
		}
		if len(recs) < want {
			// Fewer records arrived than requested: we may be near the end of
			// the log, or records may still be trickling in from other
			// partitions. Drain with short timeouts until a poll returns
			// nothing, then return a stable batch.
			for len(msgs) < limit {
				short, scancel := context.WithTimeout(ctx, 200*time.Millisecond)
				more := consumer.PollRecords(short, limit-len(msgs))
				scancel()
				if len(more.Records()) == 0 {
					return msgs, nil
				}
				for _, r := range more.Records() {
					msgs = append(msgs, toMessage(r))
				}
			}
			break
		}
	}
	return msgs, nil
}

// newKgoBrowseConsumer builds a browse consumer from the connection options
// with the initial direct assignment set to this browse's topic+offsets.
func (c *Client) newKgoBrowseConsumer(topic string, offsets map[int32]kgo.Offset) (*kgo.Client, error) {
	return kgo.NewClient(
		append(append([]kgo.Opt{}, c.opts...),
			kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{topic: offsets}),
		)...,
	)
}

// resetBrowseAssignment re-points a reused browse consumer at this browse's
// assignment, so no assignment/offset of the previous browse survives:
//
//  1. RemoveConsumePartitions drops the previous partitions of the same
//     topic. kgo treats removed partitions with assignInvalidateMatching:
//     in-flight fetches are invalidated and buffered fetches are dropped,
//     and the old offsets go away with the assignment. 重新 Add 虽也能改
//     offset,但按 kgo 的 direct-assignment 路径不会失效在途/已缓冲的
//     fetch,旧 offset 的记录可能串进本次浏览,故必须先 Remove;
//  2. topics 不再被浏览的用 PurgeTopicsFromClient 彻底清除——kgo 文档明确
//     RemoveConsumePartitions 不清除 topic 概念,元数据仍会周期性拉取;
//  3. AddConsumePartitions 按本次浏览显式指定的 offsets 重新 assignment
//     (仅作用于 direct、非 regex 的 consumer,即浏览 consumer 的形态)。
func resetBrowseAssignment(consumer *kgo.Client, prev map[string][]int32, topic string, offsets map[int32]kgo.Offset) {
	var purge []string
	for t, ps := range prev {
		if t == topic {
			consumer.RemoveConsumePartitions(map[string][]int32{t: ps})
			continue
		}
		purge = append(purge, t)
	}
	if len(purge) > 0 {
		consumer.PurgeTopicsFromClient(purge...)
	}
	consumer.AddConsumePartitions(map[string]map[int32]kgo.Offset{topic: offsets})
}

// partitionKeys returns the partitions covered by an offset map, used to
// record the installed assignment of a pooled consumer.
func partitionKeys(offsets map[int32]kgo.Offset) []int32 {
	ps := make([]int32, 0, len(offsets))
	for p := range offsets {
		ps = append(ps, p)
	}
	return ps
}

// toMessage converts a kgo.Record into the model.Message exposed to the UI.
func toMessage(r *kgo.Record) *model.Message {
	m := &model.Message{
		Partition: r.Partition,
		Offset:    r.Offset,
		Timestamp: r.Timestamp.UnixMilli(),
		Key:       string(r.Key),
		Value:     string(r.Value),
	}
	for _, h := range r.Headers {
		m.Headers = append(m.Headers, model.Header{Key: h.Key, Value: string(h.Value)})
	}
	return m
}
