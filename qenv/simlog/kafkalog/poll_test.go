package kafkalog_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv/simlog"
	"github.com/chucklehead-dev/gotogether/qenv/simlog/kafkalog"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func pollFixture(t *testing.T, records bool, faults ...kfake.Fault) (*kafkalog.Reader, *kfake.FaultHandle) {
	t.Helper()
	c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(2, "events"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	if records {
		p, err := kgo.NewClient(kgo.SeedBrokers(c.ListenAddrs()...), kgo.RecordPartitioner(kgo.ManualPartitioner()))
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if err := p.ProduceSync(ctx,
			&kgo.Record{Topic: "events", Partition: 0, Value: []byte("zero")},
			&kgo.Record{Topic: "events", Partition: 1, Value: []byte("one")},
		).FirstErr(); err != nil {
			t.Fatal(err)
		}
	}
	var fault *kfake.FaultHandle
	if len(faults) > 0 {
		// Install faults before starting the reader: background fetches must
		// not buffer healthy records before the controlled response exists.
		fault = c.Fault(faults...)
		t.Cleanup(fault.Remove)
	}
	r, err := kafkalog.NewReader(c.ListenAddrs(), "events", "poll-errors", kgo.DisableFetchSessions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r, fault
}

func pollError(t *testing.T, r simlog.Reader, wanted error) ([]simlog.Record, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		records, err := r.Poll(ctx)
		if err != nil {
			if !errors.Is(err, wanted) {
				t.Fatalf("Poll error %v, want %v", err, wanted)
			}
			return records, err
		}
	}
}

// Kafka may split native responses into different Poll batches. Healthy
// records and broker errors must both remain observable without assuming how
// the client grouped those batches.
func TestPollRetainsRecordsAndPartitionError(t *testing.T) {
	r, fault := pollFixture(t, true, kfake.Fault{Keys: []kmsg.Key{kmsg.Fetch}, Topic: "events", Partitions: []int32{0}, Err: kerr.UnsupportedCompressionType, Count: -1})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var records []simlog.Record
	var fetchError error
	for len(records) == 0 || fetchError == nil {
		batch, err := r.Poll(ctx)
		records = append(records, batch...)
		if err != nil {
			if !errors.Is(err, kerr.UnsupportedCompressionType) {
				t.Fatalf("Poll: %v", err)
			}
			fetchError = err
		}
	}
	if len(records) != 1 || records[0].Partition != 1 || records[0].Offset != 0 || string(records[0].Value) != "one" {
		t.Fatalf("healthy records lost: %+v (%v)", records, fetchError)
	}
	if !strings.Contains(fetchError.Error(), `topic "events" partition 0`) || fault.Hits() == 0 {
		t.Fatalf("fetch provenance missing: %v, broker hits %d", fetchError, fault.Hits())
	}
}

func TestPollRetainsEveryPartitionError(t *testing.T) {
	r, _ := pollFixture(t, true, kfake.Fault{Keys: []kmsg.Key{kmsg.Fetch}, Topic: "events", Err: kerr.UnsupportedCompressionType, Count: -1})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var errs []error
	for {
		records, err := r.Poll(ctx)
		if len(records) != 0 {
			t.Fatalf("records returned from rejected partitions: %+v", records)
		}
		if err != nil {
			if !errors.Is(err, kerr.UnsupportedCompressionType) {
				t.Fatalf("Poll: %v", err)
			}
			errs = append(errs, err)
		}
		joined := errors.Join(errs...)
		if joined != nil && strings.Contains(joined.Error(), "partition 0") && strings.Contains(joined.Error(), "partition 1") {
			break
		}
	}
}

func TestPollSurfacesAuthorizationError(t *testing.T) {
	r, _ := pollFixture(t, true, kfake.Fault{Keys: []kmsg.Key{kmsg.Fetch}, Topic: "events", Err: kerr.TopicAuthorizationFailed, Count: -1})
	_, err := pollError(t, r, kerr.TopicAuthorizationFailed)
	if !strings.Contains(err.Error(), `topic "events"`) {
		t.Fatalf("topic provenance missing: %v", err)
	}
}

func TestPollParentContextAndIdleTimeout(t *testing.T) {
	r, _ := pollFixture(t, false)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if records, err := r.Poll(canceled); len(records) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled parent: records %+v, error %v", records, err)
	}
	expired, stop := context.WithDeadline(t.Context(), time.Time{})
	defer stop()
	if records, err := r.Poll(expired); len(records) != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired parent: records %+v, error %v", records, err)
	}
	// A live parent's earlier deadline must not be confused with the adapter's
	// normal 200 ms idle deadline.
	short, stopShort := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stopShort()
	if records, err := r.Poll(short); len(records) != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("parent deadline while polling: records %+v, error %v", records, err)
	}
	live, stopLive := context.WithTimeout(t.Context(), time.Second)
	defer stopLive()
	if records, err := r.Poll(live); len(records) != 0 || err != nil || live.Err() != nil {
		t.Fatalf("idle poll: records %+v, error %v, parent %v", records, err, live.Err())
	}
}

func TestPollClosedClient(t *testing.T) {
	r, _ := pollFixture(t, false)
	r.Close()
	if records, err := r.Poll(t.Context()); len(records) != 0 || !errors.Is(err, kgo.ErrClientClosed) {
		t.Fatalf("closed reader: records %+v, error %v", records, err)
	}
}
