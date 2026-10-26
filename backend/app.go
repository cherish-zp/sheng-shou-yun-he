// Package backend exposes the application API to the Wails frontend. Each
// exported method is bound to the UI and talks to the service layer.
//
// Bound methods intentionally do NOT take a context.Context parameter: Wails
// v2 marshals arguments as JSON and cannot inject a Go context. Instead the
// app stores the context handed to Startup (or tests may SetContext) and each
// call derives a bounded sub-context from it.
package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sheng-shou-yun-he/backend/internal/model"
	"sheng-shou-yun-he/backend/internal/service"
	"sheng-shou-yun-he/backend/internal/store"
)

// methodTimeout bounds each individual frontend call.
const methodTimeout = 60 * time.Second

// ResolveMasterKey 按优先级解析落盘加密主密钥,供两个入口程序(main.go 与
// cmd/dbclient)复用——backend/internal 对模块根不可见,故由此转发:
// DB_CLIENT_MASTER > ~/.db-client/master.key 文件 > 首次生成 32 字节随机
// hex(0600 落盘);全部不可用回退内置默认并向 warn 告警。绝不输出密钥
// 内容。实现与测试见 store 包。
func ResolveMasterKey(envValue, keyPath string, warn io.Writer) (string, store.MasterKeySource) {
	return store.ResolveMasterPassword(envValue, keyPath, warn)
}

// App is the Wails application root. Methods on it are exposed to the frontend.
type App struct {
	svc *service.Service
	// ctx is the Wails runtime context handed to Startup; dialog calls need it.
	ctx context.Context
	// dialog resolves the native save-file dialog; replaceable in tests. nil
	// falls back to defaultSaveDialog.
	dialog func(ctx context.Context, opts SaveDialogOptions) (string, error)
	// --- 更新引擎依赖(测试可注入,见 update.go) ---
	// baseURL 是 Gitee API 地址;fallbackURL 是回退的 GitHub API 地址;
	// httpClient 用于探测/下载;applyCmd 拦截安装脚本的启动;stagingDir
	// 记录下载解包后的暂存位置;downloadURLCheck 校验下载入口 URL(https +
	// 域名白名单),nil 时用生产实现,测试注入旁路。
	baseURL     atomic.Value
	fallbackURL atomic.Value
	httpClient  *http.Client
	applyCmd    func(cmd *exec.Cmd) error
	dl          *downloadState
	stagingDir  atomic.Value
	// downloadURLCheck 是更新下载入口 URL 的校验函数(测试注入点)。
	downloadURLCheck func(url string) error
}

// NewApp builds the application root around the service layer.
func NewApp(svc *service.Service) *App {
	return &App{svc: svc}
}

// Startup receives the Wails runtime context. main.go wires it to OnStartup;
// without it the native dialogs cannot be opened.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}

// newContext returns a per-call context with a timeout. Bound methods do not
// accept a context (Wails v2 cannot marshal one), so each call is bounded by
// methodTimeout.
func (a *App) newContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), methodTimeout)
}

// auditResult classifies an operation outcome for the audit log.
func auditResult(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

// auditDetail returns the failure detail, or an empty string on success.
func auditDetail(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// audit best-effort records a dangerous operation. A failed write must never
// change the outcome of the operation being audited, so the error is dropped.
func (a *App) audit(connectionID, action, target, result, detail string) {
	_ = a.svc.RecordAudit(context.Background(), &model.AuditEntry{
		ConnectionID: connectionID,
		Action:       action,
		Target:       target,
		Result:       result,
		Detail:       detail,
	})
}

// joinAuditList renders items for an audit field, capping the joined list at
// 10 items followed by an ellipsis.
func joinAuditList(items []string, sep string) string {
	if len(items) <= 10 {
		return strings.Join(items, sep)
	}
	return strings.Join(items[:10], sep) + "…"
}

// joinAuditTargets renders topic names for the batch-delete audit target,
// capping the joined list at 10 names followed by an ellipsis.
func joinAuditTargets(names []string) string {
	return joinAuditList(names, ", ")
}

// joinAuditFailureDetails renders per-topic failure details for the batch-delete
// audit detail, capping the joined list at 10 entries followed by an ellipsis.
func joinAuditFailureDetails(failures []string) string {
	return joinAuditList(failures, "; ")
}

// connectionIDOf best-effort returns a connection's id, guarding nil input so
// a malformed create request cannot panic in the audit path.
func connectionIDOf(c *model.Connection) string {
	if c == nil {
		return ""
	}
	return c.ID
}

// nameOf best-effort returns a connection's name, guarding nil input.
func nameOf(c *model.Connection) string {
	if c == nil {
		return ""
	}
	return c.Name
}

// ConsumeRequest carries the parameters for a message fetch.
type ConsumeRequest struct {
	ConnectionID string `json:"connection_id"`
	Topic        string `json:"topic"`
	Partition    int32  `json:"partition"`
	Offset       int64  `json:"offset"`
	TimestampMS  int64  `json:"timestamp_ms,omitempty"`
	Limit        int    `json:"limit"`
}

// ActiveMembersRequest carries the parameters for listing active producers or
// consumers on a topic for a consumer group.
type ActiveMembersRequest struct {
	ConnectionID string `json:"connection_id"`
	Group        string `json:"group"`
	Topic        string `json:"topic"`
}

// ResetOffsetRequest carries the parameters for resetting a group offset.
type ResetOffsetRequest struct {
	ConnectionID string                `json:"connection_id"`
	Group        string                `json:"group"`
	Topic        string                `json:"topic"`
	Mode         model.ResetOffsetMode `json:"mode"`
	TimestampMS  int64                 `json:"timestamp_ms,omitempty"`
	// PerPartitionOffsets carries the explicit targets for mode "offset"
	// (ResetOffsetExplicit); ignored by every other mode.
	PerPartitionOffsets map[int32]int64 `json:"per_partition_offsets,omitempty"`
}

// CreateTopicRequest carries the parameters for creating a Kafka topic.
type CreateTopicRequest struct {
	ConnectionID      string `json:"connection_id"`
	Topic             string `json:"topic"`
	Partitions        int32  `json:"partitions"`
	ReplicationFactor int16  `json:"replication_factor"`
}

// DeleteTopicRequest carries the parameters for deleting a Kafka topic.
type DeleteTopicRequest struct {
	ConnectionID string `json:"connection_id"`
	Topic        string `json:"topic"`
}

// DeleteTopicsRequest carries the parameters for batch-deleting Kafka topics.
type DeleteTopicsRequest struct {
	ConnectionID string   `json:"connection_id"`
	Names        []string `json:"names"`
}

// AlterTopicConfigRequest carries the parameters for editing a topic's
// whitelisted configs.
type AlterTopicConfigRequest struct {
	ConnectionID string                   `json:"connection_id"`
	Topic        string                   `json:"topic"`
	Entries      []model.TopicConfigEntry `json:"entries"`
}

// AlterTopicPartitionsRequest carries the parameters for growing a topic to a
// final partition count (Kafka cannot shrink partitions).
type AlterTopicPartitionsRequest struct {
	ConnectionID string `json:"connection_id"`
	Topic        string `json:"topic"`
	Partitions   int32  `json:"partitions"`
}

// DeleteConsumerGroupRequest carries the parameters for deleting a consumer group.
type DeleteConsumerGroupRequest struct {
	ConnectionID string `json:"connection_id"`
	Group        string `json:"group"`
}

// ProduceRequest carries the parameters for publishing a record.
type ProduceRequest struct {
	ConnectionID string `json:"connection_id"`
	Topic        string `json:"topic"`
	Partition    int32  `json:"partition"`
	Key          string `json:"key"`
	Value        string `json:"value"`
}

// BatchProduceRequest carries the parameters for publishing a batch of records.
type BatchProduceRequest struct {
	ConnectionID string                      `json:"connection_id"`
	Topic        string                      `json:"topic"`
	Partition    int32                       `json:"partition"`
	Messages     []model.BatchProduceMessage `json:"messages"`
}

// CreateTopic creates a topic on a connection's cluster.
func (a *App) CreateTopic(req CreateTopicRequest) error {
	ctx, cancel := a.newContext()
	defer cancel()
	err := a.svc.CreateTopic(ctx, req.ConnectionID, req.Topic, req.Partitions, req.ReplicationFactor)
	a.audit(req.ConnectionID, "create_topic", req.Topic, auditResult(err), auditDetail(err))
	return err
}

// DeleteTopic removes a topic from a connection's cluster.
func (a *App) DeleteTopic(req DeleteTopicRequest) error {
	ctx, cancel := a.newContext()
	defer cancel()
	err := a.svc.DeleteTopic(ctx, req.ConnectionID, req.Topic)
	a.audit(req.ConnectionID, "delete_topic", req.Topic, auditResult(err), auditDetail(err))
	return err
}

// DeleteTopics removes several topics from a connection's cluster and returns
// one result per topic. The audit target is the joined topic names (capped at
// 10) and the result is ok only when every topic was deleted.
func (a *App) DeleteTopics(req DeleteTopicsRequest) ([]*model.TopicDeleteResult, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	target := joinAuditTargets(req.Names)
	results, err := a.svc.DeleteTopics(ctx, req.ConnectionID, req.Names)
	if err != nil {
		a.audit(req.ConnectionID, "delete_topics", target, "error", auditDetail(err))
		return nil, err
	}
	result := "ok"
	var failures []string
	for _, r := range results {
		if r.Error != "" {
			result = "error"
			failures = append(failures, fmt.Sprintf("%s: %s", r.Name, r.Error))
		}
	}
	a.audit(req.ConnectionID, "delete_topics", target, result, joinAuditFailureDetails(failures))
	return results, nil
}

// DeleteConsumerGroup removes a consumer group from a connection's cluster.
func (a *App) DeleteConsumerGroup(req DeleteConsumerGroupRequest) error {
	ctx, cancel := a.newContext()
	defer cancel()
	err := a.svc.DeleteConsumerGroup(ctx, req.ConnectionID, req.Group)
	a.audit(req.ConnectionID, "delete_consumer_group", req.Group, auditResult(err), auditDetail(err))
	return err
}

// maskConnectionSecrets 返回 c 的浅拷贝,并把 config JSON 中的敏感凭据字段
// 替换为空串(纯函数:入参不修改)。敏感字段清单与 store 落盘加密、service
// 层 RestoreSecrets 的回填一致:kafka 为 sasl.password;redis/clickhouse/
// mysql/tidb/postgres/hive 为 password;es 为 password + api_key。App 层
// 回传 WebView 的出口(List/Get/Create/Update)统一调用:明文密码只留在
// 后端进程内,连接池与内部逻辑仍用真实值。
func maskConnectionSecrets(c *model.Connection) *model.Connection {
	if c == nil {
		return nil
	}
	masked := *c
	if len(c.Config) == 0 {
		return &masked
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(c.Config, &root); err != nil {
		// config 不是 JSON 对象时没有结构化凭据可剥,原样返回。
		return &masked
	}
	changed := false
	blank := func(m map[string]json.RawMessage, key string) {
		raw, ok := m[key]
		if !ok {
			return
		}
		var v string
		if err := json.Unmarshal(raw, &v); err == nil && v != "" {
			m[key] = json.RawMessage(`""`)
			changed = true
		}
	}
	switch c.Type {
	case model.ConnectionTypeKafka:
		if saslRaw, ok := root["sasl"]; ok {
			var sasl map[string]json.RawMessage
			if json.Unmarshal(saslRaw, &sasl) == nil {
				blank(sasl, "password")
				root["sasl"] = model.MustConfigJSON(sasl)
			}
		}
	case model.ConnectionTypeES:
		blank(root, "password")
		blank(root, "api_key")
	default:
		blank(root, "password")
	}
	if changed {
		masked.Config = model.MustConfigJSON(root)
	}
	return &masked
}

// CreateConnection validates and saves a new connection.
func (a *App) CreateConnection(c *model.Connection) (*model.Connection, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	created, err := a.svc.CreateConnection(ctx, c)
	if err != nil {
		a.audit(connectionIDOf(c), "create_connection", nameOf(c), "error", auditDetail(err))
		return nil, err
	}
	a.audit(created.ID, "create_connection", created.Name, "ok", "")
	return maskConnectionSecrets(created), nil
}

// UpdateConnectionRequest carries the edited fields of an existing connection.
// Config shares the CreateConnection config JSON shape; the id locates the row
// and must already exist.
type UpdateConnectionRequest struct {
	ID     string               `json:"id"`
	Name   string               `json:"name"`
	Type   model.ConnectionType `json:"type,omitempty"`
	Config json.RawMessage      `json:"config"`
}

// resolvedType defaults empty requests to kafka (the only type that existed
// before the field was introduced).
func (r UpdateConnectionRequest) resolvedType() model.ConnectionType {
	if r.Type == "" {
		return model.ConnectionTypeKafka
	}
	return r.Type
}

// UpdateConnection validates and overwrites an existing connection, keeping its
// id and created_at while refreshing updated_at. The connection type is a
// property of the stored row; only Kafka sources are editable from the UI, so
// the request carries no type and kafka is assumed.
func (a *App) UpdateConnection(req UpdateConnectionRequest) (*model.Connection, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	c := &model.Connection{
		ID:     req.ID,
		Name:   req.Name,
		Type:   req.resolvedType(),
		Config: req.Config,
	}
	if err := a.svc.UpdateConnection(ctx, c); err != nil {
		a.audit(req.ID, "update_connection", req.Name, "error", auditDetail(err))
		return nil, err
	}
	a.audit(c.ID, "update_connection", c.Name, "ok", "")
	// Read the persisted row back so the caller receives the complete record
	// (id/created_at preserved, updated_at refreshed). 回传前剥离敏感字段。
	updated, err := a.svc.GetConnection(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return maskConnectionSecrets(updated), nil
}

// ListConnections returns all saved connections. 敏感凭据字段在回传前统一
// 脱敏为空串(maskConnectionSecrets),前端展示不受影响。
func (a *App) ListConnections() ([]*model.Connection, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	list, err := a.svc.ListConnections(ctx)
	if err != nil {
		return nil, err
	}
	masked := make([]*model.Connection, len(list))
	for i, c := range list {
		masked[i] = maskConnectionSecrets(c)
	}
	return masked, nil
}

// ReorderConnections 按前端提交的完整有序 id 列表持久化连接顺序;列表
// 顺序保存到 SQLite,重启后保留。
func (a *App) ReorderConnections(ids []string) error {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.ReorderConnections(ctx, ids)
}

// GetConnection returns a single saved connection. 敏感凭据字段在回传前
// 统一脱敏为空串(maskConnectionSecrets)。
func (a *App) GetConnection(id string) (*model.Connection, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	c, err := a.svc.GetConnection(ctx, id)
	if err != nil {
		return nil, err
	}
	return maskConnectionSecrets(c), nil
}

// DeleteConnection removes a connection and closes any pooled client.
func (a *App) DeleteConnection(id string) error {
	ctx, cancel := a.newContext()
	defer cancel()
	// Prefer the human-readable name as the audit target when available.
	target := id
	if c, err := a.svc.GetConnection(ctx, id); err == nil && c.Name != "" {
		target = c.Name
	}
	err := a.svc.DeleteConnection(ctx, id)
	a.audit(id, "delete_connection", target, auditResult(err), auditDetail(err))
	return err
}

// TestConnectionRequest carries an unsaved Kafka config plus the optional id
// of the connection being edited. 编辑既有连接时列表/详情回传的密码已脱敏,
// 前端不重输密码 → 后端用已存配置回填空敏感字段(fillTestSecrets)再探活;
// 纯新建(connection_id 为空)按提交的配置原样测试。
type TestConnectionRequest struct {
	ConnectionID string            `json:"connection_id,omitempty"`
	Config       model.KafkaConfig `json:"config"`
}

// fillTestSecrets 把连接测试配置中留空的敏感字段用 connectionID 已存配置
// 回填(与 UpdateConnection 的「留空=不变」同规则,见 service.RestoreSecrets)。
// connectionID 为空(纯新建测试)、连接不存在或配置解析失败时保持原配置。
// cfg 须为指向具体 Config 结构的指针,回填就地生效。
func (a *App) fillTestSecrets(ctx context.Context, connectionID string, cfg any) {
	if connectionID == "" {
		return
	}
	old, err := a.svc.GetConnection(ctx, connectionID)
	if err != nil || old == nil {
		return
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return
	}
	// RestoreSecrets 的产物要么是 raw 本身、要么是同形的合法 JSON,对同类型
	// 结构反序列化不会失败。
	_ = json.Unmarshal(service.RestoreSecrets(old.Type, old.Config, raw), cfg)
}

// TestConnection verifies connectivity to a config without saving it.
func (a *App) TestConnection(req TestConnectionRequest) error {
	ctx, cancel := a.newContext()
	defer cancel()
	a.fillTestSecrets(ctx, req.ConnectionID, &req.Config)
	return a.svc.TestConnection(ctx, req.Config)
}

// Connect establishes a pooled connection by id.
func (a *App) Connect(id string) error {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.ConnectConnection(ctx, id)
}

// Disconnect closes a pooled connection by id.
func (a *App) Disconnect(id string) error {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.CloseConnection(ctx, id)
}

// ListTopics lists topics for a connection.
func (a *App) ListTopics(id string) ([]*model.Topic, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.ListTopics(ctx, id)
}

// DescribeTopic returns a topic's partition topology and key configs.
func (a *App) DescribeTopic(id, topic string) (*model.TopicDetail, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.DescribeTopic(ctx, id, topic)
}

// AlterTopicConfig applies the whitelisted topic config values in req.
func (a *App) AlterTopicConfig(req AlterTopicConfigRequest) error {
	ctx, cancel := a.newContext()
	defer cancel()
	err := a.svc.AlterTopicConfig(ctx, req.ConnectionID, req.Topic, req.Entries)
	detail := auditDetail(err)
	if err == nil {
		keys := make([]string, 0, len(req.Entries))
		for _, e := range req.Entries {
			keys = append(keys, e.Key)
		}
		detail = strings.Join(keys, ", ")
	}
	a.audit(req.ConnectionID, "alter_topic_config", req.Topic, auditResult(err), detail)
	return err
}

// AlterTopicPartitions grows a topic to the requested final partition count.
func (a *App) AlterTopicPartitions(req AlterTopicPartitionsRequest) error {
	ctx, cancel := a.newContext()
	defer cancel()
	err := a.svc.AlterTopicPartitions(ctx, req.ConnectionID, req.Topic, req.Partitions)
	a.audit(req.ConnectionID, "alter_topic_partitions", req.Topic, auditResult(err), auditDetail(err))
	return err
}

// GetTopicMessageCounts returns per-topic record counts derived from broker
// offsets for the requested topics (read-only, not audited).
func (a *App) GetTopicMessageCounts(id string, topics []string) (map[string]model.TopicMessageCounts, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.GetTopicMessageCounts(ctx, id, topics...)
}

// SaveDialogOptions carries the native save-dialog inputs, decoupled from the
// wails runtime type so tests can inject a fake dialog.
type SaveDialogOptions struct {
	DefaultFilename string
}

// SaveTextFileRequest carries one export payload to store on disk. The
// filename seeds the dialog's default (its extension drives the file type);
// content is written verbatim as UTF-8.
type SaveTextFileRequest struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
	Mime     string `json:"mime,omitempty"`
}

// SaveTextFile asks the user where to store an export via the native save
// dialog and writes the content. Exports must bypass the WebView: WKWebView
// has no download delegate, so <a download> anchor clicks are silently
// dropped. A cancelled dialog returns an empty path and no error — callers
// treat it as a silent no-op.
func (a *App) SaveTextFile(req SaveTextFileRequest) (string, error) {
	dialog := a.dialog
	if dialog == nil {
		dialog = defaultSaveDialog
	}
	path, err := dialog(a.ctx, SaveDialogOptions{DefaultFilename: req.Filename})
	if err != nil {
		return "", fmt.Errorf("保存对话框: %w", err)
	}
	if path == "" {
		return "", nil
	}
	if err := os.WriteFile(path, []byte(req.Content), 0o644); err != nil {
		return "", fmt.Errorf("写入 %s: %w", path, err)
	}
	return path, nil
}

// defaultSaveDialog bridges to the wails runtime dialog; it needs the
// Startup-provided context, which only exists once the app is wired to Wails.
func defaultSaveDialog(ctx context.Context, opts SaveDialogOptions) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("应用尚未初始化，无法打开保存对话框")
	}
	return runtime.SaveFileDialog(ctx, runtime.SaveDialogOptions{DefaultFilename: opts.DefaultFilename})
}

// DescribeCluster returns broker topology, controller, Kafka version and
// under-replicated partitions for a connection's cluster.
func (a *App) DescribeCluster(id string) (*model.ClusterHealth, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.DescribeCluster(ctx, id)
}

// ListConsumerGroups lists consumer groups (with lag) for a connection.
func (a *App) ListConsumerGroups(id string) ([]*model.ConsumerGroup, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.ListConsumerGroups(ctx, id)
}

// DescribeGroup returns a consumer group's state and member topology with
// per-topic partition assignments.
func (a *App) DescribeGroup(id, group string) (*model.GroupDetail, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.DescribeGroup(ctx, id, group)
}

// ConsumeMessages fetches a batch of messages for a connection.
func (a *App) ConsumeMessages(req ConsumeRequest) ([]*model.Message, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.ConsumeMessages(ctx, req.ConnectionID, req.Topic, req.Partition, req.Offset, req.Limit)
}

// ConsumeMessagesByTimestamp fetches messages at or after a unix-ms timestamp.
func (a *App) ConsumeMessagesByTimestamp(req ConsumeRequest) ([]*model.Message, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.ConsumeMessagesByTimestamp(ctx, req.ConnectionID, req.Topic, req.Partition, req.TimestampMS, req.Limit)
}

// GetPartitionLag returns per-partition lag for a group on a topic.
func (a *App) GetPartitionLag(id, topic, group string) (map[int32]int64, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.GetPartitionLag(ctx, id, topic, group)
}

// ListActiveProducers returns the producers currently producing to a topic.
func (a *App) ListActiveProducers(req ActiveMembersRequest) ([]*model.ActiveProducer, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.ListActiveProducers(ctx, req.ConnectionID, req.Topic)
}

// ListActiveConsumers returns the group members assigned to a topic.
func (a *App) ListActiveConsumers(req ActiveMembersRequest) ([]*model.ActiveConsumer, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.ListActiveConsumers(ctx, req.ConnectionID, req.Group, req.Topic)
}

// ResetConsumerGroupOffset resets a consumer group offset.
func (a *App) ResetConsumerGroupOffset(req ResetOffsetRequest) error {
	ctx, cancel := a.newContext()
	defer cancel()
	err := a.svc.ResetConsumerGroupOffset(ctx, req.ConnectionID, req.Group, req.Topic, req.Mode, req.TimestampMS, req.PerPartitionOffsets)
	detail := auditDetail(err)
	if err == nil {
		detail = string(req.Mode)
		if req.TimestampMS > 0 {
			detail = fmt.Sprintf("%s(%d)", req.Mode, req.TimestampMS)
		}
	}
	a.audit(req.ConnectionID, "reset_group_offset", req.Group+"/"+req.Topic, auditResult(err), detail)
	return err
}

// PreviewResetOffset returns the per-partition target offsets a reset in the
// given mode would commit, without altering anything (read-only dry-run
// preview). It reuses the ResetOffsetRequest wire shape; the group is context
// only and is never modified.
func (a *App) PreviewResetOffset(req ResetOffsetRequest) (map[int32]int64, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.PreviewResetOffset(ctx, req.ConnectionID, req.Topic, req.Mode, req.TimestampMS)
}

// ListAudit returns the most recent audit entries, newest first. A non-positive
// limit falls back to the store default (200).
func (a *App) ListAudit(limit int) ([]*model.AuditEntry, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.ListAudit(ctx, limit)
}

// ProduceMessage publishes a record to a connection.
func (a *App) ProduceMessage(req ProduceRequest) error {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.ProduceMessage(ctx, req.ConnectionID, req.Topic, req.Partition, []byte(req.Key), []byte(req.Value))
}

// ProduceMessages publishes a batch of records to a connection and returns one
// result per message.
func (a *App) ProduceMessages(req BatchProduceRequest) ([]*model.ProduceResult, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return a.svc.ProduceMessages(ctx, req.ConnectionID, req.Topic, req.Partition, req.Messages)
}
