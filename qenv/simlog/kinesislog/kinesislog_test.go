package kinesislog_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv/simlog"
	"github.com/chucklehead-dev/gotogether/qenv/simlog/kinesislog"
	"github.com/chucklehead-dev/gotogether/qenv/simlog/kinesislog/kinesisfake"
)

// Writer and Reader are the simulated log's seams.
var (
	_ simlog.Writer = (*kinesislog.Writer)(nil)
	_ simlog.Reader = (*kinesislog.Reader)(nil)
)

func produce(t *testing.T, w simlog.Writer, keys []string, values ...string) {
	t.Helper()
	for _, k := range keys {
		for _, v := range values {
			w.Produce(context.Background(), k, []byte(k+v), func(err error) {
				if err != nil {
					t.Errorf("produce %s%s: %v", k, v, err)
				}
			})
		}
	}
	w.Flush(context.Background())
}

func poll(t *testing.T, r simlog.Reader, n int) []simlog.Record {
	t.Helper()
	var out []simlog.Record
	for i := 0; i < 50 && len(out) < n; i++ {
		recs, err := r.Poll(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, recs...)
	}
	if len(out) < n {
		t.Fatalf("read %d records, want %d", len(out), n)
	}
	return out
}

// Records of a key land in one shard (by the MD5 of the key, as in
// Kinesis), in order, read from the shard's start with dense offsets; a
// commit is checkpointed and reported, and a Reader on the same
// checkpoints resumes after it.
func TestWriteReadCommit(t *testing.T) {
	srv := kinesisfake.New("events", 2)
	defer srv.Close()
	c := kinesislog.Client(srv.URL)
	w := &kinesislog.Writer{C: c, Stream: "events"}
	keys := []string{"a", "b", "c", "d"}
	produce(t, w, keys, "1", "2", "3")
	cps := &kinesislog.Checkpoints{}
	var commits []string
	r := &kinesislog.Reader{C: c, Stream: "events", Checkpoints: cps,
		OnCommit: func(_ context.Context, p, next int) { commits = append(commits, fmt.Sprint(p, ":", next)) }}
	recs := poll(t, r, 12)
	byKey, part, next := map[string]string{}, map[string]int{}, map[int]int{}
	for _, rec := range recs {
		if p, ok := part[rec.Key]; ok && p != rec.Partition {
			t.Errorf("key %s in shards %d and %d", rec.Key, p, rec.Partition)
		}
		part[rec.Key] = rec.Partition
		if rec.Offset != next[rec.Partition] {
			t.Errorf("shard %d: offset %d, want %d", rec.Partition, rec.Offset, next[rec.Partition])
		}
		next[rec.Partition]++
		byKey[rec.Key] += string(rec.Value) + " "
	}
	for _, k := range keys {
		if want := k + "1 " + k + "2 " + k + "3 "; byKey[k] != want {
			t.Errorf("key %s read %q, want %q", k, byKey[k], want)
		}
	}
	if len(next) != 2 {
		t.Errorf("records in %d shards, want both", len(next))
	}
	for p, n := range next {
		if err := r.Commit(context.Background(), p, n); err != nil {
			t.Fatal(err)
		}
	}
	if len(commits) != 2 {
		t.Errorf("OnCommit saw %v, want one per shard", commits)
	}
	if err := r.Commit(context.Background(), 0, next[0]+5); err == nil {
		t.Errorf("a commit past what was read was taken")
	}

	produce(t, w, []string{"a"}, "4")
	r2 := &kinesislog.Reader{C: c, Stream: "events", Checkpoints: cps}
	if got := poll(t, r2, 1); len(got) != 1 || string(got[0].Value) != "a4" || got[0].Offset != next[got[0].Partition] {
		t.Errorf("a Reader on the same checkpoints read %+v, want a4 alone, after the commit", got)
	}
}

// The fake refuses another stream, as Kinesis does.
func TestUnknownStream(t *testing.T) {
	srv := kinesisfake.New("events", 1)
	defer srv.Close()
	var got error
	(&kinesislog.Writer{C: kinesislog.Client(srv.URL), Stream: "other"}).Produce(context.Background(), "k", []byte("v"), func(err error) { got = err })
	if got == nil {
		t.Errorf("a put to an unknown stream succeeded")
	}
}
