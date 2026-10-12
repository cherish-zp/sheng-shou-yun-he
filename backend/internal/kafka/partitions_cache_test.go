package kafka

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestPartitionsOfCachesWithinTTL proves partitionsOf serves repeat calls
// from the TTL cache (one pull), refreshes once the injected clock passes the
// expiry, and returns the cached partition list untouched.
func TestPartitionsOfCachesWithinTTL(t *testing.T) {
	c := newCluster(t, 2, "t1")
	cl := newKafkaClient(t, c)

	calls := 0
	cl.listPartitions = func(ctx context.Context, topic string) ([]int32, error) {
		calls++
		return []int32{0, 1}, nil
	}
	now := time.Unix(1700000000, 0)
	cl.now = func() time.Time { return now }

	ctx := context.Background()
	parts, err := cl.partitionsOf(ctx, "t1")
	if err != nil {
		t.Fatalf("partitionsOf: %v", err)
	}
	if len(parts) != 2 || parts[0] != 0 || parts[1] != 1 {
		t.Fatalf("expected partitions [0 1], got %v", parts)
	}
	if calls != 1 {
		t.Fatalf("expected 1 pull, got %d", calls)
	}

	// TTL 内二次调用:直接命中缓存,不触发第二次拉取。
	now = now.Add(59 * time.Second)
	if _, err := cl.partitionsOf(ctx, "t1"); err != nil {
		t.Fatalf("partitionsOf within TTL: %v", err)
	}
	if calls != 1 {
		t.Fatalf("second call within TTL must be served from cache, got %d pulls", calls)
	}

	// 注入时钟推进到过期后:重新拉取并刷新缓存。
	now = now.Add(2 * time.Second)
	if _, err := cl.partitionsOf(ctx, "t1"); err != nil {
		t.Fatalf("partitionsOf after TTL: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expired cache must be refetched, got %d pulls", calls)
	}
	// 刷新后再次进入 TTL 窗口。
	if _, err := cl.partitionsOf(ctx, "t1"); err != nil {
		t.Fatalf("partitionsOf after refresh: %v", err)
	}
	if calls != 2 {
		t.Fatalf("call after refresh must be served from cache, got %d pulls", calls)
	}
}

// TestPartitionsOfCachesPerTopic proves the cache is keyed per topic: a
// second topic is pulled independently.
func TestPartitionsOfCachesPerTopic(t *testing.T) {
	c := newCluster(t, 1, "a", "b")
	cl := newKafkaClient(t, c)

	pulls := map[string]int{}
	cl.listPartitions = func(ctx context.Context, topic string) ([]int32, error) {
		pulls[topic]++
		return []int32{0}, nil
	}

	ctx := context.Background()
	if _, err := cl.partitionsOf(ctx, "a"); err != nil {
		t.Fatalf("partitionsOf a: %v", err)
	}
	if _, err := cl.partitionsOf(ctx, "b"); err != nil {
		t.Fatalf("partitionsOf b: %v", err)
	}
	if _, err := cl.partitionsOf(ctx, "a"); err != nil {
		t.Fatalf("partitionsOf a again: %v", err)
	}
	if pulls["a"] != 1 || pulls["b"] != 1 {
		t.Fatalf("expected one pull per topic, got %v", pulls)
	}
}

// TestPartitionsOfDoesNotCacheFailures proves a failed pull is not cached:
// the next call retries, and once it succeeds the result is cached.
func TestPartitionsOfDoesNotCacheFailures(t *testing.T) {
	c := newCluster(t, 1, "t1")
	cl := newKafkaClient(t, c)

	calls := 0
	boom := errors.New("boom")
	cl.listPartitions = func(ctx context.Context, topic string) ([]int32, error) {
		calls++
		if calls == 1 {
			return nil, boom
		}
		return []int32{0}, nil
	}

	ctx := context.Background()
	if _, err := cl.partitionsOf(ctx, "t1"); !errors.Is(err, boom) {
		t.Fatalf("expected the pull error, got %v", err)
	}
	// 失败不缓存:重试成功后结果进入缓存。
	parts, err := cl.partitionsOf(ctx, "t1")
	if err != nil {
		t.Fatalf("partitionsOf retry: %v", err)
	}
	if len(parts) != 1 || parts[0] != 0 {
		t.Fatalf("expected partitions [0], got %v", parts)
	}
	// 成功结果已缓存。
	if _, err := cl.partitionsOf(ctx, "t1"); err != nil {
		t.Fatalf("partitionsOf cached: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 pulls (failure + success), got %d", calls)
	}
}
