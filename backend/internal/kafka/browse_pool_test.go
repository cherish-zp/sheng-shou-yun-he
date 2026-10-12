package kafka

import (
	"context"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"

	"sheng-shou-yun-he/backend/internal/model"
)

// installBrowseCounter wraps the browse consumer factory with a counting
// version around the real one and restores it at cleanup. The returned
// pointer counts how many browse consumers were built.
func installBrowseCounter(t *testing.T, cl *Client) *int {
	t.Helper()
	n := 0
	real := cl.newBrowseConsumer
	cl.newBrowseConsumer = func(topic string, offsets map[int32]kgo.Offset) (*kgo.Client, error) {
		n++
		return real(topic, offsets)
	}
	t.Cleanup(func() { cl.newBrowseConsumer = real })
	return &n
}

// browsePoolLen returns how many pools the client currently caches.
func browsePoolLen(cl *Client) int {
	cl.browseMu.Lock()
	defer cl.browseMu.Unlock()
	return len(cl.browsePools)
}

// TestBrowseConsumerPoolReusesSequentialBrowses proves that two sequential
// message browses over the same connection reuse one consumer instance (one
// build instead of a full TCP handshake per browse) and that the consumer
// state is reset between browses: the second browse must honour its own
// offsets, not continue from the first.
func TestBrowseConsumerPoolReusesSequentialBrowses(t *testing.T) {
	c := newCluster(t, 1, "t1")
	seedMessages(t, c, "t1", 0, 10, 0)
	cl := newKafkaClient(t, c)
	created := installBrowseCounter(t, cl)

	ctx := context.Background()
	first, err := cl.ConsumeMessages(ctx, "t1", 0, model.OffsetEarliest, 5)
	if err != nil {
		t.Fatalf("first browse: %v", err)
	}
	if len(first) != 5 || first[0].Offset != 0 {
		t.Fatalf("first browse: expected offsets 0..4, got %d records starting at %d", len(first), first[0].Offset)
	}

	second, err := cl.ConsumeMessages(ctx, "t1", 0, 7, 3)
	if err != nil {
		t.Fatalf("second browse: %v", err)
	}
	if len(second) != 3 || second[0].Offset != 7 {
		t.Fatalf("second browse: expected offsets 7..9, got %d records starting at %d", len(second), second[0].Offset)
	}

	if *created != 1 {
		t.Fatalf("expected the pool to build 1 consumer for sequential browses, got %d", *created)
	}
}

// TestBrowseConsumerPoolResetsAcrossTopics browses two different topics in
// alternation and proves the pool still reuses one instance without leaking
// the previous assignment into the next browse.
func TestBrowseConsumerPoolResetsAcrossTopics(t *testing.T) {
	c := newCluster(t, 1, "a", "b")
	seedMessages(t, c, "a", 0, 3, 0)
	seedMessages(t, c, "b", 0, 4, 0)
	cl := newKafkaClient(t, c)
	created := installBrowseCounter(t, cl)

	ctx := context.Background()
	// Round 1: a (3 records) then b (4 records).
	msgs, err := cl.ConsumeMessages(ctx, "a", 0, model.OffsetEarliest, 10)
	if err != nil {
		t.Fatalf("browse a: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("browse a: expected 3 records, got %d", len(msgs))
	}
	msgs, err = cl.ConsumeMessages(ctx, "b", 0, model.OffsetEarliest, 10)
	if err != nil {
		t.Fatalf("browse b: %v", err)
	}
	if len(msgs) != 4 || msgs[0].Value != "v-0" {
		t.Fatalf("browse b: expected 4 fresh records, got %d records (first %q)", len(msgs), msgs[0].Value)
	}
	// Round 2: back to a at offset 1 — must not see topic b's records nor
	// repeat topic a's offset 0.
	msgs, err = cl.ConsumeMessages(ctx, "a", 0, 1, 10)
	if err != nil {
		t.Fatalf("browse a again: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Offset != 1 {
		t.Fatalf("browse a again: expected offsets 1..2, got %d records starting at %d", len(msgs), msgs[0].Offset)
	}

	if *created != 1 {
		t.Fatalf("expected 1 consumer across alternating topic browses, got %d", *created)
	}
}

// TestBrowseConsumerPoolInvalidatedByClose covers the invalidation path:
// after the pool is cleared (what Client.Close does when the service layer
// disconnects / deletes / re-configures a connection), the next browse builds
// a fresh consumer and works.
func TestBrowseConsumerPoolInvalidatedByClose(t *testing.T) {
	c := newCluster(t, 1, "t1")
	seedMessages(t, c, "t1", 0, 5, 0)
	cl := newKafkaClient(t, c)
	created := installBrowseCounter(t, cl)
	ctx := context.Background()

	msgs, err := cl.ConsumeMessages(ctx, "t1", 0, model.OffsetEarliest, 2)
	if err != nil {
		t.Fatalf("first browse: %v", err)
	}
	if len(msgs) != 2 || *created != 1 {
		t.Fatalf("first browse: got %d records, %d builds", len(msgs), *created)
	}

	cl.closeBrowsePools()
	if n := browsePoolLen(cl); n != 0 {
		t.Fatalf("expected browse pools to be cleared, %d remain", n)
	}

	msgs, err = cl.ConsumeMessages(ctx, "t1", 0, model.OffsetEarliest, 2)
	if err != nil {
		t.Fatalf("browse after invalidation: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("browse after invalidation: expected 2 records, got %d", len(msgs))
	}
	if *created != 2 {
		t.Fatalf("expected a fresh consumer after pool invalidation, got %d builds", *created)
	}
}

// TestClientCloseClearsBrowsePools pins that Client.Close — the single exit
// the service layer uses for disconnect / delete / update of a connection —
// closes every cached browse consumer and empties the pool map.
func TestClientCloseClearsBrowsePools(t *testing.T) {
	c := newCluster(t, 1, "t1")
	seedMessages(t, c, "t1", 0, 5, 0)
	cl := newKafkaClient(t, c)

	if _, err := cl.ConsumeMessages(context.Background(), "t1", 0, model.OffsetEarliest, 1); err != nil {
		t.Fatalf("browse: %v", err)
	}
	if n := browsePoolLen(cl); n != 1 {
		t.Fatalf("expected 1 cached browse pool, got %d", n)
	}
	cl.browseMu.Lock()
	var pool *browsePool
	for _, p := range cl.browsePools {
		pool = p
	}
	cl.browseMu.Unlock()

	if err := cl.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if n := browsePoolLen(cl); n != 0 {
		t.Fatalf("expected browse pools to be cleared by Close, %d remain", n)
	}
	pool.mu.Lock()
	idleDrained := pool.idle == nil && pool.closed
	pool.mu.Unlock()
	if !idleDrained {
		t.Fatal("expected the pool to be closed and its idle consumer released")
	}
}
