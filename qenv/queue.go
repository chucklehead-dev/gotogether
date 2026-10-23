package qenv

import (
	"context"
	"fmt"
	"sort"
)

// Queue is an at-least-once message queue: a published message is
// delivered until a consumer acknowledges it. Its contract allows, and its
// faults produce:
//   - redelivery: an acknowledgement is lost ("queue.<name>.ack"), or the
//     consumer crashed holding the message;
//   - reordering, unless Ordered: which pending message is delivered next
//     is a schedule choice;
//   - with a broker process (NewQueue), partitions (Sim.Net) and the
//     broker's crash: a publisher or consumer cut off from the broker, or
//     whose broker is down, waits until it heals or restarts, and a
//     consumer's cut or the broker's crash drops its connection, so the
//     messages it holds are redelivered and its later acknowledgements are
//     lost. The broker keeps its messages across a crash (a durable queue).
//
// Consumers must therefore be idempotent, and must not rely on order
// across messages.
type Queue struct {
	Name string
	// Ordered delivers the oldest pending message first (a single FIFO
	// partition). Redelivered messages go back to the front.
	Ordered bool
	sim     *Sim
	proc    *Process  // the broker's (NewQueue), or nil
	ack     FaultKind // "queue.<name>.ack"
	pending []*Delivery
	held    map[*Delivery]*Process
	next    int
	acked   map[int]bool // under Sim.Check: the messages acknowledged
	forget  bool         // a broken queue, for the check to catch (export_test.go)
	// Published counts messages published, Redelivered redeliveries.
	Published, Redelivered int
}

// Delivery is one delivery of a message.
type Delivery struct {
	ID          int // the message's id: equal across its redeliveries
	Body        any
	Redelivered bool
	q           *Queue
}

// NewQueue returns an empty queue in s whose broker runs in process p: the
// network between it and its clients can be partitioned. With p nil the
// broker is of no process, and no partition reaches the queue. Its lost
// acknowledgements are the fault kind "queue.<name>.ack" ("queue" covers
// every queue's).
func NewQueue(s *Sim, name string, p *Process) *Queue {
	q := &Queue{Name: name, sim: s, proc: p, held: map[*Delivery]*Process{},
		ack: DeclareFaults("queue."+name+".ack", "lost")}
	if s.Check {
		q.acked = map[int]bool{}
	}
	// unacknowledged messages of a crashed consumer are redelivered, as
	// are those of one whose connection a partition or the broker's crash
	// dropped
	s.OnCrash(func(c *Process) {
		if c != q.proc {
			q.drop(c)
			return
		}
		q.putBack(q.heldIn())
		q.check("the broker's crash")
	})
	s.net.OnCut(func(a, b *Process) {
		switch {
		case q.proc == nil:
		case a == q.proc:
			q.drop(b)
		case b == q.proc:
			q.drop(a)
		}
	})
	return q
}

// drop requeues the messages p holds, at the front, oldest first.
func (q *Queue) drop(p *Process) {
	var back []*Delivery
	for _, d := range q.heldIn() {
		if q.held[d] == p {
			back = append(back, d)
		}
	}
	q.putBack(back)
	q.check("dropping " + p.Name + "'s deliveries")
}

// putBack requeues held deliveries ds (in id order) at the front, oldest
// first, as a FIFO broker redelivers them.
func (q *Queue) putBack(ds []*Delivery) {
	for i := len(ds) - 1; i >= 0; i-- {
		delete(q.held, ds[i])
		if !q.forget {
			q.requeue(ds[i])
		}
	}
}

// check verifies the queue's contract under Sim.Check, after op: every
// message published and not acknowledged is pending or held (none lost),
// none acknowledged is (none redelivered after its acknowledgement), and
// an Ordered queue's first deliveries wait in publication order.
func (q *Queue) check(op string) {
	if q.acked == nil {
		return
	}
	// O(pending + held) per operation: the live messages, counted against
	// those published and not acknowledged; the scan over every id runs
	// only to name the one lost
	live := map[int]bool{}
	last := 0
	for _, d := range q.pending {
		live[d.ID] = true
		if q.Ordered && !d.Redelivered {
			if d.ID < last {
				panic(fmt.Sprintf("qenv: queue %s, after %s: message %d waits behind %d, though Ordered (GOTOGETHER_CHECK)", q.Name, op, d.ID, last))
			}
			last = d.ID
		}
	}
	for d := range q.held {
		live[d.ID] = true
	}
	for _, id := range sortedIDs(live) {
		if q.acked[id] {
			panic(fmt.Sprintf("qenv: queue %s, after %s: message %d is delivered again after its acknowledgement (GOTOGETHER_CHECK)", q.Name, op, id))
		}
	}
	if len(live) == q.next-len(q.acked) {
		return
	}
	for id := 1; id <= q.next; id++ {
		if !q.acked[id] && !live[id] {
			panic(fmt.Sprintf("qenv: queue %s, after %s: message %d was lost, neither acknowledged nor pending nor held (GOTOGETHER_CHECK)", q.Name, op, id))
		}
	}
}

// sortedIDs are m's keys in order: a check's first failure is the same
// in every run.
func sortedIDs(m map[int]bool) []int {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// reaches reports whether the task of ctx can reach the broker: it is up,
// and the network lets it.
func (q *Queue) reaches(ctx context.Context) bool {
	return q.proc == nil || !q.proc.Down() && !q.sim.net.Partitioned(ProcessOf(ctx), q.proc)
}

func (q *Queue) heldIn() []*Delivery {
	var ds []*Delivery
	for d := range q.held {
		ds = append(ds, d)
	}
	// deterministic: by message id
	for i := 1; i < len(ds); i++ {
		for j := i; j > 0 && ds[j].ID < ds[j-1].ID; j-- {
			ds[j], ds[j-1] = ds[j-1], ds[j]
		}
	}
	return ds
}

func (q *Queue) requeue(d *Delivery) {
	q.Redelivered++
	r := &Delivery{ID: d.ID, Body: d.Body, Redelivered: true, q: q}
	q.pending = append([]*Delivery{r}, q.pending...)
}

// Publish enqueues a message. Cut off from the broker, it waits until the
// partition heals: the publisher retries until the broker confirms.
func (q *Queue) Publish(ctx context.Context, body any) {
	Yield(ctx)
	Wait(ctx, func() bool { return q.reaches(ctx) })
	q.next++
	q.Published++
	q.pending = append(q.pending, &Delivery{ID: q.next, Body: body, q: q})
	q.check("a publish")
}

// Receive waits for a message and takes it.
func (q *Queue) Receive(ctx context.Context) *Delivery {
	WaitIdle(ctx, func() bool { return len(q.pending) > 0 && q.reaches(ctx) })
	i := 0
	if !q.Ordered {
		i = q.sim.Choose("sched.queue."+q.Name, len(q.pending))
	}
	d := q.pending[i]
	q.pending = append(q.pending[:i:i], q.pending[i+1:]...)
	q.held[d] = ProcessOf(ctx)
	q.check("a receive")
	return d
}

// Ack acknowledges a delivery. The acknowledgement may be lost
// ("queue.<name>.ack"): the message is then delivered again.
func (d *Delivery) Ack(ctx context.Context) {
	Yield(ctx)
	q := d.q
	if _, ok := q.held[d]; !ok {
		return
	}
	delete(q.held, d)
	if q.sim.FaultOf(q.ack) == "lost" {
		q.requeue(d)
	} else if q.acked != nil {
		q.acked[d.ID] = true
	}
	q.check("an acknowledgement")
}

// Nack returns a delivery to the queue for redelivery.
func (d *Delivery) Nack(ctx context.Context) {
	Yield(ctx)
	if _, ok := d.q.held[d]; ok {
		delete(d.q.held, d)
		d.q.requeue(d)
	}
	d.q.check("a nack")
}

// Pending is the number of messages waiting for delivery.
func (q *Queue) Pending() int { return len(q.pending) }

// InFlight is the number of messages taken and not yet acknowledged.
func (q *Queue) InFlight() int { return len(q.held) }
