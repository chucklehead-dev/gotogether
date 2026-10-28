// Package kinesislog is qenv/simlog's seams on Kinesis, with
// aws-sdk-go-v2: a Writer over PutRecord and a Reader over a stream's
// shards with GetShardIterator and GetRecords, checkpointing to a
// Checkpoints store, so code written against simlog.Writer and
// simlog.Reader, checked on the simulated log, runs on Kinesis unchanged
// (kinesisfake's in-process endpoint in tests, AWS otherwise).
//
// Kinesis has no consumer groups: the KCL coordinates shard leases in
// DynamoDB. The Reader here owns every shard (one consumer); its
// checkpoints are what a restarted consumer resumes from, and the store a
// lease table would hold. A shard's partition number is its index in
// ListShards' order by id, and a record's offset its index in the shard.
// Kinesis has no idempotent producer either: a PutRecord retried after a
// lost response writes the record twice.
//
// kinesislog is a module of its own (it needs the AWS SDK, which the core
// does not): go get github.com/chucklehead-dev/gotogether/qenv/simlog/kinesislog.
package kinesislog

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/chucklehead-dev/gotogether/qenv/simlog"
)

// Client is a Kinesis client for endpoint (the fake's URL, or "" for
// AWS's), without credentials for a fake.
func Client(endpoint string) *kinesis.Client {
	o := kinesis.Options{Region: "us-east-1"}
	if endpoint != "" {
		o.BaseEndpoint = aws.String(endpoint)
		o.Credentials = aws.AnonymousCredentials{}
	}
	return kinesis.New(o)
}

// Writer is a simlog.Writer over PutRecord, one record at a time: a
// key's records land in the order produced (Kinesis has no idempotent
// producer; a retried PutRecord after a lost response writes twice).
type Writer struct {
	C      *kinesis.Client
	Stream string
}

// Produce implements simlog.Writer; done is called before it returns.
func (w *Writer) Produce(ctx context.Context, key string, value []byte, done func(error)) {
	_, err := w.C.PutRecord(ctx, &kinesis.PutRecordInput{StreamName: aws.String(w.Stream), PartitionKey: aws.String(key),
		Data: value})
	done(err)
}

// Flush implements simlog.Writer: Produce is synchronous.
func (w *Writer) Flush(context.Context) {}

// Checkpoints are a consumer's committed positions, per shard index: the
// next offset and the sequence number of the record before it. Readers
// that share one resume from each other's commits.
type Checkpoints struct {
	mu  sync.Mutex
	pos map[int]checkpoint
}

type checkpoint struct {
	next int
	seq  string
}

// Reader is a simlog.Reader over every shard of Stream. A shard's
// partition number is its index in ListShards' order by id.
type Reader struct {
	C           *kinesis.Client
	Stream      string
	Checkpoints *Checkpoints
	// OnCommit, if set, is called when a commit was checkpointed, before
	// Commit returns.
	OnCommit func(ctx context.Context, partition, next int)

	shards []string
	iters  map[int]string
	pos    map[int]int
	seqAt  map[int]map[int]string // shard -> offset -> sequence number, for Commit
}

func (r *Reader) open(ctx context.Context) error {
	out, err := r.C.ListShards(ctx, &kinesis.ListShardsInput{StreamName: aws.String(r.Stream)})
	if err != nil {
		return err
	}
	for _, s := range out.Shards {
		r.shards = append(r.shards, aws.ToString(s.ShardId))
	}
	sort.Strings(r.shards)
	r.iters, r.pos, r.seqAt = map[int]string{}, map[int]int{}, map[int]map[int]string{}
	if r.Checkpoints.pos == nil {
		r.Checkpoints.pos = map[int]checkpoint{}
	}
	return nil
}

// Poll implements simlog.Reader: up to 10 records of each shard, from
// the checkpoint on a shard's first read.
func (r *Reader) Poll(ctx context.Context) ([]simlog.Record, error) {
	if r.shards == nil {
		if err := r.open(ctx); err != nil {
			return nil, err
		}
	}
	var out []simlog.Record
	for i, id := range r.shards {
		if r.iters[i] == "" {
			r.Checkpoints.mu.Lock()
			cp := r.Checkpoints.pos[i]
			r.Checkpoints.mu.Unlock()
			in := &kinesis.GetShardIteratorInput{StreamName: aws.String(r.Stream), ShardId: aws.String(id), ShardIteratorType: types.ShardIteratorTypeTrimHorizon}
			if cp.seq != "" {
				in.ShardIteratorType, in.StartingSequenceNumber = types.ShardIteratorTypeAfterSequenceNumber, aws.String(cp.seq)
			}
			it, err := r.C.GetShardIterator(ctx, in)
			if err != nil {
				return nil, err
			}
			r.iters[i], r.pos[i] = aws.ToString(it.ShardIterator), cp.next
		}
		got, err := r.C.GetRecords(ctx, &kinesis.GetRecordsInput{ShardIterator: aws.String(r.iters[i]), Limit: aws.Int32(10)})
		if err != nil {
			r.iters[i] = "" // reopen from the checkpoint
			return nil, err
		}
		r.iters[i] = aws.ToString(got.NextShardIterator)
		for _, rec := range got.Records {
			if r.seqAt[i] == nil {
				r.seqAt[i] = map[int]string{}
			}
			r.seqAt[i][r.pos[i]] = aws.ToString(rec.SequenceNumber)
			out = append(out, simlog.Record{Partition: i, Offset: r.pos[i], Key: aws.ToString(rec.PartitionKey), Value: rec.Data})
			r.pos[i]++
		}
	}
	if len(out) == 0 {
		// Kinesis allows five GetRecords a second per shard
		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
		}
	}
	return out, nil
}

// Commit implements simlog.Reader: it checkpoints shard partition at
// next (a record this Reader read must be at next-1), and calls OnCommit.
func (r *Reader) Commit(ctx context.Context, partition, next int) error {
	seq, ok := r.seqAt[partition][next-1]
	if !ok {
		return fmt.Errorf("kinesislog: no record read at offset %d of shard %d", next-1, partition)
	}
	r.Checkpoints.mu.Lock()
	r.Checkpoints.pos[partition] = checkpoint{next, seq}
	r.Checkpoints.mu.Unlock()
	if r.OnCommit != nil {
		r.OnCommit(ctx, partition, next)
	}
	return nil
}
