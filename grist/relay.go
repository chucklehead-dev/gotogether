package grist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
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
	// app, once a node started with the relay (App.Start), re-renders a
	// row's events stored at an older version as the newest (upcast):
	// the model sees only the newest shapes. Of the Apps started with it,
	// the newest release's (one reading every version the others read)
	app atomic.Pointer[App]

	// clocks are the node's memory of each tenant's log (rewind.go): what
	// a rewind is seen against, and the base its steps' clocks are at
	clocks clocks

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
	// marks are the rows recorded, to tell whether the log still holds
	// them (rewind.go); epoch counts the rewinds recorded, so a flush that
	// read rows before one records none of them
	marks []rowMark
	epoch int
	// verified is the head after the last row recorded, causes each
	// recorded event's cause by position (the last markWindow): what a
	// poller checks a trigger against before it handles it (verify)
	verified atomic.Int64
	causes   map[Position]string
}

// NewRelay returns a relay of s's step log: an error if s keeps none.
func NewRelay(s Store) (*Relay, error) {
	log, ok := s.(store.StepLog)
	if !ok {
		return nil, fmt.Errorf("grist: store %s (%T) keeps no step log (store.StepLog): grist records every commit's step from the rows a store writes in the commit's transaction", s.Name(), s)
	}
	return &Relay{s: s, log: log, tenants: map[string]*relayed{}, errs: map[string]error{}, stopped: map[string]bool{}}, nil
}

// RenderAs makes the relay render its rows' events as a reads them:
// those stored at an older version than a's newest upcast, so their steps
// are recorded in a's shapes, its model's. App.Start renders as the newest
// release started with the relay (one reading every version the others
// read); a relay that records a rolling update from its first step, its
// older release's nodes starting first (gristsim's Config.From), renders
// as the newer release from the start.
// Known source and upcast scalar shapes are checked even for current-version
// and already-rendered rows. Invalid data stops the row without recording it;
// an opaque mixed row requiring re-rendering needs a release reading all its
// types and versions. CheckStoredBatch provides the full retained-row audit.
func (r *Relay) RenderAs(a *App) { r.app.Store(a) }

// A StartOption configures App.Start (WithRelay). The zero StartOption
// changes nothing.
type StartOption struct{ apply func(*startConfig) }

// startConfig is App.Start's options.
type startConfig struct {
	relay  *Relay
	onNode func(*node) // serve's: the node, to wait for it to quiet before a settle
}

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
		p.scratch = qobs.NewRecorder(p.sink, qobs.WithID(RecorderID(r.s.Name(), t)), qobs.WithActor(rec.Actor(r.s.Name(), t)), qobs.WithProcess(r.s.Name()))
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
		if r.clocks.toRecord(t) {
			if err := r.recordRewind(ctx, t, p); err != nil {
				return err
			}
		}
		// the last row recorded, then the rows after the progress: another
		// flush may record some of them first, which record skips (the log
		// is dense, so the next row not recorded is always the next one
		// read). The last row is read again to see that the log still holds
		// it: a log that went backwards (ErrRewound) no longer has it, or has
		// another in its place
		done := p.done.Load()
		p.mu.Lock()
		epoch := p.epoch
		mark, marked := p.markOf(done)
		p.mu.Unlock()
		rows, err := r.log.Steps(ctx, t, max(done-1, 0), relayBatch+1)
		if err != nil {
			return err
		}
		if done > 0 && marked {
			switch {
			case len(rows) > 0 && rows[0].Seq > done:
				// pruned (sqlstore.Prune): the rows after it are there, which
				// a log that went back would not have
			case len(rows) == 0:
				n, err := r.log.StepCount(ctx, t)
				if err != nil {
					return err
				}
				if n >= done {
					break // pruned, the count kept
				}
				r.clocks.stopFor(t, &RewoundError{Tenant: t, Seen: mark.head, Steps: fmt.Sprintf("row %d, which it recorded, is gone (%d rows)", done, n)})
				continue
			case !mark.same(rows[0]):
				r.clocks.stopFor(t, &RewoundError{Tenant: t, Seen: mark.head, Head: rows[0].Head,
					Steps: fmt.Sprintf("row %d is not the row it recorded (head %d, now %d)", done, mark.head, rows[0].Head)})
				continue
			}
		}
		if len(rows) > 0 && rows[0].Seq == done && done > 0 {
			rows = rows[1:]
		}
		for _, row := range rows {
			if row.Seq <= p.done.Load() {
				continue // recorded already, by another flush
			}
			// decoded before the record's lock: upcasting reads the store
			steps, err := r.decode(ctx, t, row)
			if err != nil {
				return err
			}
			if err := r.record(ctx, t, p, row, steps, epoch); err != nil {
				return err
			}
		}
		if len(rows) < relayBatch {
			return nil
		}
	}
}

// decode is a row's steps, its events stored at an older version
// re-rendered as the newest (upcastRow).
func (r *Relay) decode(ctx context.Context, t string, row store.StepRow) ([]relayStep, error) {
	steps, err := decodeRow(row)
	if err != nil {
		return nil, &rowError{fmt.Errorf("grist: tenant %s's step row %d: %w", t, row.Seq, err)}
	}
	if app := r.app.Load(); app != nil {
		if err := app.upcastRow(ctx, r.s, t, steps); err != nil {
			return nil, err
		}
	}
	for i := range steps {
		steps[i].kv = dropKV(steps[i].kv, renderedKey) // the relay's, no step's field
	}
	return steps, nil
}

// record records one row's steps, if it is the next of t's (under p.mu,
// reaching no seam).
func (r *Relay) record(ctx context.Context, t string, p *relayed, row store.StepRow, steps []relayStep, epoch int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.epoch != epoch:
		return nil // read before a rewind was recorded: the next flush reads the new history
	case row.Seq <= p.rows:
		return nil // recorded already, by another flush
	case p.rows == 0 && len(p.marks) == 0 && row.Seq > 1:
		// a log pruned before its first row this relay reads
		// (sqlstore.Prune): recorded from its oldest row kept
	case row.Seq != p.rows+1:
		return &rowError{fmt.Errorf("grist: tenant %s's step log has row %d after %d: its rows must be numbered densely from 1 (store.StepRow.Seq)", t, row.Seq, p.rows)}
	}
	base := r.clocks.base(t)
	resumed := false
	for _, s := range steps {
		shiftOrders(s.kv, base)                                          // after a rewind, on the tenant's new clock
		p.scratch.Record(context.Background(), s.action, s.err, s.kv...) // an error's step keeps its kv's outcome
		resumed = resumed || s.action == "gristResumed"
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
	r.mark(p, row, steps)
	p.noteCauses(steps)
	p.verified.Store(int64(row.Head))
	r.clocks.saw(t, row.Head)
	if resumed {
		r.clocks.resume(t) // an operator's resume (Resume): the processors go on
	}
	return nil
}

// export records tenant t's rows after row after into ctx's recorder:
// `<app> steps -export`'s, a fresh relay's.
func (r *Relay) export(ctx context.Context, t string, after int64) error {
	p := r.tenant(t)
	p.mu.Lock()
	p.rows = after
	p.done.Store(after)
	p.mu.Unlock()
	return r.flush(ctx, t)
}

// run relays tenant t until ctx is done. Waiting for a new step is idle;
// flushing rows and retrying errors remain work for simulation completion.
// A row it cannot read stops it (Err); a transient error backs off.
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
	// Error is the step's error text, if it has one beside its outcome: a
	// refusal of a command that declares none (Refuses) says what to
	// declare, so the binding's "no mapping" names the fix. A grist that
	// does not know the field records the step without it.
	Error string `json:"error,omitempty"`
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
	err    error
	// events and head: how many events of the batch are its, and the
	// head after them
	events int
	head   Position
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
		var err error
		if s.Error != "" {
			err = errors.New(s.Error)
		}
		out = append(out, relayStep{action: s.Action, kv: kv, time: time.Unix(0, row.Time), err: err, events: s.Events, head: head})
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

// upcastRow validates known event shapes before recording a row, and renders
// older observations for today's model. Unknown/newer shapes stay opaque to a
// rolling reader. A mixed row needing rendering cannot safely split aggregate
// scalar lists, so it fails rather than dropping unknown observations.
func (a *App) upcastRow(ctx context.Context, s Store, t string, steps []relayStep) error {
	for i := range steps {
		st := &steps[i]
		if st.events == 0 {
			continue
		}
		types := kvStrings(st.kv, "obs.events")
		if st.events < 0 || len(types) != st.events {
			return &rowError{fmt.Errorf("grist: tenant %s's step at %d has invalid event observations", t, st.head)}
		}
		evs, _, err := s.Read(ctx, t, Selector{Types: uniq(types), After: st.head - Position(st.events)}, st.head)
		if err != nil {
			return err
		}
		if len(evs) != st.events {
			return &rowError{fmt.Errorf("grist: tenant %s's step at %d has %d events, and the store %d of their types through it", t, st.head, st.events, len(evs))}
		}
		older, opaque := false, false
		strs, ints := []string{}, []int64{}
		for _, e := range evs {
			info := a.events[e.Type]
			if info == nil || storedVersion(e.Version) > info.version {
				opaque = true
				continue
			}
			older = older || storedVersion(e.Version) < info.version
			v, typ, _, err := info.storedValue(e.Event, info.version)
			if err != nil {
				return &rowError{fmt.Errorf("grist: tenant %s's event %d: %w", t, e.Pos, err)}
			}
			body, err := json.Marshal(v.Interface())
			if err != nil {
				return &rowError{err}
			}
			ss, ns, err := scalars(typ, body)
			if err != nil {
				return &rowError{fmt.Errorf("grist: tenant %s's event %d observations: %w", t, e.Pos, err)}
			}
			strs, ints = append(strs, ss...), append(ints, ns...)
		}
		if !older || a.renderedNewest(types, kvInts(st.kv, renderedKey)) {
			continue
		}
		if opaque {
			return &rowError{fmt.Errorf("grist: tenant %s's step at %d needs re-rendering but includes unknown or newer event shapes: use a release that reads every event type and version in this row", t, st.head)}
		}
		setKV(st.kv, "obs.strs", strs)
		setKV(st.kv, "obs.ints", ints)
	}
	return nil
}

// kvStrings is the list of strings at key in kv (a decoded row's pairs).
func kvStrings(kv []any, key string) []string {
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] == key {
			l, _ := kv[i+1].([]any)
			out := make([]string, 0, len(l))
			for _, v := range l {
				s, _ := v.(string)
				out = append(out, s)
			}
			return out
		}
	}
	return nil
}

// renderedKey is a row step's record of the version each of its events'
// observations were rendered in (batchObs). The relay validates known event
// shapes on every row and uses this marker to avoid re-rendering observations
// already produced by this release or a newer one. It drops the key before recording.
const renderedKey = "grist.rendered"

// renderedNewest reports whether a row's events of types were rendered
// in versions (renderedKey's) at least a's newest: no re-rendering needed. A row without the key (an older grist's) is not.
func (a *App) renderedNewest(types []string, versions []int64) bool {
	if len(versions) != len(types) {
		return false
	}
	for i, t := range types {
		if info := a.events[t]; info != nil && versions[i] < int64(info.version) {
			return false
		}
	}
	return true
}

// kvInts is the list of integers at key in kv (a decoded row's pairs:
// json.Numbers, as decodeRow decodes them).
func kvInts(kv []any, key string) []int64 {
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] == key {
			l, _ := kv[i+1].([]any)
			out := make([]int64, 0, len(l))
			for _, v := range l {
				switch n := v.(type) {
				case json.Number:
					if k, err := n.Int64(); err == nil {
						out = append(out, k)
					}
				case float64:
					out = append(out, int64(n))
				case int64:
					out = append(out, n)
				case int:
					out = append(out, int64(n))
				}
			}
			return out
		}
	}
	return nil
}

// dropKV is kv without key.
func dropKV(kv []any, key string) []any {
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] == key {
			return append(kv[:i:i], kv[i+2:]...)
		}
	}
	return kv
}

// setKV sets key's value in kv.
func setKV(kv []any, key string, v any) {
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] == key {
			kv[i+1] = v
			return
		}
	}
}

func uniq(s []string) []string {
	var out []string
	for _, x := range s {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}
