package kafkalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Native fetches may contain several partition errors and healthy records in
// the same batch. Test that shape directly, independent of network batching.
func TestPollResultPreservesMixedBatch(t *testing.T) {
	second := errors.New("another partition failed")
	batch := kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "events", Partitions: []kgo.FetchPartition{
		{Partition: 0, Err: kerr.TopicAuthorizationFailed},
		{Partition: 1, Records: []*kgo.Record{{Topic: "events", Partition: 1, Offset: 7, Key: []byte("k"), Value: []byte("healthy")}}},
		{Partition: 2, Err: second},
	}}}}}
	records, err := pollResult(t.Context(), t.Context(), batch)
	if len(records) != 1 || records[0].Partition != 1 || records[0].Offset != 7 || records[0].Key != "k" || string(records[0].Value) != "healthy" {
		t.Fatalf("healthy batch lost: %+v", records)
	}
	if !errors.Is(err, kerr.TopicAuthorizationFailed) || !errors.Is(err, second) || !strings.Contains(err.Error(), `topic "events" partition 0`) || !strings.Contains(err.Error(), `topic "events" partition 2`) {
		t.Fatalf("partition failures lost: %v", err)
	}
}

func TestPollResultIgnoresOnlyOwnIdleDeadline(t *testing.T) {
	expired, cancel := context.WithDeadline(t.Context(), time.Time{})
	defer cancel()
	if _, err := pollResult(t.Context(), expired, kgo.NewErrFetch(context.DeadlineExceeded)); err != nil {
		t.Fatalf("own idle deadline: %v", err)
	}
	wrapped := fmt.Errorf("group session failed: %w", context.DeadlineExceeded)
	partitionDeadline := kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "events", Partitions: []kgo.FetchPartition{{Partition: 4, Err: context.DeadlineExceeded}}}}}}
	for _, tc := range []struct {
		name   string
		parent context.Context
		batch  kgo.Fetches
	}{
		{"partition deadline", t.Context(), partitionDeadline},
		{"wrapped deadline", t.Context(), kgo.NewErrFetch(wrapped)},
		{"parent deadline", expired, kgo.NewErrFetch(context.DeadlineExceeded)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pollResult(tc.parent, expired, tc.batch); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("failure suppressed: %v", err)
			}
		})
	}
}

func TestPollResultCancellationRetainsReturnedRecords(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	batch := kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "events", Partitions: []kgo.FetchPartition{{Partition: 1, Records: []*kgo.Record{{Partition: 1, Value: []byte("healthy")}}}}}}}}
	records, err := pollResult(ctx, ctx, batch)
	if len(records) != 1 || string(records[0].Value) != "healthy" || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost native result: %+v, %v", records, err)
	}
}
