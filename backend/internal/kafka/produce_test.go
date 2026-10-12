package kafka

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"sheng-shou-yun-he/backend/internal/model"
)

// TestProduceMessagesBatch publishes several records in one call and verifies
// the per-message results plus what actually landed in the log.
func TestProduceMessagesBatch(t *testing.T) {
	c := newCluster(t, 2, "batch")
	cl := newKafkaClient(t, c)
	ctx := context.Background()

	msgs := []model.BatchProduceMessage{
		{Key: "k-0", Value: "v-0"},
		{Key: "k-1", Value: "v-1"},
		{Key: "k-2", Value: "v-2"},
	}
	results, err := cl.ProduceMessages(ctx, "batch", 1, msgs)
	if err != nil {
		t.Fatalf("ProduceMessages: %v", err)
	}
	if len(results) != len(msgs) {
		t.Fatalf("expected %d results, got %d: %+v", len(msgs), len(results), results)
	}
	for i, r := range results {
		if r.Index != i {
			t.Fatalf("result %d: expected index %d, got %+v", i, i, r)
		}
		if r.Error != "" {
			t.Fatalf("result %d: unexpected error %q", i, r.Error)
		}
		if r.Partition != 1 {
			t.Fatalf("result %d: expected partition 1, got %d", i, r.Partition)
		}
		if r.Offset != int64(i) {
			t.Fatalf("result %d: expected offset %d, got %d", i, i, r.Offset)
		}
	}

	// Every record must be visible on the target partition, in order.
	got, err := cl.ConsumeMessages(ctx, "batch", 1, model.OffsetEarliest, 10)
	if err != nil {
		t.Fatalf("ConsumeMessages: %v", err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("expected %d consumed records, got %d: %+v", len(msgs), len(got), got)
	}
	for i, m := range got {
		if m.Key != msgs[i].Key || m.Value != msgs[i].Value {
			t.Fatalf("record %d: expected %q/%q, got %q/%q", i, msgs[i].Key, msgs[i].Value, m.Key, m.Value)
		}
		if m.Partition != 1 {
			t.Fatalf("record %d: expected partition 1, got %d", i, m.Partition)
		}
	}
	// The other partition must stay untouched.
	other, err := cl.ConsumeMessages(ctx, "batch", 0, model.OffsetEarliest, 10)
	if err != nil {
		t.Fatalf("ConsumeMessages partition 0: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("partition 0 must be empty, got %+v", other)
	}
}

// TestProduceMessagesLargeBatchBackfillsAllRecords verifies that a larger
// batch lands completely and that every result gets its Index plus the
// produced partition/offset backfilled in input order.
func TestProduceMessagesLargeBatchBackfillsAllRecords(t *testing.T) {
	c := newCluster(t, 2, "big")
	cl := newKafkaClient(t, c)
	ctx := context.Background()

	const n = 20
	msgs := make([]model.BatchProduceMessage, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, model.BatchProduceMessage{Key: fmt.Sprintf("k-%d", i), Value: fmt.Sprintf("v-%d", i)})
	}
	results, err := cl.ProduceMessages(ctx, "big", 1, msgs)
	if err != nil {
		t.Fatalf("ProduceMessages: %v", err)
	}
	if len(results) != n {
		t.Fatalf("expected %d results, got %d", n, len(results))
	}
	seenOffsets := map[int64]bool{}
	for i, r := range results {
		if r.Index != i {
			t.Fatalf("result %d: expected index %d, got %+v", i, i, r)
		}
		if r.Error != "" {
			t.Fatalf("result %d: unexpected error %q", i, r.Error)
		}
		if r.Partition != 1 {
			t.Fatalf("result %d: expected partition 1, got %d", i, r.Partition)
		}
		if r.Offset < 0 || seenOffsets[r.Offset] {
			t.Fatalf("result %d: offset %d missing or duplicated", i, r.Offset)
		}
		seenOffsets[r.Offset] = true
	}
	// All n records must be visible on partition 1 with their original order
	// preserved (single-partition produce keeps input order).
	got, err := cl.ConsumeMessages(ctx, "big", 1, model.OffsetEarliest, n)
	if err != nil {
		t.Fatalf("ConsumeMessages: %v", err)
	}
	if len(got) != n {
		t.Fatalf("expected %d records on disk, got %d", n, len(got))
	}
	for i, m := range got {
		if m.Key != msgs[i].Key || m.Value != msgs[i].Value {
			t.Fatalf("record %d: expected %q/%q, got %q/%q", i, msgs[i].Key, msgs[i].Value, m.Key, m.Value)
		}
	}
}

// TestProduceMessagesPerRecordErrorKeepsCallSuccess pins the failure
// semantics: a record that cannot be produced carries its error in
// ProduceResult.Error (prefixed "produce record:") while the call itself
// still succeeds and returns one result per input message.
func TestProduceMessagesPerRecordErrorKeepsCallSuccess(t *testing.T) {
	c := newCluster(t, 2, "two")
	cl := newKafkaClient(t, c)
	ctx := context.Background()

	msgs := []model.BatchProduceMessage{
		{Key: "a", Value: "va"},
		{Key: "b", Value: "vb"},
	}
	// Partition 99 does not exist on a 2-partition topic: every record fails
	// immediately, but results must still be collected per message.
	results, err := cl.ProduceMessages(ctx, "two", 99, msgs)
	if err != nil {
		t.Fatalf("ProduceMessages must not fail wholesale on per-record errors: %v", err)
	}
	if len(results) != len(msgs) {
		t.Fatalf("expected %d results, got %d", len(msgs), len(results))
	}
	for i, r := range results {
		if r.Index != i {
			t.Fatalf("result %d: expected index %d, got %+v", i, i, r)
		}
		if r.Error == "" {
			t.Fatalf("result %d: expected a per-record error, got %+v", i, r)
		}
		if !strings.HasPrefix(r.Error, "produce record:") {
			t.Fatalf("result %d: error %q must keep the \"produce record:\" prefix", i, r.Error)
		}
	}
}

// TestProduceMessagesAutoPartition publishes with partition < 0 and expects
// every record to land somewhere without error.
func TestProduceMessagesAutoPartition(t *testing.T) {
	c := newCluster(t, 2, "auto")
	cl := newKafkaClient(t, c)
	ctx := context.Background()

	msgs := []model.BatchProduceMessage{
		{Key: "a", Value: "va"},
		{Key: "b", Value: "vb"},
	}
	results, err := cl.ProduceMessages(ctx, "auto", -1, msgs)
	if err != nil {
		t.Fatalf("ProduceMessages: %v", err)
	}
	for _, r := range results {
		if r.Error != "" {
			t.Fatalf("unexpected per-message error: %+v", r)
		}
	}
	got, err := cl.ConsumeMessages(ctx, "auto", -1, model.OffsetEarliest, 10)
	if err != nil {
		t.Fatalf("ConsumeMessages: %v", err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("expected %d records across partitions, got %d", len(msgs), len(got))
	}
}
