package kafkalog_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"

	"github.com/chucklehead-dev/gotogether/qenv/simlog"
	"github.com/chucklehead-dev/gotogether/qenv/simlog/kafkalog"
)

// cluster is an in-process Kafka cluster with topic "events" of two
// partitions.
func cluster(t *testing.T) []string {
	t.Helper()
	c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(2, "events"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c.ListenAddrs()
}

// produce writes each key's values through w and waits for them, failing
// on any error.
func produce(t *testing.T, w simlog.Writer, keys []string, values ...string) {
	t.Helper()
	var mu sync.Mutex
	var errs []error
	for _, k := range keys {
		for _, v := range values {
			w.Produce(context.Background(), k, []byte(k+v), func(err error) {
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs = append(errs, err)
				}
			})
		}
	}
	w.Flush(context.Background())
	if len(errs) > 0 {
		t.Fatalf("produce: %v", errs)
	}
}

// poll reads from r until it has n records or 10 s pass.
func poll(t *testing.T, r simlog.Reader, n int) []simlog.Record {
	t.Helper()
	var out []simlog.Record
	for deadline := time.Now().Add(10 * time.Second); len(out) < n && time.Now().Before(deadline); {
		recs, err := r.Poll(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, recs...)
	}
	if len(out) < n {
		t.Fatalf("read %d records in 10 s, want %d", len(out), n)
	}
	return out
}

// A key's records land in one partition in order and are read from its
// start with dense offsets; a commit is reported, and the group's next
// member resumes after it.
func TestWriteReadCommit(t *testing.T) {
	for _, idempotent := range []bool{true, false} {
		t.Run(fmt.Sprint("idempotent=", idempotent), func(t *testing.T) {
			brokers := cluster(t)
			topic, group := "events", "projection"
			w, err := kafkalog.NewWriter(brokers, topic, idempotent)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			keys := []string{"a", "b", "c"}
			produce(t, w, keys, "1", "2", "3")
			r, err := kafkalog.NewReader(brokers, topic, group)
			if err != nil {
				t.Fatal(err)
			}
			var commits []string
			r.OnCommit = func(_ context.Context, p, next int) { commits = append(commits, fmt.Sprint(p, ":", next)) }
			recs := poll(t, r, 9)
			byKey, part, next := map[string]string{}, map[string]int{}, map[int]int{}
			for _, rec := range recs {
				if p, ok := part[rec.Key]; ok && p != rec.Partition {
					t.Errorf("key %s in partitions %d and %d", rec.Key, p, rec.Partition)
				}
				part[rec.Key] = rec.Partition
				if rec.Offset != next[rec.Partition] {
					t.Errorf("partition %d: offset %d, want %d", rec.Partition, rec.Offset, next[rec.Partition])
				}
				next[rec.Partition]++
				byKey[rec.Key] += string(rec.Value) + " "
			}
			for _, k := range keys {
				if want := k + "1 " + k + "2 " + k + "3 "; byKey[k] != want {
					t.Errorf("key %s read %q, want %q", k, byKey[k], want)
				}
			}
			for p, n := range next {
				if err := r.Commit(context.Background(), p, n); err != nil {
					t.Fatal(err)
				}
			}
			if len(commits) != len(next) {
				t.Errorf("OnCommit saw %v, want a commit per partition read", commits)
			}
			r.Close()

			produce(t, w, []string{"a"}, "4")
			r2, err := kafkalog.NewReader(brokers, topic, group)
			if err != nil {
				t.Fatal(err)
			}
			defer r2.Close()
			if got := poll(t, r2, 1); len(got) != 1 || string(got[0].Value) != "a4" || got[0].Offset != next[got[0].Partition] {
				t.Errorf("the next member read %+v, want a4 alone, after the commit", got)
			}
		})
	}
}

// Writer and Reader are the simulated log's seams.
var (
	_ simlog.Writer = (*kafkalog.Writer)(nil)
	_ simlog.Reader = (*kafkalog.Reader)(nil)
)

// A value is the bytes produced.
func TestValuesAreBytes(t *testing.T) {
	brokers := cluster(t)
	w, err := kafkalog.NewWriter(brokers, "events", true)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	produce(t, w, []string{"k"}, "\x00\xff:x")
	r, err := kafkalog.NewReader(brokers, "events", "bytes")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if got := poll(t, r, 1); !strings.HasSuffix(string(got[0].Value), "\x00\xff:x") {
		t.Errorf("read %q", got[0].Value)
	}
}
