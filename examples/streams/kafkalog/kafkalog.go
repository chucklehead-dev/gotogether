// Package kafkalog runs the pipeline on Kafka with franz-go: Writer and
// Reader over kgo clients, and Run, a whole deployment against a broker
// (kfake's in-process cluster in the tests and the demo, any cluster
// otherwise).
package kafkalog

import (
	"context"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv/simlog"
	"github.com/chucklehead-dev/gotogether/qobs"
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

// NewWriter connects a producer. With idempotent false, the producer's
// idempotence is off (pipeline.NonIdempotentProducer): its retries can
// then reorder and duplicate.
func NewWriter(brokers []string, topic string, idempotent bool) (*Writer, error) {
	opts := []kgo.Opt{kgo.SeedBrokers(brokers...), kgo.DefaultProduceTopic(topic)}
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
// automatic commits off: the pipeline commits after applying.
type Reader struct {
	cl    *kgo.Client
	topic string
}

// NewReader joins group as a consumer of topic.
func NewReader(brokers []string, topic, group string) (*Reader, error) {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumerGroup(group), kgo.ConsumeTopics(topic),
		kgo.DisableAutoCommit(), kgo.FetchMaxWait(100*time.Millisecond), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		return nil, err
	}
	return &Reader{cl: cl, topic: topic}, nil
}

// Poll implements simlog.Reader: the records fetched within 200 ms.
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

// Commit implements simlog.Reader, recording the commit when the
// coordinator accepted it. A member of an older generation is refused
// (Kafka's ILLEGAL_GENERATION, REBALANCE_IN_PROGRESS).
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
	qobs.Record(ctx, "commit", nil, "partition", partition, "offset", next)
	return nil
}

// Close leaves the group.
func (r *Reader) Close() { r.cl.Close() }
