package kafka

import (
	"context"
	"strings"
	"testing"
)

func TestAlterTopicPartitionsSetsFinalCount(t *testing.T) {
	c := newCluster(t, 1, "user-log")
	cl := newKafkaClient(t, c)

	ctx := context.Background()
	if err := cl.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := cl.AlterTopicPartitions(ctx, "user-log", 3); err != nil {
		t.Fatalf("AlterTopicPartitions: %v", err)
	}
	topics, err := cl.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}
	for _, tp := range topics {
		if tp.Name != "user-log" {
			continue
		}
		if len(tp.Partitions) != 3 {
			t.Fatalf("topic %s: expected 3 partitions after expand, got %d", tp.Name, len(tp.Partitions))
		}
		return
	}
	t.Fatalf("topic user-log not found after expand")
}

// TestAlterTopicPartitionsInvalidatesPartsCache 扩分区后 partsTTL 缓存必须
// 失效:预热缓存(1 分区)→ 扩到 3 → TTL 内再查必须看到 3 个分区(命中
// 旧缓存则只会看到 1)。
func TestAlterTopicPartitionsInvalidatesPartsCache(t *testing.T) {
	c := newCluster(t, 1, "user-log")
	cl := newKafkaClient(t, c)

	ctx := context.Background()
	if err := cl.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	parts, err := cl.partitionsOf(ctx, "user-log")
	if err != nil {
		t.Fatalf("partitionsOf (warm-up): %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 partition before expand, got %d", len(parts))
	}
	if err := cl.AlterTopicPartitions(ctx, "user-log", 3); err != nil {
		t.Fatalf("AlterTopicPartitions: %v", err)
	}
	parts, err = cl.partitionsOf(ctx, "user-log")
	if err != nil {
		t.Fatalf("partitionsOf (after expand): %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("parts cache must be invalidated by alter: expected 3 partitions, got %d", len(parts))
	}
}

// TestDeleteTopicInvalidatesPartsCache 删除 topic 后缓存必须失效:注入拉取
// 计数,预热 1 次 → 删除 → 再查必须真正重拉(第 2 次拉取),而不是命中缓存。
// 不对删除后的元数据行为(各实现异步程度不同)做断言,只锁定「失效」本身。
func TestDeleteTopicInvalidatesPartsCache(t *testing.T) {
	c := newCluster(t, 2, "user-log")
	cl := newKafkaClient(t, c)

	ctx := context.Background()
	if err := cl.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	orig := cl.listPartitions
	calls := 0
	cl.listPartitions = func(ctx context.Context, topic string) ([]int32, error) {
		calls++
		return orig(ctx, topic)
	}
	if _, err := cl.partitionsOf(ctx, "user-log"); err != nil {
		t.Fatalf("partitionsOf (warm-up): %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 fetch on warm-up, got %d", calls)
	}
	if err := cl.DeleteTopic(ctx, "user-log"); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	if _, err := cl.partitionsOf(ctx, "user-log"); err != nil && calls == 1 {
		t.Fatal("partitionsOf after delete must refetch (cache invalidated), not hit the cache")
	}
	if calls != 2 {
		t.Fatalf("expected a refetch after delete (calls=2), got %d", calls)
	}
}

func TestAlterTopicPartitionsRejectsShrink(t *testing.T) {
	c := newCluster(t, 3, "user-log")
	cl := newKafkaClient(t, c)

	ctx := context.Background()
	if err := cl.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	err := cl.AlterTopicPartitions(ctx, "user-log", 1)
	if err == nil {
		t.Fatalf("expected shrink to fail, got nil")
	}
	if !strings.Contains(err.Error(), "扩容") {
		t.Fatalf("expected a shrink-specific error, got %v", err)
	}
}

func TestAlterTopicPartitionsRejectsUnchangedCount(t *testing.T) {
	c := newCluster(t, 2, "user-log")
	cl := newKafkaClient(t, c)

	ctx := context.Background()
	if err := cl.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := cl.AlterTopicPartitions(ctx, "user-log", 2); err == nil {
		t.Fatalf("expected unchanged count to fail, got nil")
	}
}

func TestAlterTopicPartitionsUnknownTopicFails(t *testing.T) {
	c := newCluster(t, 1, "user-log")
	cl := newKafkaClient(t, c)

	ctx := context.Background()
	if err := cl.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := cl.AlterTopicPartitions(ctx, "missing", 3); err == nil {
		t.Fatalf("expected unknown topic to fail, got nil")
	}
}
