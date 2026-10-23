package qenv

import (
	"context"
	"fmt"
)

// Queue is an at-least-once message queue: a published message is
// delivered until a consumer acknowledges it. Its contract allows, and its
// faults produce:
//   - redelivery: an acknowledgement is lost ("queue.ack"), or the
//     consumer crashed holding the message;
//   - reordering, unless Ordered: which pending message is delivered next
//     is a schedule choice;
//   - with Proc set, partitions (Sim.Partition, Sim.Partitions): a
//     publisher or consumer cut off from the broker waits until it heals,
//     and a consumer's cut drops its connection, so the messages it holds
//     are redelivered and its later acknowledgements are lost.
//
// Consumers must therefore be idempotent, and must not rely on order
// across messages.
type Queue struct {
	Name string
	// Ordered delivers the oldest pending message first (a single FIFO
	// partition). Redelivered messages go back to the front.
	Ordered bool
	// Proc is the broker's process, if it has one: the network between it
	// and its clients can be partitioned. Without one no partition reaches
	// the queue.
	Proc *Process

	sim     *Sim
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

// NewQueue returns an empty queue in s.
func NewQueue(s *Sim, name string) *Queue {
	q := &Queue{Name: name, sim: s, held: map[*Delivery]*Process{}}
	if s.Check {
		q.acked = map[int]bool{}
	}
	// unacknowledged messages of a crashed consumer are redelivered, as
	// are those of one whose connection a partition dropped
	s.OnCrash(q.drop)
	s.onCut = append(s.onCut, func(a, b *Process) {
		switch {
		case q.Proc == nil:
		case a == q.Proc:
			q.drop(b)
		case b == q.Proc:
			q.drop(a)
		}
	})
	return q
}

// drop requeues the messages p holds, in the order they were taken.
func (q *Queue) drop(p *Process) {
	var back []*Delivery
	for _, d := range q.heldIn() {
		if q.held[d] == p {
			back = append(back, d)
			delete(q.held, d)
		}
	}
	for _, d := range back {
		if !q.forget {
			q.requeue(d)
		}
	}
	q.check("dropping " + p.Name + "'s deliveries")
}

// check verifies the queue's contract under Sim.Check, after op: every
// message published and not acknowledged is pending or held (none lost),
// none acknowledged is (none redelivered after its acknowledgement), and
// an Ordered queue's first deliveries wait in publication order.
func (q *Queue) check(op string) {
	if q.acked == nil {
		return
	}
	copies := map[int]int{}
	last := 0
	for _, d := range q.pending {
		copies[d.ID]++
		if q.Ordered && !d.Redelivered {
			if d.ID < last {
				panic(fmt.Sprintf("qenv: queue %s, after %s: message %d waits behind %d, though Ordered (GOTOGETHER_CHECK)", q.Name, op, d.ID, last))
			}
			last = d.ID
		}
	}
	for d := range q.held {
		copies[d.ID]++
	}
	for id := 1; id <= q.next; id++ {
		switch {
		case q.acked[id] && copies[id] > 0:
			panic(fmt.Sprintf("qenv: queue %s, after %s: message %d is delivered again after its acknowledgement (GOTOGETHER_CHECK)", q.Name, op, id))
		case !q.acked[id] && copies[id] == 0:
			panic(fmt.Sprintf("qenv: queue %s, after %s: message %d was lost, neither acknowledged nor pending nor held (GOTOGETHER_CHECK)", q.Name, op, id))
		}
	}
}

// reaches reports whether the network lets the task of ctx reach the
// broker.
func (q *Queue) reaches(ctx context.Context) bool {
	return q.Proc == nil || !q.sim.Partitioned(ProcessOf(ctx), q.Proc)
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

// queueAck is a lost acknowledgement: the message is delivered again.
var queueAck = DeclareFaults("queue.ack", "lost")

// Ack acknowledges a delivery. The acknowledgement may be lost
// ("queue.ack"): the message is then delivered again.
func (d *Delivery) Ack(ctx context.Context) {
	Yield(ctx)
	q := d.q
	if _, ok := q.held[d]; !ok {
		return
	}
	delete(q.held, d)
	if q.sim.FaultOf(queueAck) == "lost" {
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
