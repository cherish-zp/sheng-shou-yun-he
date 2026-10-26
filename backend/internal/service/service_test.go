package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"sheng-shou-yun-he/backend/internal/model"
	"sheng-shou-yun-he/backend/internal/store"
)

// fakeKafka implements KafkaDataSource for service-layer tests.
type fakeKafka struct {
	fakeDataSource
	topics       []*model.Topic
	msgs         []*model.Message
	lag          map[int32]int64
	group        []*model.ConsumerGroup
	producers    []*model.ActiveProducer
	consumers    []*model.ActiveConsumer
	resetCalls   []model.ResetOffsetMode
	preview      map[int32]int64
	previewCalls []previewResetCall
	detail       *model.TopicDetail
	health       *model.ClusterHealth
	groupDg      *model.GroupDetail
	batchCalls   [][]model.BatchProduceMessage
	deleteCalls  [][]string
}

// previewResetCall records the parameters of a PreviewResetOffset delegation.
type previewResetCall struct {
	topic       string
	mode        model.ResetOffsetMode
	timestampMS int64
}

func (f *fakeKafka) ListTopics(context.Context) ([]*model.Topic, error)      { return f.topics, nil }
func (f *fakeKafka) CreateTopic(context.Context, string, int32, int16) error { return nil }
func (f *fakeKafka) DeleteTopic(context.Context, string) error               { return nil }

func (f *fakeKafka) DeleteTopics(_ context.Context, names []string) ([]*model.TopicDeleteResult, error) {
	f.deleteCalls = append(f.deleteCalls, names)
	out := make([]*model.TopicDeleteResult, 0, len(names))
	for _, n := range names {
		out = append(out, &model.TopicDeleteResult{Name: n})
	}
	return out, nil
}
func (f *fakeKafka) DeleteConsumerGroup(context.Context, string) error { return nil }
func (f *fakeKafka) ListConsumerGroups(context.Context) ([]*model.ConsumerGroup, error) {
	return f.group, nil
}
func (f *fakeKafka) ConsumeMessages(_ context.Context, _ string, _ int32, _ int64, _ int) ([]*model.Message, error) {
	return f.msgs, nil
}
func (f *fakeKafka) ConsumeMessagesByTimestamp(_ context.Context, _ string, _ int32, _ int64, _ int) ([]*model.Message, error) {
	return f.msgs, nil
}
func (f *fakeKafka) GetPartitionLag(context.Context, string, string) (map[int32]int64, error) {
	return f.lag, nil
}
func (f *fakeKafka) ResetConsumerGroupOffset(_ context.Context, _, _ string, mode model.ResetOffsetMode, _ int64, _ map[int32]int64) error {
	f.resetCalls = append(f.resetCalls, mode)
	return nil
}
func (f *fakeKafka) PreviewResetOffset(_ context.Context, topic string, mode model.ResetOffsetMode, timestampMS int64) (map[int32]int64, error) {
	f.previewCalls = append(f.previewCalls, previewResetCall{topic: topic, mode: mode, timestampMS: timestampMS})
	return f.preview, nil
}
func (f *fakeKafka) ProduceMessage(_ context.Context, _ string, _ int32, _, _ []byte) error {
	return nil
}

func (f *fakeKafka) ProduceMessages(_ context.Context, _ string, _ int32, msgs []model.BatchProduceMessage) ([]*model.ProduceResult, error) {
	f.batchCalls = append(f.batchCalls, msgs)
	out := make([]*model.ProduceResult, len(msgs))
	for i := range msgs {
		out[i] = &model.ProduceResult{Index: i, Partition: 0, Offset: int64(i)}
	}
	return out, nil
}
func (f *fakeKafka) ListActiveProducers(context.Context, string) ([]*model.ActiveProducer, error) {
	return f.producers, nil
}
func (f *fakeKafka) ListActiveConsumers(context.Context, string, string) ([]*model.ActiveConsumer, error) {
	return f.consumers, nil
}
func (f *fakeKafka) DescribeTopic(context.Context, string) (*model.TopicDetail, error) {
	return f.detail, nil
}
func (f *fakeKafka) AlterTopicConfig(context.Context, string, []model.TopicConfigEntry) error {
	return nil
}

func (f *fakeKafka) AlterTopicPartitions(context.Context, string, int32) error {
	return nil
}

func (f *fakeKafka) GetTopicMessageCounts(context.Context, ...string) (map[string]model.TopicMessageCounts, error) {
	return nil, nil
}
func (f *fakeKafka) DescribeCluster(context.Context) (*model.ClusterHealth, error) {
	return f.health, nil
}

func (f *fakeKafka) DescribeGroup(context.Context, string) (*model.GroupDetail, error) {
	return f.groupDg, nil
}

type fakeFactory struct {
	mu      sync.Mutex
	created []model.KafkaConfig
	err     error
	k       *fakeKafka
}

func (f *fakeFactory) NewKafkaClient(_ context.Context, cfg model.KafkaConfig) (KafkaDataSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, cfg)
	if f.err != nil {
		return nil, f.err
	}
	return f.k, nil
}

func (f *fakeFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created)
}

func newTestService(t *testing.T) (*Service, *fakeFactory) {
	t.Helper()
	path := t.TempDir() + "/config.db"
	st, err := store.Open(path, "test-master")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fakeFactory{k: &fakeKafka{}}
	return NewService(st, f), f
}

func sampleConn() *model.Connection {
	return &model.Connection{
		Name: "local",
		Type: model.ConnectionTypeKafka,
		Config: model.MustConfigJSON(model.KafkaConfig{
			BootstrapServers: []string{"localhost:9092"},
		}),
	}
}

func TestCreateConnectionPersistsAndAssignsID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	if c.ID == "" {
		t.Fatal("connection must receive a generated id")
	}
	got, err := svc.store.GetConnection(c.ID)
	if err != nil {
		t.Fatalf("connection not persisted: %v", err)
	}
	if got.Name != "local" {
		t.Fatalf("unexpected name: %s", got.Name)
	}
}

func TestCreateConnectionValidates(t *testing.T) {
	svc, _ := newTestService(t)
	bad := sampleConn()
	bad.Name = ""
	if _, err := svc.CreateConnection(context.Background(), bad); err == nil {
		t.Fatal("invalid connection must be rejected")
	}
}

func TestUpdateConnectionPersistsChanges(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	createdAt, updatedAt := c.CreatedAt, c.UpdatedAt

	c.Name = "renamed"
	c.Config = model.MustConfigJSON(model.KafkaConfig{BootstrapServers: []string{"broker-a:9092", "broker-b:9092"}, SecurityProtocol: "SSL"})
	if err := svc.UpdateConnection(ctx, c); err != nil {
		t.Fatalf("UpdateConnection: %v", err)
	}
	if c.ID == "" || c.CreatedAt != createdAt {
		t.Fatalf("update must keep id and created_at: id=%q created=%d/%d", c.ID, c.CreatedAt, createdAt)
	}
	if c.UpdatedAt < updatedAt {
		t.Fatalf("updated_at must be refreshed: %d < %d", c.UpdatedAt, updatedAt)
	}
	got, err := svc.store.GetConnection(c.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	gotCfg, err := got.KafkaConfig()
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if got.Name != "renamed" || len(gotCfg.BootstrapServers) != 2 {
		t.Fatalf("changes not persisted: %+v", got)
	}
}

func TestUpdateConnectionValidates(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	bad := *c
	bad.Name = ""
	if err := svc.UpdateConnection(ctx, &bad); err == nil {
		t.Fatal("invalid connection must be rejected")
	}
	badCfg := *c
	badCfg.Config = model.MustConfigJSON(model.KafkaConfig{})
	if err := svc.UpdateConnection(ctx, &badCfg); err == nil {
		t.Fatal("invalid config must be rejected")
	}
	// The stored connection must still hold the original values.
	got, err := svc.store.GetConnection(c.ID)
	if err != nil {
		t.Fatalf("get after rejected update: %v", err)
	}
	gotCfg, err := got.KafkaConfig()
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if got.Name != "local" || len(gotCfg.BootstrapServers) != 1 {
		t.Fatalf("rejected update must not touch the stored row: %+v", got)
	}
}

func TestUpdateConnectionUnknownID(t *testing.T) {
	svc, _ := newTestService(t)
	c := sampleConn()
	c.ID = "nope"
	if err := svc.UpdateConnection(context.Background(), c); err == nil {
		t.Fatal("updating a missing connection must fail")
	}
}

func TestUpdateConnectionEvictsPooledClient(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.ListTopics(ctx, c.ID); err != nil {
		t.Fatalf("auto-connect: %v", err)
	}
	if _, err := svc.pool.Get(c.ID); err != nil {
		t.Fatal("client should be pooled after auto-connect")
	}
	if err := svc.UpdateConnection(ctx, c); err != nil {
		t.Fatalf("UpdateConnection: %v", err)
	}
	if _, err := svc.pool.Get(c.ID); err == nil {
		t.Fatal("pooled client must be evicted after update")
	}
	// The next operation must build a fresh client from the edited config.
	if _, err := svc.ListTopics(ctx, c.ID); err != nil {
		t.Fatalf("list after update: %v", err)
	}
	if f.count() != 2 {
		t.Fatalf("expected a second client build after eviction, got %d", f.count())
	}
}

func TestTestConnection(t *testing.T) {
	svc, f := newTestService(t)
	sampleCfg, err := sampleConn().KafkaConfig()
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if err := svc.TestConnection(context.Background(), sampleCfg); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if f.count() != 1 {
		t.Fatalf("factory should have created 1 client, got %d", f.count())
	}
}

func TestTestConnectionFailure(t *testing.T) {
	svc, f := newTestService(t)
	f.err = errors.New("boom")
	sampleCfg, err := sampleConn().KafkaConfig()
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if err := svc.TestConnection(context.Background(), sampleCfg); err == nil {
		t.Fatal("TestConnection must surface client errors")
	}
}

func TestListTopicsAutoConnects(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f.k.topics = []*model.Topic{{Name: "t1"}}

	topics, err := svc.ListTopics(ctx, c.ID)
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}
	if len(topics) != 1 || topics[0].Name != "t1" {
		t.Fatalf("unexpected topics: %+v", topics)
	}
	if f.count() != 1 {
		t.Fatalf("expected client auto-created, got %d", f.count())
	}
	// The connection must now be pooled.
	if ds, err := svc.pool.Get(c.ID); err != nil || ds == nil {
		t.Fatal("connection should be pooled after auto-connect")
	}
}

func TestListTopicsUnknownConnection(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.ListTopics(context.Background(), "nope"); err == nil {
		t.Fatal("unknown connection must fail")
	}
}

func TestListConsumerGroupsDelegates(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, _ := svc.CreateConnection(ctx, sampleConn())
	f.k.group = []*model.ConsumerGroup{{Name: "g1"}}

	groups, err := svc.ListConsumerGroups(ctx, c.ID)
	if err != nil {
		t.Fatalf("ListConsumerGroups: %v", err)
	}
	if len(groups) != 1 || groups[0].Name != "g1" {
		t.Fatalf("unexpected groups: %+v", groups)
	}
}

func TestConsumeMessagesDelegates(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, _ := svc.CreateConnection(ctx, sampleConn())
	f.k.msgs = []*model.Message{{Partition: 0, Offset: 1}}

	msgs, err := svc.ConsumeMessages(ctx, c.ID, "t1", 0, model.OffsetEarliest, 10)
	if err != nil {
		t.Fatalf("ConsumeMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Offset != 1 {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
}

func TestGetPartitionLagDelegates(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, _ := svc.CreateConnection(ctx, sampleConn())
	f.k.lag = map[int32]int64{0: 5}

	lag, err := svc.GetPartitionLag(ctx, c.ID, "t1", "g1")
	if err != nil {
		t.Fatalf("GetPartitionLag: %v", err)
	}
	if lag[0] != 5 {
		t.Fatalf("unexpected lag: %+v", lag)
	}
}

func TestResetConsumerGroupOffsetDelegates(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, _ := svc.CreateConnection(ctx, sampleConn())

	if err := svc.ResetConsumerGroupOffset(ctx, c.ID, "g1", "t1", model.ResetOffsetEarliest, 0, nil); err != nil {
		t.Fatalf("ResetConsumerGroupOffset: %v", err)
	}
	if len(f.k.resetCalls) != 1 || f.k.resetCalls[0] != model.ResetOffsetEarliest {
		t.Fatalf("unexpected reset calls: %+v", f.k.resetCalls)
	}
}

func TestPreviewResetOffsetDelegates(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, _ := svc.CreateConnection(ctx, sampleConn())
	f.k.preview = map[int32]int64{0: 3, 1: 8}

	got, err := svc.PreviewResetOffset(ctx, c.ID, "t1", model.ResetOffsetTime, 1700000000000)
	if err != nil {
		t.Fatalf("PreviewResetOffset: %v", err)
	}
	if got[0] != 3 || got[1] != 8 {
		t.Fatalf("unexpected preview: %+v", got)
	}
	if len(f.k.previewCalls) != 1 {
		t.Fatalf("expected 1 preview call, got %d", len(f.k.previewCalls))
	}
	call := f.k.previewCalls[0]
	if call.topic != "t1" || call.mode != model.ResetOffsetTime || call.timestampMS != 1700000000000 {
		t.Fatalf("preview must be delegated verbatim, got %+v", call)
	}
}

func TestCloseConnectionRemovesFromPool(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	c, _ := svc.CreateConnection(ctx, sampleConn())
	if _, err := svc.ListTopics(ctx, c.ID); err != nil {
		t.Fatalf("auto-connect: %v", err)
	}
	if err := svc.CloseConnection(ctx, c.ID); err != nil {
		t.Fatalf("CloseConnection: %v", err)
	}
	if _, err := svc.pool.Get(c.ID); err == nil {
		t.Fatal("connection must be removed from pool after CloseConnection")
	}
}

func TestCloseConnectionNotPooled(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	c, _ := svc.CreateConnection(ctx, sampleConn())
	if err := svc.CloseConnection(ctx, c.ID); err == nil {
		t.Fatal("closing a non-pooled connection must fail")
	}
}

func TestDeleteConnectionRemovesPersisted(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	c, _ := svc.CreateConnection(ctx, sampleConn())
	// Auto-connect so there is a pooled client to clean up.
	if _, err := svc.ListTopics(ctx, c.ID); err != nil {
		t.Fatalf("auto-connect: %v", err)
	}
	if err := svc.DeleteConnection(ctx, c.ID); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}
	if _, err := svc.store.GetConnection(c.ID); err == nil {
		t.Fatal("connection must be removed from store")
	}
	if _, err := svc.pool.Get(c.ID); err == nil {
		t.Fatal("connection must be removed from pool")
	}
}

func TestProduceMessageDelegates(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	c, _ := svc.CreateConnection(ctx, sampleConn())
	if err := svc.ProduceMessage(ctx, c.ID, "t1", 0, []byte("k"), []byte("v")); err != nil {
		t.Fatalf("ProduceMessage: %v", err)
	}
}

func TestProduceMessagesDelegates(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	msgs := []model.BatchProduceMessage{
		{Key: "k-1", Value: "v-1"},
		{Key: "k-2", Value: "v-2"},
	}
	got, err := svc.ProduceMessages(ctx, c.ID, "t1", 0, msgs)
	if err != nil {
		t.Fatalf("ProduceMessages: %v", err)
	}
	if len(f.k.batchCalls) != 1 {
		t.Fatalf("expected 1 batch call, got %d", len(f.k.batchCalls))
	}
	if len(f.k.batchCalls[0]) != 2 || f.k.batchCalls[0][0].Key != "k-1" || f.k.batchCalls[0][1].Value != "v-2" {
		t.Fatalf("batch must be delegated verbatim, got %+v", f.k.batchCalls)
	}
	if len(got) != 2 || got[0].Index != 0 || got[1].Index != 1 || got[1].Offset != 1 {
		t.Fatalf("unexpected results: %+v", got)
	}
}

func TestDeleteTopicsDelegates(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.DeleteTopics(ctx, c.ID, []string{"t2", "t1"})
	if err != nil {
		t.Fatalf("DeleteTopics: %v", err)
	}
	if len(f.k.deleteCalls) != 1 {
		t.Fatalf("expected 1 batch delete call, got %d", len(f.k.deleteCalls))
	}
	if len(f.k.deleteCalls[0]) != 2 || f.k.deleteCalls[0][0] != "t2" || f.k.deleteCalls[0][1] != "t1" {
		t.Fatalf("names must be delegated verbatim, got %+v", f.k.deleteCalls)
	}
	if len(got) != 2 || got[0].Name != "t2" || got[1].Name != "t1" {
		t.Fatalf("unexpected results: %+v", got)
	}
}

func TestDescribeTopicDelegates(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	want := &model.TopicDetail{Name: "t1"}
	f.k.detail = want
	got, err := svc.DescribeTopic(ctx, c.ID, "t1")
	if err != nil {
		t.Fatalf("DescribeTopic: %v", err)
	}
	if got != want {
		t.Fatalf("DescribeTopic must delegate to the data source, got %+v", got)
	}
}

func TestAlterTopicConfigDelegates(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	entries := []model.TopicConfigEntry{{Key: "retention.ms", Value: "123456789"}}
	if err := svc.AlterTopicConfig(ctx, c.ID, "t1", entries); err != nil {
		t.Fatalf("AlterTopicConfig: %v", err)
	}
}

func TestDescribeClusterDelegates(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	want := &model.ClusterHealth{ClusterID: "c-1"}
	f.k.health = want
	got, err := svc.DescribeCluster(ctx, c.ID)
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}
	if got != want {
		t.Fatalf("DescribeCluster must delegate to the data source, got %+v", got)
	}
}

func TestListActiveProducers(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f.k.producers = []*model.ActiveProducer{
		{Topic: "t1", Partition: 0, ProducerID: 101, ProducerEpoch: 2, LastSequence: 9, LastTimestamp: 1700000000000, Leader: 1},
	}
	got, err := svc.ListActiveProducers(ctx, c.ID, "t1")
	if err != nil {
		t.Fatalf("ListActiveProducers: %v", err)
	}
	if len(got) != 1 || got[0].ProducerID != 101 || got[0].Topic != "t1" {
		t.Fatalf("unexpected producers: %+v", got)
	}
}

func TestListActiveConsumers(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f.k.consumers = []*model.ActiveConsumer{
		{MemberID: "m-1", ClientID: "c-1", ClientHost: "10.0.0.1", Partitions: []int32{0, 1}},
	}
	got, err := svc.ListActiveConsumers(ctx, c.ID, "grp", "t1")
	if err != nil {
		t.Fatalf("ListActiveConsumers: %v", err)
	}
	if len(got) != 1 || got[0].MemberID != "m-1" || len(got[0].Partitions) != 2 {
		t.Fatalf("unexpected consumers: %+v", got)
	}
}

func TestDescribeGroupDelegates(t *testing.T) {
	svc, f := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	want := &model.GroupDetail{
		Group:        "grp-1",
		State:        "Stable",
		ProtocolType: "consumer",
		Members: []model.GroupMember{
			{MemberID: "m-1", ClientID: "c-1", Host: "10.0.0.1", Assignment: map[string][]int32{"t1": {0, 1}}},
		},
	}
	f.k.groupDg = want
	got, err := svc.DescribeGroup(ctx, c.ID, "grp-1")
	if err != nil {
		t.Fatalf("DescribeGroup: %v", err)
	}
	if got != want {
		t.Fatalf("DescribeGroup must delegate to the data source, got %+v", got)
	}
}

func TestServiceRecordAndListAudit(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if err := svc.RecordAudit(ctx, &model.AuditEntry{
		ConnectionID: "c1", Action: "create_topic", Target: "t1", Result: "ok",
	}); err != nil {
		t.Fatalf("RecordAudit: %v", err)
	}
	list, err := svc.ListAudit(ctx, 10)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(list) != 1 || list[0].Action != "create_topic" || list[0].Target != "t1" {
		t.Fatalf("unexpected audit list: %+v", list)
	}
}

func TestServiceListAuditDefaultLimit(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := svc.RecordAudit(ctx, &model.AuditEntry{Action: "a", Target: fmt.Sprintf("t%d", i), Result: "ok"}); err != nil {
			t.Fatalf("record #%d: %v", i, err)
		}
	}
	list, err := svc.ListAudit(ctx, 0)
	if err != nil {
		t.Fatalf("ListAudit(0): %v", err)
	}
	if len(list) != 5 || list[0].Target != "t4" {
		t.Fatalf("expected all 5 entries newest-first via default limit, got %+v", list)
	}
}

// TestKafkaColdOpenConcurrentSingleFlight 复现「首开必现 context canceled」:
// 树展开并发发 listTopics + listConsumerGroups,冷池下两个请求各自冷建
// 客户端,Pool.Put 覆盖旧实例时把带在途请求的前一个客户端 Close,在途
// 请求随即报 context canceled。修复后同一连接的冷建必须单飞:并发只建
// 一次客户端,两次调用都成功。
func TestKafkaColdOpenConcurrentSingleFlight(t *testing.T) {
	svc, factory := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 慢化工厂:放大并发冷建窗口(并发两个调用都落在建窗内)。
	base := svc.factory
	svc.factory = ClientFactoryFunc(func(ctx context.Context, cfg model.KafkaConfig) (KafkaDataSource, error) {
		time.Sleep(100 * time.Millisecond)
		return base.NewKafkaClient(ctx, cfg)
	})

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			_, errs[slot] = svc.ListTopics(ctx, c.ID)
		}(i)
	}
	wg.Wait()
	for slot, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", slot, err)
		}
	}
	if got := factory.count(); got != 1 {
		t.Fatalf("并发冷开必须单飞只建一次客户端,实际创建 %d 次", got)
	}
}

// --- 凭据「留空=不变」:RestoreSecrets 纯函数与 UpdateConnection 回填 ---

// decodeJSONAsMap 把 config JSON 解码成 map,便于断言字段存在性。
func decodeJSONAsMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return m
}

func TestRestoreSecretsMysql(t *testing.T) {
	prev := model.MustConfigJSON(model.MysqlConfig{Host: "h", Port: 3306, Username: "u", Password: "old-pw", Database: "d"})

	// 空串密码回填为已存值,其余字段保持 next 的新值。
	next := model.MustConfigJSON(model.MysqlConfig{Host: "h2", Port: 3307, Username: "u2", Password: "", Database: "d2"})
	restored := RestoreSecrets(model.ConnectionTypeMySQL, prev, next)
	m := decodeJSONAsMap(t, restored)
	if m["password"] != "old-pw" || m["host"] != "h2" || m["port"] != float64(3307) {
		t.Fatalf("空密码应回填已存值且保留新字段: %v", m)
	}

	// 非空密码保留 next 值(不回填)。
	next = model.MustConfigJSON(model.MysqlConfig{Host: "h2", Username: "u2", Password: "new-pw"})
	m = decodeJSONAsMap(t, RestoreSecrets(model.ConnectionTypeMySQL, prev, next))
	if m["password"] != "new-pw" {
		t.Fatalf("非空密码必须保留 next 值: %v", m)
	}

	// prev 本身无密码(next 空密码)→ 保持空。
	empty := model.MustConfigJSON(model.MysqlConfig{Host: "h", Password: ""})
	m = decodeJSONAsMap(t, RestoreSecrets(model.ConnectionTypeMySQL, empty, next))
	if m["password"] != "new-pw" {
		t.Fatalf("prev 无密码时 next 非空值不受影响: %v", m)
	}
	m = decodeJSONAsMap(t, RestoreSecrets(model.ConnectionTypeMySQL, empty, model.MustConfigJSON(model.MysqlConfig{Host: "h", Password: ""})))
	// MysqlConfig.Password 带 omitempty,空密码时键省略——语义上仍为「无密码」。
	if v, ok := m["password"]; ok && v != "" {
		t.Fatalf("prev/next 均无密码时必须保持为空: %v", m)
	}
}

func TestRestoreSecretsRedisMissingKey(t *testing.T) {
	// 前端 redis 空密码会省略 password 键(PasswordInput 留空 → undefined),
	// 回填必须覆盖「键缺失」而不只是空串。
	prev := model.MustConfigJSON(model.RedisConfig{Addr: "a:6379", Password: "old-pw", DB: 2})
	next := json.RawMessage(`{"addr":"a:6379","db":3,"tls":true}`)
	m := decodeJSONAsMap(t, RestoreSecrets(model.ConnectionTypeRedis, prev, next))
	if m["password"] != "old-pw" || m["db"] != float64(3) {
		t.Fatalf("缺失的 password 键应回填已存值: %v", m)
	}
	// 显式空串同样回填。
	next = model.MustConfigJSON(model.RedisConfig{Addr: "a:6379", DB: 3})
	m = decodeJSONAsMap(t, RestoreSecrets(model.ConnectionTypeRedis, prev, next))
	if m["password"] != "old-pw" {
		t.Fatalf("空串 password 应回填已存值: %v", m)
	}
}

func TestRestoreSecretsKafkaSasl(t *testing.T) {
	prev := model.MustConfigJSON(model.KafkaConfig{
		BootstrapServers: []string{"h:9092"},
		SASL:             &model.SASLConfig{Enabled: true, Mechanism: "PLAIN", Username: "u", Password: "old-pw"},
	})

	// next SASL 密码留空 → 回填;mechanism/username 等 next 值保留。
	next := model.MustConfigJSON(model.KafkaConfig{
		BootstrapServers: []string{"h2:9092"},
		SASL:             &model.SASLConfig{Enabled: true, Mechanism: "SCRAM-SHA-256", Username: "u2", Password: ""},
	})
	var cfg model.KafkaConfig
	if err := json.Unmarshal(RestoreSecrets(model.ConnectionTypeKafka, prev, next), &cfg); err != nil {
		t.Fatalf("decode restored: %v", err)
	}
	if cfg.SASL == nil || cfg.SASL.Password != "old-pw" || cfg.SASL.Mechanism != "SCRAM-SHA-256" || cfg.SASL.Username != "u2" {
		t.Fatalf("SASL 空密码应回填且保留其余新值: %+v", cfg.SASL)
	}

	// next 未启用 SASL(无 sasl 键,如切回 PLAINTEXT)→ 不回填、原样返回。
	plain := model.MustConfigJSON(model.KafkaConfig{BootstrapServers: []string{"h:9092"}})
	if got := RestoreSecrets(model.ConnectionTypeKafka, prev, plain); string(got) != string(plain) {
		t.Fatalf("next 无 sasl 时必须原样返回: %s", got)
	}

	// 非空密码保留 next 值。
	keep := model.MustConfigJSON(model.KafkaConfig{
		BootstrapServers: []string{"h:9092"},
		SASL:             &model.SASLConfig{Enabled: true, Mechanism: "PLAIN", Username: "u", Password: "new-pw"},
	})
	if err := json.Unmarshal(RestoreSecrets(model.ConnectionTypeKafka, prev, keep), &cfg); err != nil {
		t.Fatalf("decode kept: %v", err)
	}
	if cfg.SASL.Password != "new-pw" {
		t.Fatalf("非空 SASL 密码必须保留 next 值: %+v", cfg.SASL)
	}
}

func TestRestoreSecretsEsApiKey(t *testing.T) {
	prev := json.RawMessage(`{"hosts":["h:9200"],"username":"elastic","password":"old-pw","api_key":"old-key","auth_mode":"basic","tls_mode":"disabled"}`)
	next := json.RawMessage(`{"hosts":["h:9200"],"username":"","password":"","api_key":"","auth_mode":"apikey","tls_mode":"disabled"}`)
	m := decodeJSONAsMap(t, RestoreSecrets(model.ConnectionTypeES, prev, next))
	if m["password"] != "old-pw" || m["api_key"] != "old-key" {
		t.Fatalf("es 的 password 与 api_key 都应回填: %v", m)
	}
}

func TestRestoreSecretsInvalidJSON(t *testing.T) {
	// 任一 config 非 JSON 对象时原样返回 next,不 panic。
	next := json.RawMessage(`{"host":"h"}`)
	if got := RestoreSecrets(model.ConnectionTypeMySQL, json.RawMessage(`not-json`), next); string(got) != string(next) {
		t.Fatalf("prev 非法时应原样返回 next: %s", got)
	}
	if got := RestoreSecrets(model.ConnectionTypeMySQL, nil, next); string(got) != string(next) {
		t.Fatalf("prev 为空时应原样返回 next: %s", got)
	}
}

// TestUpdateConnectionRestoresBlankSecrets 验证编辑「留空=不变」:密码留空
// 保存时后端用已存配置回填,落库的仍是原密码;前端不再需要(也无法)拿到
// 明文密码。覆盖 mysql/redis/kafka SASL 三类形状。
func TestUpdateConnectionRestoresBlankSecrets(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// mysql:编辑留空密码 → 落库保持原密码。
	created, err := svc.CreateConnection(ctx, &model.Connection{
		Name: "my", Type: model.ConnectionTypeMySQL,
		Config: model.MustConfigJSON(model.MysqlConfig{Host: "h", Port: 3306, Username: "u", Password: "secret", Database: "d"}),
	})
	if err != nil {
		t.Fatalf("create mysql: %v", err)
	}
	if err := svc.UpdateConnection(ctx, &model.Connection{
		ID: created.ID, Name: "my2", Type: model.ConnectionTypeMySQL,
		Config: model.MustConfigJSON(model.MysqlConfig{Host: "h", Port: 3306, Username: "u", Password: "", Database: "d"}),
	}); err != nil {
		t.Fatalf("update mysql: %v", err)
	}
	got, err := svc.GetConnection(ctx, created.ID)
	if err != nil {
		t.Fatalf("get mysql: %v", err)
	}
	myCfg, err := got.MysqlConfig()
	if err != nil {
		t.Fatalf("decode mysql: %v", err)
	}
	if myCfg.Password != "secret" {
		t.Fatalf("mysql 留空密码必须回填为原密码,got %q", myCfg.Password)
	}

	// redis:空密码省略键 → 落库保持原密码。
	created, err = svc.CreateConnection(ctx, &model.Connection{
		Name: "rd", Type: model.ConnectionTypeRedis,
		Config: model.MustConfigJSON(model.RedisConfig{Addr: "a:6379", Password: "secret", DB: 1}),
	})
	if err != nil {
		t.Fatalf("create redis: %v", err)
	}
	if err := svc.UpdateConnection(ctx, &model.Connection{
		ID: created.ID, Name: "rd2", Type: model.ConnectionTypeRedis,
		Config: json.RawMessage(`{"addr":"a:6379","db":2}`),
	}); err != nil {
		t.Fatalf("update redis: %v", err)
	}
	got, err = svc.GetConnection(ctx, created.ID)
	if err != nil {
		t.Fatalf("get redis: %v", err)
	}
	rdCfg, err := got.RedisConfig()
	if err != nil {
		t.Fatalf("decode redis: %v", err)
	}
	if rdCfg.Password != "secret" {
		t.Fatalf("redis 省略的密码必须回填为原密码,got %q", rdCfg.Password)
	}

	// kafka SASL:留空密码 → 落库保持原密码。
	created, err = svc.CreateConnection(ctx, &model.Connection{
		Name: "kf", Type: model.ConnectionTypeKafka,
		Config: model.MustConfigJSON(model.KafkaConfig{
			BootstrapServers: []string{"h:9092"}, SecurityProtocol: "SASL_PLAINTEXT",
			SASL: &model.SASLConfig{Enabled: true, Mechanism: "PLAIN", Username: "u", Password: "secret"},
		}),
	})
	if err != nil {
		t.Fatalf("create kafka: %v", err)
	}
	if err := svc.UpdateConnection(ctx, &model.Connection{
		ID: created.ID, Name: "kf2", Type: model.ConnectionTypeKafka,
		Config: model.MustConfigJSON(model.KafkaConfig{
			BootstrapServers: []string{"h:9092"}, SecurityProtocol: "SASL_PLAINTEXT",
			SASL: &model.SASLConfig{Enabled: true, Mechanism: "PLAIN", Username: "u", Password: ""},
		}),
	}); err != nil {
		t.Fatalf("update kafka: %v", err)
	}
	got, err = svc.GetConnection(ctx, created.ID)
	if err != nil {
		t.Fatalf("get kafka: %v", err)
	}
	kfCfg, err := got.KafkaConfig()
	if err != nil {
		t.Fatalf("decode kafka: %v", err)
	}
	if kfCfg.SASL == nil || kfCfg.SASL.Password != "secret" {
		t.Fatalf("kafka SASL 留空密码必须回填为原密码,got %+v", kfCfg.SASL)
	}
}

// TestCreateConnectionDoesNotRestoreSecrets 红线:新建空密码就是空密码,
// CreateConnection 不做任何回填。
func TestCreateConnectionDoesNotRestoreSecrets(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateConnection(context.Background(), &model.Connection{
		Name: "my", Type: model.ConnectionTypeMySQL,
		Config: model.MustConfigJSON(model.MysqlConfig{Host: "h", Port: 3306, Username: "u", Password: "", Database: "d"}),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.GetConnection(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	cfg, err := got.MysqlConfig()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Password != "" {
		t.Fatalf("新建空密码必须保持为空,got %q", cfg.Password)
	}
}
