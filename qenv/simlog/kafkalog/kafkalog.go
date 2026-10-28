// Package kafkalog is qenv/simlog's seams on Kafka, with franz-go: a
// Writer and a Reader over kgo clients, so code written against
// simlog.Writer and simlog.Reader, checked on the simulated log, runs on
// a Kafka cluster unchanged (kfake's in-process cluster in tests, any
// cluster otherwise).
//
// The Writer is kgo's producer, idempotent unless asked not to be; the
// Reader is a member of a consumer group with automatic commits off: the
// application commits after processing, and a member of an older
// generation is refused. A commit is reported when the coordinator
// accepted it (Reader.OnCommit): a real client cannot see one that landed
// with its response lost.
//
// kafkalog is a module of its own (it needs franz-go, which the core does
// not): go get github.com/chucklehead-dev/gotogether/qenv/simlog/kafkalog.
package kafkalog

import (
	"context"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv/simlog"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// Writer is a simlog.Writer over a kgo producer. Records are keyed, so
// kgo's default partitioner sends a key to one partition.
type Writer struct {
	cl    *kgo.Client
	topic string
}

// NewWriter connects a producer of topic, with any further kgo options
// (TLS, SASL). With idempotent false, the producer's idempotence is off,
// with five requests in flight: its retries can then reorder and
// duplicate.
func NewWriter(brokers []string, topic string, idempotent bool, opts ...kgo.Opt) (*Writer, error) {
	opts = append([]kgo.Opt{kgo.SeedBrokers(brokers...), kgo.DefaultProduceTopic(topic)}, opts...)
	if !idempotent {
		opts = append(opts, kgo.DisableIdempotentWrite(), kgo.MaxProduceRequestsInflightPerBroker(5))
	}
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return &Writer{cl: cl, topic: topic}, nil
}

// Produce implements simlog.Writer.
func (w *Writer) Produce(ctx context.Context, key string, value []byte, done func(error)) {
	r := &kgo.Record{Topic: w.topic, Key: []byte(key), Value: value}
	w.cl.Produce(ctx, r, func(_ *kgo.Record, err error) { done(err) })
}

// Flush implements simlog.Writer.
func (w *Writer) Flush(ctx context.Context) { _ = w.cl.Flush(ctx) }

// Close closes the producer.
func (w *Writer) Close() { w.cl.Close() }

// Reader is a simlog.Reader over a kgo consumer in a group, with
// automatic commits off: the application commits after processing.
type Reader struct {
	// OnCommit, if set, is called when a commit was accepted, before
	// Commit returns.
	OnCommit func(ctx context.Context, partition, next int)

	cl    *kgo.Client
	topic string
}

// NewReader joins group as a consumer of topic, reading a partition with
// no committed offset from its start, with any further kgo options.
func NewReader(brokers []string, topic, group string, opts ...kgo.Opt) (*Reader, error) {
	cl, err := kgo.NewClient(append([]kgo.Opt{kgo.SeedBrokers(brokers...), kgo.ConsumerGroup(group), kgo.ConsumeTopics(topic),
		kgo.DisableAutoCommit(), kgo.FetchMaxWait(100 * time.Millisecond), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart())}, opts...)...)
	if err != nil {
		return nil, err
	}
	return &Reader{cl: cl, topic: topic}, nil
}

// Poll implements simlog.Reader: the records fetched within 200 ms, at
// most 10.
func (r *Reader) Poll(ctx context.Context) ([]simlog.Record, error) {
	pctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	fs := r.cl.PollRecords(pctx, 10)
	if fs.IsClientClosed() {
		return nil, kgo.ErrClientClosed
	}
	var out []simlog.Record
	fs.EachRecord(func(rec *kgo.Record) {
		out = append(out, simlog.Record{Partition: int(rec.Partition), Offset: int(rec.Offset), Key: string(rec.Key), Value: rec.Value})
	})
	return out, nil
}

// Commit implements simlog.Reader, calling OnCommit when the coordinator
// accepted it. A member of an older generation is refused (Kafka's
// ILLEGAL_GENERATION, REBALANCE_IN_PROGRESS).
func (r *Reader) Commit(ctx context.Context, partition, next int) error {
	var cerr error
	offsets := map[string]map[int32]kgo.EpochOffset{r.topic: {int32(partition): {Epoch: -1, Offset: int64(next)}}}
	r.cl.CommitOffsetsSync(ctx, offsets, func(_ *kgo.Client, _ *kmsg.OffsetCommitRequest, resp *kmsg.OffsetCommitResponse, err error) {
		if err != nil {
			cerr = err
			return
		}
		for _, t := range resp.Topics {
			for _, p := range t.Partitions {
				if e := kerr.ErrorForCode(p.ErrorCode); e != nil {
					cerr = e
				}
			}
		}
	})
	if cerr != nil {
		return cerr
	}
	if r.OnCommit != nil {
		r.OnCommit(ctx, partition, next)
	}
	return nil
}

// Close leaves the group.
func (r *Reader) Close() { r.cl.Close() }
