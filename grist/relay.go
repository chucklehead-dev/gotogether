package grist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A Relay records the steps of every commit from the store's step log
// (store.StepLog): each commit writes its step as a row in its own
// transaction (Batch.Step), and the relay reads the committed rows in
// order and records them. It is the only way grist records a commit or a
// committed refusal, on every tier: no hook runs at the commit, so a node
// that dies between its COMMIT and its record loses no step; the
// restarted node's relay (or any other node's) records the row
// (docs/grist.md §2.10, §3.8).
//
// A relayed step is recorded under a recorder of its own,
// "grist/<store>/<tenant>", numbered by its place in the tenant's step
// log and timed by its batch, on one thread (commit order): the same row
// makes the same record wherever and however often it is relayed, so
// qtrace.Reconstruct keeps one copy (a duplicate of a record is an info
// issue, "seen twice; kept the first"). A relay that starts afresh (a
// restarted process, a second node) records the log from its first row;
// one that lives across a node's restarts (WithRelay: gristsim's)
// resumes where it was. Its steps go to the recorder of the context it
// records with (qobs.From): App.Start's tasks record with the node's.
type Relay struct {
	s   Store
	log store.StepLog

	mu      sync.Mutex // the tenants map only: no seam is reached while it is held
	tenants map[string]*relayed
}

// relayed is a tenant's progress: the rows recorded, and the scratch
// recorder that numbers and normalizes its steps.
type relayed struct {
	rows    int64
	done    atomic.Int64 // rows, for Behind: read by other goroutines
	scratch *qobs.Recorder
	sink    *qobs.MemorySink
}

// NewRelay returns a relay of s's step log: an error if s keeps none.
func NewRelay(s Store) (*Relay, error) {
	log, ok := s.(store.StepLog)
	if !ok {
		return nil, fmt.Errorf("grist: store %s (%T) keeps no step log (store.StepLog): grist records every commit's step from the rows a store writes in the commit's transaction", s.Name(), s)
	}
	return &Relay{s: s, log: log, tenants: map[string]*relayed{}}, nil
}

type relayKey struct{}

// WithRelay returns a context whose App.Start relays with r instead of a
// relay of its own: one that outlives a node's incarnations resumes
// where the crashed one stopped, instead of recording the log again from
// its first row (whose copies Reconstruct would drop).
func WithRelay(ctx context.Context, r *Relay) context.Context {
	return context.WithValue(ctx, relayKey{}, r)
}

// relayOf is ctx's relay of s (WithRelay's if it relays s), else a new one.
func relayOf(ctx context.Context, s Store) (*Relay, error) {
	if r, ok := ctx.Value(relayKey{}).(*Relay); ok && r.s == s {
		return r, nil
	}
	return NewRelay(s)
}

// RecorderID is the recorder of tenant's relayed steps at a store named
// storeName.
func RecorderID(storeName, tenant string) string { return "grist/" + storeName + "/" + tenant }

// relayThread is the thread of a tenant's relayed steps: commit order.
const relayThread = "commits"

func (r *Relay) tenant(t string) *relayed {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.tenants[t]
	if p == nil {
		p = &relayed{sink: &qobs.MemorySink{}}
		p.scratch = qobs.NewRecorder(p.sink, qobs.WithID(RecorderID(r.s.Name(), t)), qobs.WithActor(t), qobs.WithProcess(r.s.Name()))
		r.tenants[t] = p
	}
	return p
}

// relayBatch is how many rows a relay reads at once.
const relayBatch = 100

// Flush records every committed step of every tenant that this relay has
// not recorded, and returns once they are recorded: for a test that runs
// commands without a node. It must not run beside a node's relay of the
// same Relay (App.Start's tasks), each tenant's progress being one
// task's.
func (r *Relay) Flush(ctx context.Context) error {
	ts, err := r.s.Tenants(ctx)
	if err != nil {
		return err
	}
	for _, t := range ts {
		if err := r.flush(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

// Behind is how many committed step rows the relay has not recorded,
// over every tenant: 0 once it caught up, which a real tier's harness
// waits for before it records a settle.
func (r *Relay) Behind(ctx context.Context) (int64, error) {
	ts, err := r.s.Tenants(ctx)
	if err != nil {
		return 0, err
	}
	var behind int64
	for _, t := range ts {
		done := r.tenant(t).done.Load()
		rows, err := r.log.Steps(ctx, t, done, 0)
		if err != nil {
			return 0, err
		}
		behind += int64(len(rows))
	}
	return behind, nil
}

// flush records tenant t's rows after its progress.
func (r *Relay) flush(ctx context.Context, t string) error {
	p := r.tenant(t)
	for {
		rows, err := r.log.Steps(ctx, t, p.rows, relayBatch)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Seq != p.rows+1 {
				return fmt.Errorf("grist: tenant %s's step log has row %d after %d: its rows must be numbered densely from 1 (store.StepRow.Seq)", t, row.Seq, p.rows)
			}
			steps, err := decodeRow(row)
			if err != nil {
				return fmt.Errorf("grist: tenant %s's step row %d: %w", t, row.Seq, err)
			}
			for _, s := range steps {
				p.scratch.Record(context.Background(), s.action, nil, s.kv...)
			}
			out := p.sink.Steps()
			p.sink.Reset()
			if rec := qobs.From(ctx); rec != nil {
				for i := range out {
					out[i].Time, out[i].Thread = steps[i].time, relayThread
				}
				rec.Add(out...)
			}
			p.rows = row.Seq // no seam since the rows were read: a crash cannot fall between the record and this
			p.done.Store(p.rows)
		}
		if len(rows) < relayBatch {
			return nil
		}
	}
}

// run relays tenant t until ctx is done: a daemon waiting on the step
// log.
func (r *Relay) run(ctx context.Context, t string) {
	tries := 0
	for ctx.Err() == nil {
		if err := r.flush(ctx, t); err != nil {
			tries++
			backoff(ctx, min(tries, 6))
			continue
		}
		tries = 0
		r.log.WaitSteps(ctx, t, r.tenant(t).rows)
	}
}

// A rowStep is one step of a step row: its action, its fields but those
// the head gives, and how many of the batch's events are its.
type rowStep struct {
	Action string  `json:"action"`
	KV     [][]any `json:"kv"`
	Events int     `json:"events"`
}

// A stepRow is what Batch.Step holds: the batch's steps (a command's
// one, an Emit batch's one per trigger, in trigger order) and its time.
type stepRow struct {
	Time  int64     `json:"time"` // Unix nanoseconds: the steps' time (true time in a simulation)
	Steps []rowStep `json:"steps"`
}

// relayStep is a decoded step, ready to record.
type relayStep struct {
	action string
	kv     []any
	time   time.Time
}

// encodeRow is a batch's step row: steps' fields (key, value pairs) but
// obs.head and order.pos, which the row's head gives.
func encodeRow(ctx context.Context, steps []rowStep) json.RawMessage {
	b, err := json.Marshal(stepRow{Time: stepTime(ctx).UnixNano(), Steps: steps})
	if err != nil {
		panic(fmt.Sprintf("grist: a step row: %v", err)) // the fields are grist's own scalars and lists
	}
	return b
}

// newRowStep is a rowStep of action with kv's pairs and n events.
func newRowStep(action string, n int, kv ...any) rowStep {
	s := rowStep{Action: action, Events: n, KV: [][]any{}}
	for i := 0; i+1 < len(kv); i += 2 {
		s.KV = append(s.KV, []any{kv[i], kv[i+1]})
	}
	return s
}

// stepTime is the time a step is recorded at: the simulation's true time
// (as qobs stamps a step recorded inside one), else the wall clock's.
func stepTime(ctx context.Context) time.Time {
	if sim := qenv.From(ctx); sim != nil {
		return sim.Now()
	}
	return time.Now() //qenv:ok outside a simulation: a step's time is the wall clock's, as qobs stamps it
}

// decodeRow is a row's steps with the fields the head gives: each step's
// obs.head (the head after its own events) and order.pos (2·that head
// with events, 2·it+1 without: docs/grist.md §3.1).
func decodeRow(row store.StepRow) ([]relayStep, error) {
	var r stepRow
	d := json.NewDecoder(bytes.NewReader(row.Step))
	d.UseNumber()
	if err := d.Decode(&r); err != nil {
		return nil, fmt.Errorf("not a step row grist wrote: %v", err)
	}
	total := 0
	for _, s := range r.Steps {
		total += s.Events
	}
	head := row.Head - Position(total) // the head before the batch
	out := make([]relayStep, 0, len(r.Steps))
	for _, s := range r.Steps {
		head += Position(s.Events)
		kv := make([]any, 0, 2*len(s.KV)+4)
		for _, p := range s.KV {
			if len(p) != 2 {
				return nil, fmt.Errorf("field %v is no (key, value) pair", p)
			}
			kv = append(kv, p[0], numbers(p[1]))
		}
		kv = append(kv, "obs.head", int64(head), "order.pos", commitOrder(s.Events, head))
		out = append(out, relayStep{action: s.Action, kv: kv, time: time.Unix(0, r.Time)})
	}
	return out, nil
}

// numbers is v with its JSON numbers as int64 (or float64 with a
// fraction), as the step's own fields were: qobs records integers as
// int64, and lists as their JSON text, which a json.Number keeps.
func numbers(v any) any {
	if n, ok := v.(json.Number); ok {
		if i, err := strconv.ParseInt(string(n), 10, 64); err == nil {
			return i
		}
		f, _ := n.Float64()
		return f
	}
	return v
}

// relay starts the relay of s for every tenant (one daemon task each,
// qenv.Go), with ctx's relay if WithRelay gave one.
func (a *App) relay(ctx context.Context, s Store, tenants []string) error {
	r, err := relayOf(ctx, s)
	if err != nil {
		return err
	}
	for _, t := range tenants {
		t := t
		qenv.Go(ctx, "grist.relay."+t, func(ctx context.Context) {
			qenv.Daemon(ctx)
			r.run(ctx, t)
		})
	}
	return nil
}
