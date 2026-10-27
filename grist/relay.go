package grist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/store"
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
// "grist/<store>/<tenant>" (RecorderID), numbered by its place in the
// tenant's step log and timed by its commit (the row's time, taken under
// the tenant's lock), on one thread (commit order): the same row makes
// the same record wherever and however often it is relayed, so
// qtrace.Reconstruct keeps one copy (a duplicate of a record is an info
// issue, "seen twice; kept the first"). A relay that starts afresh (a
// restarted process, a second node) records the log from its first row;
// one that lives across a node's restarts (WithRelay: gristsim's)
// resumes where it was. Its steps go to the recorder of the context it
// records with (qobs.From): App.Start's tasks record with the node's.
// With none, a step is recorded as qobs.Record records one: dropped, or
// in a strict run (qtest's) an escape that fails the run.
//
// A row the relay cannot read (a newer grist's version, a row no grist
// wrote) stops its tenant's relay, and Err says so; a transient read
// error is retried with backoff, and Err holds it until a read succeeds.
type Relay struct {
	s   Store
	log store.StepLog

	mu      sync.Mutex // tenants and errs only: no seam is reached while it is held
	tenants map[string]*relayed
	errs    map[string]error // a tenant's relay's error: why it stopped, or its last transient one
	stopped map[string]bool  // the tenants whose relay stopped (an unreadable row)
}

// relayed is a tenant's progress: the rows recorded, and the scratch
// recorder that numbers and normalizes its steps. mu guards both, and is
// held only between reading a row's progress and recording it, where no
// seam is reached: a task holding it is never descheduled or crashed
// there (a simulation's crash falls at a seam), so a crashed node's
// frozen relay never holds it, and two flushes of one Relay (a node's
// relay and a test's Flush, two incarnations' tasks) record each row
// once, in order.
type relayed struct {
	mu      sync.Mutex
	rows    int64
	done    atomic.Int64 // rows, for Behind: read without mu
	scratch *qobs.Recorder
	sink    *qobs.MemorySink
}

// NewRelay returns a relay of s's step log: an error if s keeps none.
func NewRelay(s Store) (*Relay, error) {
	log, ok := s.(store.StepLog)
	if !ok {
		return nil, fmt.Errorf("grist: store %s (%T) keeps no step log (store.StepLog): grist records every commit's step from the rows a store writes in the commit's transaction", s.Name(), s)
	}
	return &Relay{s: s, log: log, tenants: map[string]*relayed{}, errs: map[string]error{}, stopped: map[string]bool{}}, nil
}

// A StartOption configures App.Start (WithRelay). The zero StartOption
// changes nothing.
type StartOption struct{ apply func(*startConfig) }

// startConfig is App.Start's options.
type startConfig struct{ relay *Relay }

// WithRelay makes App.Start relay with r instead of a relay of its own:
// one that outlives a node's incarnations (gristsim's) resumes where the
// crashed one stopped, instead of recording the log again from its first
// row (whose copies Reconstruct would drop), and a harness waits on it
// (Behind). r must relay Start's store: another's is Start's error.
func WithRelay(r *Relay) StartOption {
	return StartOption{apply: func(c *startConfig) { c.relay = r }}
}

// RecorderID is the recorder of tenant's relayed steps at a store named
// storeName: "grist/<store>/<tenant>", each segment path-escaped, so no
// two (store, tenant) pairs share one.
func RecorderID(storeName, tenant string) string {
	return "grist/" + url.PathEscape(storeName) + "/" + url.PathEscape(tenant)
}

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
// commands without a node, or a harness before its settle. It may run
// beside a node relaying with the same Relay (WithRelay): each row is
// recorded once, by whichever gets it first.
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
// over every tenant (the store's count of each tenant's rows, less the
// relay's progress: no row is read): 0 once it caught up, which a real
// tier's harness waits for before it records a settle. A relay stopped
// by an unreadable row stays behind; Err says why.
func (r *Relay) Behind(ctx context.Context) (int64, error) {
	ts, err := r.s.Tenants(ctx)
	if err != nil {
		return 0, err
	}
	var behind int64
	for _, t := range ts {
		n, err := r.log.StepCount(ctx, t)
		if err != nil {
			return 0, err
		}
		behind += max(n-r.tenant(t).done.Load(), 0)
	}
	return behind, nil
}

// Err is what keeps the relay from recording: for each tenant whose
// relay stopped, the row it cannot read (permanent: a newer grist's row
// version, a row no grist wrote; fix the cause and start a node again),
// and for each still retrying, its last transient error (cleared by its
// next successful read). nil when every tenant's relay is well.
func (r *Relay) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ts := make([]string, 0, len(r.errs))
	for t := range r.errs {
		ts = append(ts, t)
	}
	sort.Strings(ts)
	var errs []error
	for _, t := range ts {
		errs = append(errs, r.errs[t])
	}
	return errors.Join(errs...)
}

// setErr notes tenant t's relay error (nil: well again); stop marks it
// stopped for good.
func (r *Relay) setErr(t string, err error, stop bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped[t] {
		return
	}
	if err == nil {
		delete(r.errs, t)
		return
	}
	r.errs[t] = err
	r.stopped[t] = stop
}

// isStopped reports whether t's relay stopped.
func (r *Relay) isStopped(t string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped[t]
}

// rowError is a row the relay cannot read: no retry reads it.
type rowError struct{ err error }

func (e *rowError) Error() string { return e.err.Error() }
func (e *rowError) Unwrap() error { return e.err }

// flush records tenant t's rows after its progress.
func (r *Relay) flush(ctx context.Context, t string) error {
	if r.isStopped(t) {
		return r.Err()
	}
	p := r.tenant(t)
	for {
		// rows after the progress: another flush may record some of them
		// first, which record skips (the log is dense, so the next row
		// not recorded is always the next one read)
		rows, err := r.log.Steps(ctx, t, p.done.Load(), relayBatch)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := r.record(ctx, t, p, row); err != nil {
				return err
			}
		}
		if len(rows) < relayBatch {
			return nil
		}
	}
}

// record records one row, if it is the next of t's (under p.mu, reaching
// no seam).
func (r *Relay) record(ctx context.Context, t string, p *relayed, row store.StepRow) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case row.Seq <= p.rows:
		return nil // recorded already, by another flush
	case row.Seq != p.rows+1:
		return &rowError{fmt.Errorf("grist: tenant %s's step log has row %d after %d: its rows must be numbered densely from 1 (store.StepRow.Seq)", t, row.Seq, p.rows)}
	}
	steps, err := decodeRow(row)
	if err != nil {
		return &rowError{fmt.Errorf("grist: tenant %s's step row %d: %w", t, row.Seq, err)}
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
	} else {
		for _, s := range steps {
			qobs.Record(ctx, s.action, nil) // no recorder: dropped, or a strict run's escape, as any step's
		}
	}
	p.rows = row.Seq
	p.done.Store(p.rows)
	return nil
}

// run relays tenant t until ctx is done: a daemon waiting on the step
// log. A row it cannot read stops it (Err); a transient error backs off.
func (r *Relay) run(ctx context.Context, t string) {
	tries := 0
	for ctx.Err() == nil {
		err := r.flush(ctx, t)
		var re *rowError
		switch {
		case errors.As(err, &re):
			r.setErr(t, err, true)
			return
		case err != nil && ctx.Err() == nil:
			r.setErr(t, fmt.Errorf("grist: relaying tenant %s (retrying): %w", t, err), false)
			tries++
			backoff(ctx, min(tries, 6))
			continue
		}
		r.setErr(t, nil, false)
		tries = 0
		r.log.WaitSteps(ctx, t, r.tenant(t).done.Load())
	}
}

// A rowStep is one step of a step row: its action, its fields but those
// the head gives, and how many of the batch's events are its.
type rowStep struct {
	Action string  `json:"action"`
	KV     [][]any `json:"kv"`
	Events int     `json:"events"`
}

// stepRowVersion is the version of the step rows this grist writes and
// reads: a row of another is the relay's permanent error, naming the fix.
const stepRowVersion = 1

// A stepRow is what Batch.Step holds: its version and the batch's steps
// (a command's one, an Emit batch's one per trigger, in trigger order).
// Its time is the row's (store.StepRow.Time, the commit's).
type stepRow struct {
	V     int       `json:"v"`
	Steps []rowStep `json:"steps"`
}

// encodeRow is a batch's step row: steps' fields (key, value pairs) but
// obs.head and order.pos, which the row's head gives.
func encodeRow(steps []rowStep) json.RawMessage {
	b, err := json.Marshal(stepRow{V: stepRowVersion, Steps: steps})
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

// relayStep is a decoded step, ready to record.
type relayStep struct {
	action string
	kv     []any
	time   time.Time
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
	if r.V != stepRowVersion {
		return nil, fmt.Errorf("version %d, where this grist reads version %d: the row was written by another grist (a newer one: upgrade this node's), or by no grist (version 0: a row written before rows had one, or by hand); its steps are not recorded until a grist that reads it relays them", r.V, stepRowVersion)
	}
	if row.Time == 0 {
		return nil, fmt.Errorf("no time: the store must stamp each row with its commit's time (store.StepRow.Time, store.StepTime)")
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
		out = append(out, relayStep{action: s.Action, kv: kv, time: time.Unix(0, row.Time)})
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
