// Package simlog is a partitioned log in a qenv simulation: Kafka's
// contract (Kinesis's is a subset), seen through two seams, Writer (a
// producer client) and Reader (one consumer of a group). Code written
// against them runs on the simulated log here and, unchanged, on Kafka
// (qenv/simlog/kafkalog, over franz-go) and Kinesis
// (qenv/simlog/kinesislog, over the AWS SDK), modules of their own.
//
// The simulated log's contract allows, and its faults produce, each a
// fault kind named after the log (Log.Name), option 0 nominal:
//   - a produce request lost before it lands, or landed with its
//     acknowledgement lost ("log.<name>.produce": lost, lostAck): the
//     producer client retries, several requests in flight per partition.
//     With Idempotent (Kafka's default), the log takes a producer's
//     records of a partition in sequence and drops its duplicates, so
//     retries keep the order; without, a retried record lands after the
//     ones sent behind it, and a lost acknowledgement writes it twice;
//   - a commit lost, or landed with its acknowledgement lost
//     ("log.<name>.commit": lost, lostAck);
//   - a fetch that goes back to the committed offset ("log.<name>.fetch":
//     rewind): a duplicate delivery, as after a fetch-session reset;
//   - a consumer evicted from its group at any step
//     ("rebalance.<name>.<consumer>", a nemesis, as a session timeout
//     does): the group's generation moves on, the partitions are
//     reassigned and read again from their committed offsets, and the
//     evicted consumer's commits are fenced (ErrFenced) until it rejoins
//     at its next poll. A consumer whose process crashes leaves the
//     group the same way.
//
// Partitions are read in offset order with no holes, and a record never
// changes: what a model may assume of any log. Under Sim.Check
// (GOTOGETHER_CHECK=1) the log checks that contract after every
// operation: offsets dense, deliveries the records appended there with
// no holes, an idempotent producer's records each landed once and in
// order, a commit landing only from the partition's owner.
package simlog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A Record is one record of the log, as a consumer reads it: its
// partition (a Kafka partition, a Kinesis shard by index) and offset (its
// index in the partition), its key, and its value.
type Record struct {
	Partition, Offset int
	Key               string
	Value             []byte
}

// Writer is the producer side of a log. Produce sends a record and calls
// done once with the outcome, nil when the log acknowledged it. Records
// of one key go to one partition, and are sent in the order produced;
// whether they land in that order after a retry is the client's business
// (Kafka's idempotent producer keeps it). done may run on another
// goroutine. Flush waits until every record produced is done.
type Writer interface {
	Produce(ctx context.Context, key string, value []byte, done func(error))
	Flush(ctx context.Context)
}

// Reader is one consumer's view of a log: it is assigned partitions
// (consumer group, leases), reads them from their committed offsets, and
// commits offsets. Commit(p, next) says that every record of p before
// next was processed; it may fail, and a consumer that lost the partition
// in a rebalance is refused (ErrFenced).
type Reader interface {
	Poll(ctx context.Context) ([]Record, error)
	Commit(ctx context.Context, partition, next int) error
}

var (
	// ErrFenced is a commit from a consumer that no longer owns the
	// partition.
	ErrFenced = errors.New("simlog: fenced: the partition was reassigned")
	// ErrLost is a request that never reached the log.
	ErrLost = errors.New("simlog: request lost")
	// ErrLostAck is a request that took effect, its acknowledgement lost.
	ErrLostAck = errors.New("simlog: acknowledgement lost")
	// ErrOutOfOrder is an idempotent producer's record sent ahead of an
	// earlier one that has not landed: retried after it.
	ErrOutOfOrder = errors.New("simlog: out of order sequence")
)

// Log is one topic and its one consumer group.
type Log struct {
	Name string
	// Batch is the most records a poll returns per partition (default 2).
	Batch int
	// OnCommit, if set, is called in the committing task when a commit
	// lands, before its acknowledgement (which may then be lost): the
	// place to record it as a model step.
	OnCommit func(ctx context.Context, partition, next int)

	sim       *qenv.Sim
	parts     [][]Record
	origins   [][]origin // per record: who produced it, for the contract check
	committed map[int]int
	seqs      map[int]map[int]int // producer -> partition -> last sequence taken
	producers int
	members   []*Member // the group, by name
	gen       int
	byName    map[string]*Member // each consumer name's current member

	produce, commit, fetch qenv.FaultKind
	broken                 string // a broken log, for the check to catch (export_test.go)
}

// origin is a record's producer, its sequence and whether it was
// idempotent, and a hash of what it carried.
type origin struct {
	producer, pseq int
	idempotent     bool
	sum            uint64
}

// New returns an empty log of n partitions in s.
func New(s *qenv.Sim, name string, n int) *Log {
	l := &Log{Name: name, Batch: 2, sim: s, parts: make([][]Record, n), origins: make([][]origin, n),
		committed: map[int]int{}, seqs: map[int]map[int]int{}, byName: map[string]*Member{},
		produce: qenv.DeclareFaults("log."+name+".produce", "lost", "lostAck"),
		commit:  qenv.DeclareFaults("log."+name+".commit", "lost", "lostAck"),
		fetch:   qenv.DeclareFaults("log."+name+".fetch", "rewind")}
	s.OnCrash(func(p *qenv.Process) {
		for _, m := range l.members {
			if m.proc == p {
				l.leave(m)
			}
		}
	})
	return l
}

// Partition is the partition of key: a hash, as Kafka's and Kinesis's
// clients choose one.
func (l *Log) Partition(key string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(len(l.parts)))
}

// Records are partition p's records, as they landed.
func (l *Log) Records(p int) []Record { return l.parts[p] }

// Committed is the group's committed offset of partition p.
func (l *Log) Committed(p int) int { return l.committed[p] }

func sum(r Record) uint64 {
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%d:%s:", len(r.Key), r.Key)
	_, _ = h.Write(r.Value)
	return h.Sum64()
}

// append lands a producer's record, by Kafka's idempotence rule if asked.
func (l *Log) append(producer, pseq int, idempotent bool, r Record) error {
	p := r.Partition
	if idempotent {
		last := l.seqs[producer][p]
		switch {
		case pseq <= last && l.broken != "dedupe":
			return nil // a duplicate: acknowledged, not written again
		case pseq > last+1:
			return ErrOutOfOrder
		}
		if l.seqs[producer] == nil {
			l.seqs[producer] = map[int]int{}
		}
		l.seqs[producer][p] = max(last, pseq)
	}
	r.Offset = len(l.parts[p])
	l.parts[p] = append(l.parts[p], r)
	l.origins[p] = append(l.origins[p], origin{producer, pseq, idempotent, sum(r)})
	l.check("a produce")
	return nil
}

// check verifies the log's contract under Sim.Check, after op: every
// partition's offsets dense, and an idempotent producer's records of a
// partition each landed once, in the order produced.
func (l *Log) check(op string) {
	if !l.sim.Check {
		return
	}
	for p, recs := range l.parts {
		last := map[int]int{} // idempotent producer -> its last sequence in p
		for o, r := range recs {
			if r.Partition != p || r.Offset != o {
				l.fail(op, "partition %d's record at offset %d says partition %d, offset %d", p, o, r.Partition, r.Offset)
			}
			og := l.origins[p][o]
			if !og.idempotent {
				continue
			}
			if want := last[og.producer] + 1; og.pseq != want {
				l.fail(op, "partition %d's record at offset %d is idempotent producer %d's sequence %d, want %d: a record landed twice, or out of order",
					p, o, og.producer, og.pseq, want)
			}
			last[og.producer] = og.pseq
		}
	}
}

func (l *Log) fail(op, format string, args ...any) {
	panic(fmt.Sprintf("simlog: log %s, after %s: %s (GOTOGETHER_CHECK)", l.Name, op, fmt.Sprintf(format, args...)))
}

// ---- producing ----

// Producer is a producer client of the log: records are sent per
// partition in the order produced, InFlight requests at a time (default
// 5, Kafka's), each retried until it lands. It implements Writer.
type Producer struct {
	Idempotent bool
	InFlight   int

	log         *Log
	id          int
	unsent      map[int][]*pending // per partition, in order
	pseq        map[int]int
	outstanding int
	sending     bool
}

type pending struct {
	rec  Record
	pseq int
	done func(error)
}

// Producer returns a new producer client (a new producer id: a restarted
// process's client does not inherit its predecessor's sequences).
func (l *Log) Producer(idempotent bool) *Producer {
	l.producers++
	return &Producer{Idempotent: idempotent, InFlight: 5, log: l, id: l.producers, unsent: map[int][]*pending{}, pseq: map[int]int{}}
}

// Produce implements Writer. The value is copied: the caller may reuse
// it.
func (p *Producer) Produce(ctx context.Context, key string, value []byte, done func(error)) {
	part := p.log.Partition(key)
	p.pseq[part]++
	p.unsent[part] = append(p.unsent[part], &pending{Record{Partition: part, Key: key, Value: bytes.Clone(value)}, p.pseq[part], done})
	p.outstanding++
	if !p.sending {
		p.sending = true
		qenv.Go(ctx, "producer.sender", p.send)
	}
}

// Flush implements Writer.
func (p *Producer) Flush(ctx context.Context) {
	qenv.Wait(ctx, func() bool { return p.outstanding == 0 })
}

// send sends each partition's first InFlight records, one request each,
// in order; those that failed are retried, still in order.
func (p *Producer) send(ctx context.Context) {
	sim := p.log.sim
	for {
		// Only waiting for another record is idle. Buffered sends, retry
		// backoffs and delivery callbacks remain work Settle must account for.
		qenv.WaitIdle(ctx, func() bool { return p.outstanding > 0 })
		parts := make([]int, 0, len(p.unsent))
		for part, q := range p.unsent {
			if len(q) > 0 {
				parts = append(parts, part)
			}
		}
		sort.Ints(parts)
		if len(parts) == 0 {
			// with nothing to send the loop would spin at one instant,
			// past MaxSteps (no scheduling point): say so instead
			panic(fmt.Sprintf("simlog: producer %d of log %s has %d records outstanding and none unsent", p.id, p.log.Name, p.outstanding))
		}
		retry := false
		for _, part := range parts {
			q := p.unsent[part]
			batch := q[:min(len(q), p.InFlight)]
			var keep []*pending
			for _, r := range batch {
				qenv.Yield(ctx)
				var err error
				switch sim.FaultOf(p.log.produce) {
				case "lost":
					err = ErrLost
				case "lostAck":
					if err = p.log.append(p.id, r.pseq, p.Idempotent, r.rec); err == nil {
						err = ErrLostAck
					}
				default:
					err = p.log.append(p.id, r.pseq, p.Idempotent, r.rec)
				}
				if err != nil {
					keep = append(keep, r)
					continue
				}
				p.outstanding--
				r.done(nil)
			}
			// the queue as it is now: a record produced while a request
			// yielded is behind the batch (once dropped, by putting back
			// q's tail, which left a record outstanding with none to send
			// and the sender spinning at one instant)
			p.unsent[part] = append(keep, p.unsent[part][len(batch):]...)
			retry = retry || len(keep) > 0
		}
		if retry {
			qenv.Sleep(ctx, 20*time.Millisecond)
		}
	}
}

// ---- consuming ----

// Member is one consumer of the group, as one process incarnation sees
// it. It joins at its first poll. It implements Reader.
type Member struct {
	Name string

	log  *Log
	proc *qenv.Process
	in   bool
	gen  int         // the generation its assignment and positions are of
	pos  map[int]int // next offset to fetch, per assigned partition
}

// Member returns the consumer name's member for the calling process.
// Each name can be evicted at any step (the "rebalance.<log>.<name>"
// nemesis).
func (l *Log) Member(ctx context.Context, name string) *Member {
	m := &Member{Name: name, log: l, proc: qenv.ProcessOf(ctx)}
	if _, ok := l.byName[name]; !ok {
		l.sim.Nemesis("rebalance."+l.Name+"."+name, func() bool { m := l.byName[name]; return m != nil && m.in }, func() { l.leave(l.byName[name]) })
	}
	l.byName[name] = m
	return m
}

func (l *Log) join(m *Member) {
	m.in = true
	l.members = append(l.members, m)
	sort.Slice(l.members, func(i, j int) bool { return l.members[i].Name < l.members[j].Name })
	l.gen++
}

func (l *Log) leave(m *Member) {
	for i, x := range l.members {
		if x == m {
			l.members = append(l.members[:i:i], l.members[i+1:]...)
			m.in = false
			l.gen++
			return
		}
	}
}

// assigned are m's partitions in the current generation: round robin over
// the members by name.
func (l *Log) assigned(m *Member) []int {
	var ps []int
	for p := range l.parts {
		if len(l.members) > 0 && l.members[p%len(l.members)] == m {
			ps = append(ps, p)
		}
	}
	return ps
}

// owns reports whether m, in the current generation, is assigned p.
func (l *Log) owns(m *Member, p int) bool {
	return m.in && m.gen == l.gen && slices.Contains(l.assigned(m), p)
}

func (m *Member) ready() bool {
	l := m.log
	if !m.in || m.gen != l.gen {
		return true
	}
	for p, o := range m.pos {
		if o < len(l.parts[p]) {
			return true
		}
	}
	return false
}

// Poll implements Reader: it waits for records of its partitions,
// rejoining the group first if it was evicted, and reading from the
// committed offsets after a rebalance (an eager one: every partition is
// revoked and assigned again). Values are copies: a consumer that
// changes one changes no record.
func (m *Member) Poll(ctx context.Context) ([]Record, error) {
	l := m.log
	qenv.WaitIdle(ctx, m.ready)
	if !m.in {
		l.join(m)
	}
	if m.gen != l.gen {
		m.gen, m.pos = l.gen, map[int]int{}
		for _, p := range l.assigned(m) {
			m.pos[p] = l.committed[p]
		}
	}
	if l.sim.FaultOf(l.fetch) == "rewind" {
		for p := range m.pos {
			m.pos[p] = l.committed[p]
		}
	}
	var out []Record
	for _, p := range slices.Sorted(maps.Keys(m.pos)) {
		from := m.pos[p]
		if l.broken == "holes" && from < len(l.parts[p]) {
			from++
		}
		recs := l.parts[p][from:]
		recs = recs[:min(len(recs), l.Batch)]
		for _, r := range recs {
			r.Value = bytes.Clone(r.Value)
			out = append(out, r)
		}
		l.checkDelivery(p, m.pos[p], recs)
		m.pos[p] = from + len(recs)
	}
	return out, nil
}

// checkDelivery verifies, under Sim.Check, that a poll's records of
// partition p follow on from the member's position with no hole, each
// the record that landed at its offset.
func (l *Log) checkDelivery(p, at int, recs []Record) {
	if !l.sim.Check {
		return
	}
	for i, r := range recs {
		if r.Offset != at+i {
			l.fail("a poll", "partition %d delivered offset %d where %d was next: a hole", p, r.Offset, at+i)
		}
		if sum(r) != l.origins[p][r.Offset].sum {
			l.fail("a poll", "partition %d's record at offset %d changed since it landed", p, r.Offset)
		}
	}
}

// Commit implements Reader. A commit that lands calls Log.OnCommit.
func (m *Member) Commit(ctx context.Context, partition, next int) error {
	l := m.log
	qenv.Yield(ctx)
	if l.broken != "fence" && (!m.in || m.gen != l.gen) {
		return ErrFenced
	}
	if _, ok := m.pos[partition]; !ok && l.broken != "fence" {
		return ErrFenced
	}
	f := l.sim.FaultOf(l.commit)
	if f == "lost" {
		return ErrLost
	}
	if l.sim.Check && !l.owns(m, partition) {
		l.fail("a commit", "%s committed partition %d, which it does not own in generation %d", m.Name, partition, l.gen)
	}
	l.committed[partition] = next
	l.check("a commit")
	if l.OnCommit != nil {
		l.OnCommit(ctx, partition, next)
	}
	if f == "lostAck" {
		return ErrLostAck
	}
	return nil
}
