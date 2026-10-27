// Package storetest is grist's store contract suite: what the model
// takes for granted of every store tier (docs/grist.md §2.10, §3.3).
// Dense positions, conditions honoured and reads answered (each checked
// against store.CheckCondition and store.Select, brute force over every
// event, on small random histories), checkpoints' compare-and-set,
// markers (a commit's receipt, a refusal's outcome), concurrent appends
// replayed against the reference evaluation, Wait woken by an append,
// readers and writers sharing no slice with the store, the step log of
// a store.StepLog (its rows timed in commit order), a store.TenantFeed's
// wait for a new tenant, and tenant isolation: a read of one tenant never
// returns another's events, an append to one never moves another's head.
//
// Each check is a function of a context and a fresh store returning an
// error, so a harness can run it where the store needs (inside a qenv
// task for grist/simstore: its concurrent checks start qenv tasks, and
// outside a simulation goroutines), and a test can show that a check sees
// a broken store (a mutant that ignores the tenant in Read fails
// TenantIsolation).
package storetest

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// A Check is one property of the contract.
type Check struct {
	Name string
	Run  func(ctx context.Context, s store.Store) error
}

// Checks are the contract's properties, in order: a fresh list on each
// call.
func Checks() []Check {
	return []Check{
		{"DensePositions", DensePositions},
		{"ConditionsAgainstBruteForce", ConditionsAgainstBruteForce},
		{"ReadsAgainstBruteForce", ReadsAgainstBruteForce},
		{"Checkpoints", Checkpoints},
		{"Markers", Markers},
		{"ConcurrentAgainstBruteForce", ConcurrentAgainstBruteForce},
		{"WaitWakes", WaitWakes},
		{"NoSharedSlices", NoSharedSlices},
		{"StepLog", StepLog},
		{"TenantFeed", TenantFeed},
		{"TenantIsolation", TenantIsolation},
		{"UnknownTenant", UnknownTenant},
	}
}

// A Harness runs body against a fresh store and reports its error on t.
type Harness func(t *testing.T, body func(ctx context.Context, s store.Store) error)

// Run runs every check as a subtest, each on a fresh store from h.
func Run(t *testing.T, h Harness) {
	for _, c := range Checks() {
		t.Run(c.Name, func(t *testing.T) {
			h(t, func(ctx context.Context, s store.Store) error { return c.Run(ctx, s) })
		})
	}
}

// types and values are a random history's alphabet: small, so that
// queries and conditions collide.
var (
	types  = []string{"a-happened", "b-happened"} // read-only
	values = []string{"x", "y", "z"}              // read-only
)

func randEvent(r *rand.Rand) store.Event {
	tags := []store.TagPair{{Key: "k", Value: values[r.IntN(len(values))]}}
	if r.IntN(2) == 0 {
		tags = append(tags, store.TagPair{Key: "j", Value: values[r.IntN(len(values))]})
	}
	n := r.IntN(100)
	return store.Event{Type: types[r.IntN(len(types))], Version: 1, Tags: tags, Body: []byte(fmt.Sprintf(`{"n":%d}`, n))}
}

func randQuery(r *rand.Rand, head store.Position) store.Selector {
	q := store.Selector{Types: []string{types[r.IntN(len(types))]}}
	if r.IntN(3) == 0 {
		q.Types = types
	}
	switch r.IntN(4) {
	case 0:
		q.Tags = []store.TagPair{{Key: "k", Value: values[r.IntN(len(values))]}}
	case 1:
		q.Tags = []store.TagPair{{Key: "j", Value: values[r.IntN(len(values))]}}
	case 2: // both: a store indexing one tag must check the other on the events
		q.Tags = []store.TagPair{{Key: "k", Value: values[r.IntN(len(values))]}, {Key: "j", Value: values[r.IntN(len(values))]}}
	}
	if r.IntN(3) == 0 && head > 0 {
		q.After = store.Position(r.Int64N(int64(head)))
	}
	q.Reverse = r.IntN(2) == 0
	if r.IntN(2) == 0 {
		q.Limit = 1 + r.IntN(3)
	}
	return q
}

// appendAll appends events one batch at a time, unconditioned, and
// returns the log as stored.
func appendAll(ctx context.Context, s store.Store, tenant string, batches [][]store.Event) error {
	for _, b := range batches {
		if _, err := s.Append(ctx, tenant, store.Batch{Events: b}); err != nil {
			return err
		}
	}
	return nil
}

// stored are events as the store would hold them from position from+1:
// the reference log's entries.
func stored(tenant string, from store.Position, events []store.Event, time int64, cause string) []store.Stored {
	out := make([]store.Stored, len(events))
	for i, e := range events {
		out[i] = store.Stored{Event: e, Tenant: tenant, Pos: from + store.Position(i+1), Time: time, Cause: cause}
	}
	return out
}

func same(got, want []store.Stored) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].Pos != want[i].Pos || got[i].Tenant != want[i].Tenant || got[i].Type != want[i].Type ||
			string(got[i].Body) != string(want[i].Body) || !reflect.DeepEqual(got[i].Tags, want[i].Tags) ||
			got[i].Time != want[i].Time || got[i].Cause != want[i].Cause {
			return false
		}
	}
	return true
}

func positions(evs []store.Stored) []store.Position {
	out := []store.Position{}
	for _, e := range evs {
		out = append(out, e.Pos)
	}
	return out
}

// DensePositions: batches are appended at the next positions, 1, 2, ...;
// Append returns the head after each, and a read through the head returns
// every event in order with its envelope.
func DensePositions(ctx context.Context, s store.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	r := rand.New(rand.NewPCG(1, 2))
	var want []store.Stored
	for i := 0; i < 6; i++ {
		batch := make([]store.Event, r.IntN(3))
		for j := range batch {
			batch[j] = randEvent(r)
		}
		head, err := s.Append(ctx, "t1", store.Batch{Events: batch, Time: int64(1000 + i), Cause: fmt.Sprint("c", i)})
		if err != nil {
			return err
		}
		want = append(want, stored("t1", store.Position(len(want)), batch, int64(1000+i), fmt.Sprint("c", i))...)
		if head != store.Position(len(want)) {
			return fmt.Errorf("dense positions: batch %d of %d events: head %d, want %d", i, len(batch), head, len(want))
		}
	}
	got, at, err := s.Read(ctx, "t1", store.Selector{Types: types}, 0)
	if err != nil {
		return err
	}
	if at != store.Position(len(want)) || !same(got, want) {
		return fmt.Errorf("dense positions: a read through the head returned %v through %d; want %v through %d", positions(got), at, positions(want), len(want))
	}
	heads, err := s.Heads(ctx)
	if err != nil {
		return err
	}
	if heads["t1"] != store.Position(len(want)) {
		return fmt.Errorf("dense positions: Heads says %d, the appends %d", heads["t1"], len(want))
	}
	return s.Wait(ctx, "t1", heads["t1"]-1) // past it already: returns at once
}

// ConditionsAgainstBruteForce: on random histories, an append with a
// random condition (selectors of one or two types and none, one or two
// tags) commits exactly when store.CheckCondition over the whole log
// says it holds, and a failed one names the event that broke it.
func ConditionsAgainstBruteForce(ctx context.Context, s store.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	r := rand.New(rand.NewPCG(3, 4))
	var log []store.Stored
	committed, refused, twoCommitted, twoRefused := 0, 0, 0, 0
	for i := 0; i < 120; i++ {
		head := store.Position(len(log))
		var c store.Condition
		if head > 0 {
			c.After = store.Position(r.Int64N(int64(head) + 1))
		}
		for n := r.IntN(3) + 1; n > 0; n-- {
			q := randQuery(r, head) // one or two types, no tag, one or two
			q.After, q.Reverse, q.Limit = 0, false, 0
			c.Selectors = append(c.Selectors, q)
		}
		batch := []store.Event{randEvent(r)}
		want := store.CheckCondition(log, c)
		if twoTags(c.Selectors) {
			if want == nil {
				twoCommitted++
			} else {
				twoRefused++
			}
		}
		_, err := s.Append(ctx, "t1", store.Batch{Events: batch, Condition: &c})
		var cf, wcf *store.ConditionFailed
		switch {
		case want == nil && err != nil:
			return fmt.Errorf("conditions: append %d after %d with %v: %v, but no event after %d matches", i, c.After, c.Selectors, err, c.After)
		case want != nil && err == nil:
			errors.As(want, &wcf)
			return fmt.Errorf("conditions: append %d after %d with %v committed, but event %d matches %s", i, c.After, c.Selectors, wcf.Event, wcf.Selector)
		case want != nil && !errors.As(err, &cf):
			return fmt.Errorf("conditions: append %d: %v, not a *store.ConditionFailed", i, err)
		case want != nil:
			errors.As(want, &wcf)
			if cf.Event <= c.After || !cf.Selector.Matches(log[cf.Event-1].Event) {
				return fmt.Errorf("conditions: append %d failed naming event %d and %s, which does not break the condition after %d (the first that does: %d)", i, cf.Event, cf.Selector, c.After, wcf.Event)
			}
			refused++
		default:
			log = append(log, stored("t1", head, batch, 0, "")...)
			committed++
		}
	}
	if committed < 10 || refused < 10 {
		return fmt.Errorf("conditions: %d committed and %d refused of 120: the random histories test too little of either", committed, refused)
	}
	if twoCommitted < 3 || twoRefused < 3 {
		return fmt.Errorf("conditions: %d committed and %d refused with a selector of two tags: too few to test them", twoCommitted, twoRefused)
	}
	return nil
}

// twoTags reports whether one of sels has two tags.
func twoTags(sels []store.Selector) bool {
	for _, q := range sels {
		if len(q.Tags) > 1 {
			return true
		}
	}
	return false
}

// ReadsAgainstBruteForce: on a random history, every random query (one or
// two types; none, one or two tags; after, reverse, limit) through a
// random position returns what store.Select over the whole log returns.
func ReadsAgainstBruteForce(ctx context.Context, s store.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	r := rand.New(rand.NewPCG(5, 6))
	var log []store.Stored
	for i := 0; i < 30; i++ {
		e := randEvent(r)
		if _, err := s.Append(ctx, "t1", store.Batch{Events: []store.Event{e}}); err != nil {
			return err
		}
		log = append(log, stored("t1", store.Position(len(log)), []store.Event{e}, 0, "")...)
	}
	twoFound := 0
	for i := 0; i < 200; i++ {
		q := randQuery(r, store.Position(len(log)))
		at := store.Position(r.IntN(len(log) + 1)) // 0: the head
		got, through, err := s.Read(ctx, "t1", q, at)
		if err != nil {
			return err
		}
		wantAt := at
		if at == 0 {
			wantAt = store.Position(len(log))
		}
		if want := store.Select(log, q, wantAt); through != wantAt || !same(got, want) {
			return fmt.Errorf("reads: %s after %d (reverse %v, limit %d) through %d: got %v through %d, want %v", q, q.After, q.Reverse, q.Limit, at, positions(got), through, positions(want))
		} else if len(q.Tags) > 1 && len(want) > 0 {
			twoFound++
		}
	}
	if twoFound < 5 {
		return fmt.Errorf("reads: %d queries of two tags found events: too few to test them", twoFound)
	}
	return nil
}

// Checkpoints: a checkpoint moves only from where it is (a
// compare-and-set), alone or with events, and Checkpoint reads it.
func Checkpoints(ctx context.Context, s store.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	if _, err := s.Append(ctx, "t1", store.Batch{Checkpoint: &store.CheckpointMove{Processor: "p", From: 0, To: 3}}); err != nil {
		return fmt.Errorf("checkpoints: moving p from 0 to 3: %v", err)
	}
	_, err := s.Append(ctx, "t1", store.Batch{Events: []store.Event{{Type: "a-happened", Version: 1, Body: []byte(`{}`)}},
		Checkpoint: &store.CheckpointMove{Processor: "p", From: 0, To: 5}})
	var cm *store.CheckpointMoved
	if !errors.As(err, &cm) || cm.Found != 3 || cm.Expected != 0 {
		return fmt.Errorf("checkpoints: moving p from 0 when it is at 3: %v, want a *store.CheckpointMoved found at 3", err)
	}
	if h, _ := s.Heads(ctx); h["t1"] != 0 {
		return fmt.Errorf("checkpoints: a refused move appended its events (head %d)", h["t1"])
	}
	if _, err := s.Append(ctx, "t1", store.Batch{Events: []store.Event{{Type: "a-happened", Version: 1, Body: []byte(`{}`)}},
		Checkpoint: &store.CheckpointMove{Processor: "p", From: 3, To: 4}}); err != nil {
		return fmt.Errorf("checkpoints: moving p from 3 to 4 with an event: %v", err)
	}
	for p, want := range map[string]store.Position{"p": 4, "q": 0} {
		got, err := s.Checkpoint(ctx, "t1", p)
		if err != nil || got != want {
			return fmt.Errorf("checkpoints: %s is %d (%v), want %d", p, got, err, want)
		}
	}
	return nil
}

// Markers: a batch's marker is written with its receipt, and a second
// batch with the same id fails, naming it, and appends nothing.
func Markers(ctx context.Context, s store.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	e := store.Event{Type: "a-happened", Version: 1, Body: []byte(`{}`)}
	head, err := s.Append(ctx, "t1", store.Batch{Events: []store.Event{e, e}, Marker: &store.Marker{ID: "cmd-1", Hash: "h1"}})
	if err != nil {
		return err
	}
	m, ok, err := s.Marker(ctx, "t1", "cmd-1")
	want := store.NewReceipt("t1", head, 2)
	if err != nil || !ok || m.Hash != "h1" || !reflect.DeepEqual(m.Receipt, want) {
		return fmt.Errorf("markers: cmd-1 is %+v (found %v, %v), want hash h1 and receipt %+v", m, ok, err, want)
	}
	_, err = s.Append(ctx, "t1", store.Batch{Events: []store.Event{e}, Marker: &store.Marker{ID: "cmd-1", Hash: "h2"}})
	var cf *store.ConditionFailed
	if !errors.As(err, &cf) || cf.Marker != "cmd-1" {
		return fmt.Errorf("markers: a second batch with id cmd-1: %v, want a *store.ConditionFailed naming the marker", err)
	}
	if h, _ := s.Heads(ctx); h["t1"] != head {
		return fmt.Errorf("markers: the refused batch moved the head to %d", h["t1"])
	}
	if _, ok, _ := s.Marker(ctx, "t1", "cmd-2"); ok {
		return fmt.Errorf("markers: cmd-2 found, never written")
	}
	// a refusal's marker: no events, its outcome kept, the key taken
	if _, err := s.Append(ctx, "t1", store.Batch{Marker: &store.Marker{ID: "cmd-3", Hash: "h3", Outcome: "name-taken"}}); err != nil {
		return fmt.Errorf("markers: a refusal's marker alone: %v", err)
	}
	m, ok, err = s.Marker(ctx, "t1", "cmd-3")
	if err != nil || !ok || m.Outcome != "name-taken" || m.Hash != "h3" || m.Receipt.Pos != head {
		return fmt.Errorf("markers: cmd-3 is %+v (found %v, %v), want outcome name-taken, hash h3, at head %d", m, ok, err, head)
	}
	_, err = s.Append(ctx, "t1", store.Batch{Events: []store.Event{e}, Marker: &store.Marker{ID: "cmd-3", Hash: "h3"}})
	if !errors.As(err, &cf) || cf.Marker != "cmd-3" {
		return fmt.Errorf("markers: a commit under a refused key: %v, want a *store.ConditionFailed naming the marker (a key's first answer is final)", err)
	}
	return nil
}

// ConcurrentAgainstBruteForce: tasks append at once (qenv.Go: a
// simulation's tasks, goroutines outside one), each read of the head
// followed by a pause, then an append conditioned after it, half of them
// moving a checkpoint from where the task read it, every one under its
// own marker; then the committed log is replayed. Positions are dense,
// each committed batch's condition held over the log before its events
// (store.CheckCondition) and its checkpoint moved from the previous
// commit's, each refusal names an event that breaks its condition or a
// checkpoint that moved, and an append whose answer was transient is
// committed exactly when its marker exists.
func ConcurrentAgainstBruteForce(ctx context.Context, s store.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	const tasks, appends = 4, 12
	type attempt struct {
		id       string
		cond     store.Condition
		move     *store.CheckpointMove
		event    store.Event
		head     store.Position
		err      error
		readFail error
	}
	results := make([][]attempt, tasks)
	var done atomic.Int32
	for i := range tasks {
		qenv.Go(ctx, fmt.Sprintf("storetest.appender%d", i), func(ctx context.Context) {
			defer done.Add(1)
			r := rand.New(rand.NewPCG(9, uint64(i)))
			for j := 0; j < appends; j++ {
				a := attempt{id: fmt.Sprintf("a%d-%d", i, j), event: randEvent(r)}
				_, head, err := s.Read(ctx, "t1", store.Selector{}, 0)
				cp, cerr := s.Checkpoint(ctx, "t1", "p")
				if err == nil {
					err = cerr
				}
				if err != nil {
					a.readFail = err
					results[i] = append(results[i], a)
					continue
				}
				q := randQuery(r, head)
				q.After, q.Reverse, q.Limit = 0, false, 0
				if len(q.Tags) > 1 {
					q.Tags = q.Tags[:1]
				}
				a.cond = store.Condition{After: head, Selectors: []store.Selector{q}}
				if j%2 == 0 {
					a.move = &store.CheckpointMove{Processor: "p", From: cp, To: cp + 1}
				}
				qenv.Sleep(ctx, time.Duration(r.IntN(3))*time.Millisecond) // a decision's think time: others commit meanwhile
				a.head, a.err = s.Append(ctx, "t1", store.Batch{Events: []store.Event{a.event}, Condition: &a.cond, Checkpoint: a.move,
					Marker: &store.Marker{ID: a.id, Hash: a.id}})
				results[i] = append(results[i], a)
			}
		})
	}
	if !qenv.WaitFor(ctx, func() bool { return done.Load() == tasks }, time.Minute) {
		return fmt.Errorf("concurrent: the appenders did not finish within a minute")
	}
	log, _, err := s.Read(ctx, "t1", store.Selector{Types: types}, 0)
	if err != nil {
		return err
	}
	for i, e := range log {
		if e.Pos != store.Position(i+1) {
			return fmt.Errorf("concurrent: the log's %d-th event is at %d: positions are not dense", i+1, e.Pos)
		}
	}
	byHead := map[store.Position]attempt{}
	committed, refused := 0, 0
	for _, rs := range results {
		for _, a := range rs {
			if a.readFail != nil {
				continue
			}
			var cf *store.ConditionFailed
			var cm *store.CheckpointMoved
			switch {
			case a.err == nil:
			case errors.As(a.err, &cf):
				if cf.Event <= a.cond.After || int(cf.Event) > len(log) || !cf.Selector.Matches(log[cf.Event-1].Event) {
					return fmt.Errorf("concurrent: %s refused naming event %d and %s, which does not break its condition after %d", a.id, cf.Event, cf.Selector, a.cond.After)
				}
				refused++
				continue
			case errors.As(a.err, &cm):
				if a.move == nil || cm.Found == a.move.From {
					return fmt.Errorf("concurrent: %s refused as a moved checkpoint (%v), but it moved none or found it where it read it", a.id, a.err)
				}
				refused++
				continue
			default: // transient: committed exactly when its marker exists
				m, ok, merr := s.Marker(ctx, "t1", a.id)
				if merr != nil {
					return merr
				}
				if !ok {
					continue
				}
				a.head = m.Receipt.Pos
			}
			if prev, dup := byHead[a.head]; dup {
				return fmt.Errorf("concurrent: %s and %s both committed ending at %d", prev.id, a.id, a.head)
			}
			byHead[a.head] = a
			committed++
		}
	}
	if committed != len(log) {
		return fmt.Errorf("concurrent: %d appends committed one event each, the log has %d", committed, len(log))
	}
	var cp store.Position
	for p := store.Position(1); int(p) <= len(log); p++ {
		a, ok := byHead[p]
		if !ok {
			return fmt.Errorf("concurrent: event %d is no committed append's", p)
		}
		if got := log[p-1]; got.Type != a.event.Type || string(got.Body) != string(a.event.Body) {
			return fmt.Errorf("concurrent: event %d is %s %s, but %s appended %s %s there", p, got.Type, got.Body, a.id, a.event.Type, a.event.Body)
		}
		if err := store.CheckCondition(log[:p-1], a.cond); err != nil {
			return fmt.Errorf("concurrent: %s committed at %d, but its condition after %d did not hold: %v", a.id, p, a.cond.After, err)
		}
		if a.move != nil {
			if a.move.From != cp {
				return fmt.Errorf("concurrent: %s committed at %d moving the checkpoint from %d, where it was %d", a.id, p, a.move.From, cp)
			}
			cp = a.move.To
		}
	}
	if got, err := s.Checkpoint(ctx, "t1", "p"); err != nil || got != cp {
		return fmt.Errorf("concurrent: the checkpoint is %d (%v), the committed moves end at %d", got, err, cp)
	}
	if committed < tasks || refused == 0 {
		return fmt.Errorf("concurrent: %d committed and %d refused of %d: the appenders raced too little to test the lock", committed, refused, tasks*appends)
	}
	return nil
}

// WaitWakes: Wait returns at once for a head already past, and a waiter
// on the head returns once an append moves it, not before.
func WaitWakes(ctx context.Context, s store.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	var woke, waiting atomic.Bool
	qenv.Go(ctx, "storetest.waiter", func(ctx context.Context) {
		waiting.Store(true)
		if s.Wait(ctx, "t1", 0) == nil {
			woke.Store(true)
		}
	})
	if !qenv.WaitFor(ctx, waiting.Load, 10*time.Second) {
		return fmt.Errorf("wait: the waiter never started")
	}
	qenv.Sleep(ctx, 50*time.Millisecond)
	if woke.Load() {
		return fmt.Errorf("wait: Wait(t1, 0) returned on an empty tenant, before any append")
	}
	if _, err := s.Append(ctx, "t1", store.Batch{Events: []store.Event{{Type: "a-happened", Version: 1, Body: []byte(`{}`)}}}); err != nil {
		return err
	}
	if !qenv.WaitFor(ctx, woke.Load, 10*time.Second) {
		return fmt.Errorf("wait: Wait(t1, 0) did not return after an append moved the head to 1")
	}
	return s.Wait(ctx, "t1", 0) // past already: returns at once
}

// NoSharedSlices: a reader that mutates an event's tags or body in place
// (a fold reusing its input), or a writer that reuses its batch's slices
// after the append, changes nothing the store holds.
func NoSharedSlices(ctx context.Context, s store.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	e := store.Event{Type: "a-happened", Version: 1, Tags: []store.TagPair{{Key: "k", Value: "x"}}, Body: []byte(`{"n":1}`)}
	if _, err := s.Append(ctx, "t1", store.Batch{Events: []store.Event{e}}); err != nil {
		return err
	}
	e.Tags[0].Value, e.Body[1] = "w", 'W' // the writer reuses its slices
	got, _, err := s.Read(ctx, "t1", store.Selector{Types: types}, 0)
	if err != nil || len(got) != 1 {
		return fmt.Errorf("no shared slices: read %d events (%v), want 1", len(got), err)
	}
	if got[0].Tags[0].Value != "x" || string(got[0].Body) != `{"n":1}` {
		return fmt.Errorf("no shared slices: the writer's change after its append shows in the store: tags %v body %s", got[0].Tags, got[0].Body)
	}
	got[0].Tags[0].Value, got[0].Body[1] = "r", 'R' // a fold mutating its input
	again, _, err := s.Read(ctx, "t1", store.Selector{Types: types, Tags: []store.TagPair{{Key: "k", Value: "x"}}}, 0)
	if err != nil || len(again) != 1 || again[0].Tags[0].Value != "x" || string(again[0].Body) != `{"n":1}` {
		return fmt.Errorf("no shared slices: a reader's change to its copy shows in the store: %v (%v)", again, err)
	}
	return nil
}

// StepLog: the store is a store.StepLog (grist records every commit's
// step from it), keeping each committed batch's step, in commit order
// with the head after it, and none of a refused batch; and WaitSteps
// wakes for a refusal's step, which commits its marker without moving
// the head (a WaitSteps that waits on the head sleeps through it).
func StepLog(ctx context.Context, s store.Store) error {
	sl, ok := s.(store.StepLog)
	if !ok {
		return fmt.Errorf("step log: %T is no store.StepLog: grist records every commit's step from the step rows it keeps in the commit's transaction (Steps, WaitSteps)", s)
	}
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	e := store.Event{Type: "a-happened", Version: 1, Body: []byte(`{}`)}
	var want []store.StepRow
	for i, b := range []store.Batch{
		{Events: []store.Event{e}, Marker: &store.Marker{ID: "m1"}, Step: []byte(`{"n":1}`)},
		{Events: []store.Event{e}, Marker: &store.Marker{ID: "m1"}, Step: []byte(`{"n":2}`)},                                          // refused: the marker
		{Events: []store.Event{e}, Condition: &store.Condition{Selectors: []store.Selector{{Types: types}}}, Step: []byte(`{"n":3}`)}, // refused: the condition
		{Marker: &store.Marker{ID: "m2", Outcome: "no"}, Step: []byte(`{"n":4}`)},
		{Events: []store.Event{e, e}, Step: []byte(`{"n":5}`)},
		{Events: []store.Event{e}}, // no step
	} {
		head, err := s.Append(ctx, "t1", b)
		var cf *store.ConditionFailed
		switch {
		case err == nil && b.Step != nil:
			want = append(want, store.StepRow{Seq: int64(len(want) + 1), Head: head, Step: b.Step})
		case err != nil && !errors.As(err, &cf):
			return fmt.Errorf("step log: batch %d: %v", i+1, err)
		}
	}
	got, err := sl.Steps(ctx, "t1", 0, 0)
	if err != nil {
		return err
	}
	if len(got) != len(want) {
		return fmt.Errorf("step log: %d rows, want %d (the committed batches' steps)", len(got), len(want))
	}
	for i := range got {
		if got[i].Seq != want[i].Seq || got[i].Head != want[i].Head || string(got[i].Step) != string(want[i].Step) {
			return fmt.Errorf("step log: row %d is %d at %d %s, want %d at %d %s", i+1, got[i].Seq, got[i].Head, got[i].Step, want[i].Seq, want[i].Head, want[i].Step)
		}
		if got[i].Time == 0 || i > 0 && got[i].Time < got[i-1].Time {
			return fmt.Errorf("step log: row %d's time is %d after row %d's %d: a row is timed under the tenant's lock (store.StepTime), so its times are in commit order", i+1, got[i].Time, i, got[max(i-1, 0)].Time)
		}
	}
	if n, err := sl.StepCount(ctx, "t1"); err != nil || n != int64(len(want)) {
		return fmt.Errorf("step log: StepCount is %d (%v), the log has %d rows", n, err, len(want))
	}
	if _, err := sl.StepCount(ctx, "nobody"); !errors.Is(err, store.ErrUnknownTenant) {
		return fmt.Errorf("step log: StepCount of a tenant never created: %v, want store.ErrUnknownTenant", err)
	}
	if after, _ := sl.Steps(ctx, "t1", 1, 1); len(after) != 1 || after[0].Seq != 2 {
		return fmt.Errorf("step log: the rows after 1, at most 1: %v, want row 2", after)
	}
	if err := sl.WaitSteps(ctx, "t1", int64(len(want)-1)); err != nil { // past it already: returns at once
		return fmt.Errorf("step log: WaitSteps after %d with %d rows: %v", len(want)-1, len(want), err)
	}
	// a waiter on the last row, woken by a refusal: its marker and its
	// step commit, the head stays
	var woke, waiting atomic.Bool
	last := int64(len(want))
	qenv.Go(ctx, "storetest.stepwaiter", func(ctx context.Context) {
		waiting.Store(true)
		if sl.WaitSteps(ctx, "t1", last) == nil {
			woke.Store(true)
		}
	})
	if !qenv.WaitFor(ctx, waiting.Load, 10*time.Second) {
		return fmt.Errorf("step log: the waiter never started")
	}
	qenv.Sleep(ctx, 50*time.Millisecond)
	if woke.Load() {
		return fmt.Errorf("step log: WaitSteps(t1, %d) returned with %d rows, before any commit", last, last)
	}
	if _, err := s.Append(ctx, "t1", store.Batch{Marker: &store.Marker{ID: "m3", Outcome: "no"}, Step: []byte(`{"n":6}`)}); err != nil {
		return err
	}
	if !qenv.WaitFor(ctx, woke.Load, 10*time.Second) {
		return fmt.Errorf("step log: WaitSteps(t1, %d) did not return after a refusal committed its step (the head did not move: a wait on the head sleeps through it)", last)
	}
	return nil
}

// TenantFeed: the store is a store.TenantFeed (a node relays and handles
// a tenant created after it started), whose WaitTenants returns at once
// when the store has more tenants than it was told, and otherwise once a
// tenant is created, not before.
func TenantFeed(ctx context.Context, s store.Store) error {
	tf, ok := s.(store.TenantFeed)
	if !ok {
		return fmt.Errorf("tenant feed: %T is no store.TenantFeed: a node waits on WaitTenants for tenants created after it started", s)
	}
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	if err := tf.WaitTenants(ctx, 0); err != nil { // one tenant already: at once
		return fmt.Errorf("tenant feed: WaitTenants(0) with one tenant: %v", err)
	}
	var woke, waiting atomic.Bool
	qenv.Go(ctx, "storetest.tenantwaiter", func(ctx context.Context) {
		waiting.Store(true)
		if tf.WaitTenants(ctx, 1) == nil {
			woke.Store(true)
		}
	})
	if !qenv.WaitFor(ctx, waiting.Load, 10*time.Second) {
		return fmt.Errorf("tenant feed: the waiter never started")
	}
	qenv.Sleep(ctx, 50*time.Millisecond)
	if _, err := s.Append(ctx, "t1", store.Batch{Events: []store.Event{{Type: "a-happened", Version: 1, Body: []byte(`{}`)}}}); err != nil {
		return err
	}
	if err := s.CreateTenant(ctx, "t1"); err != nil { // exists: no new tenant
		return err
	}
	qenv.Sleep(ctx, 50*time.Millisecond)
	if woke.Load() {
		return fmt.Errorf("tenant feed: WaitTenants(1) returned with one tenant (after an append and a second create of t1)")
	}
	if err := s.CreateTenant(ctx, "t2"); err != nil {
		return err
	}
	if !qenv.WaitFor(ctx, woke.Load, 10*time.Second) {
		return fmt.Errorf("tenant feed: WaitTenants(1) did not return once t2 was created")
	}
	return nil
}

// TenantIsolation: a read of one tenant never returns another's events,
// and an append to one never moves another's head, checkpoints or
// markers.
func TenantIsolation(ctx context.Context, s store.Store) error {
	for _, t := range []string{"t1", "t2"} {
		if err := s.CreateTenant(ctx, t); err != nil {
			return err
		}
	}
	r := rand.New(rand.NewPCG(7, 8))
	var b1, b2 [][]store.Event
	for i := 0; i < 5; i++ {
		b1 = append(b1, []store.Event{randEvent(r), randEvent(r)})
	}
	b2 = append(b2, []store.Event{randEvent(r)})
	if err := appendAll(ctx, s, "t1", b1); err != nil {
		return err
	}
	if _, err := s.Append(ctx, "t1", store.Batch{Marker: &store.Marker{ID: "m"}, Checkpoint: &store.CheckpointMove{Processor: "p", To: 7}}); err != nil {
		return err
	}
	heads, err := s.Heads(ctx)
	if err != nil {
		return err
	}
	if heads["t2"] != 0 {
		return fmt.Errorf("tenant isolation: 10 events appended to t1 moved t2's head to %d", heads["t2"])
	}
	if err := appendAll(ctx, s, "t2", b2); err != nil {
		return err
	}
	for _, t := range []string{"t1", "t2"} {
		evs, _, err := s.Read(ctx, t, store.Selector{Types: types}, 0)
		if err != nil {
			return err
		}
		for _, e := range evs {
			if e.Tenant != t {
				return fmt.Errorf("tenant isolation: a read of %s returned %s's event %d", t, e.Tenant, e.Pos)
			}
		}
		if want := map[string]int{"t1": 10, "t2": 1}[t]; len(evs) != want {
			return fmt.Errorf("tenant isolation: a read of %s returned %d events, want %d (its own)", t, len(evs), want)
		}
	}
	if cp, _ := s.Checkpoint(ctx, "t2", "p"); cp != 0 {
		return fmt.Errorf("tenant isolation: t1's checkpoint of p shows in t2 (%d)", cp)
	}
	if _, ok, _ := s.Marker(ctx, "t2", "m"); ok {
		return fmt.Errorf("tenant isolation: t1's marker m shows in t2")
	}
	return nil
}

// UnknownTenant: a tenant never created is refused with
// store.ErrUnknownTenant, by reads and appends alike.
func UnknownTenant(ctx context.Context, s store.Store) error {
	if _, _, err := s.Read(ctx, "nobody", store.Selector{Types: types}, 0); !errors.Is(err, store.ErrUnknownTenant) {
		return fmt.Errorf("unknown tenant: a read: %v, want store.ErrUnknownTenant", err)
	}
	if _, err := s.Append(ctx, "nobody", store.Batch{}); !errors.Is(err, store.ErrUnknownTenant) {
		return fmt.Errorf("unknown tenant: an append: %v, want store.ErrUnknownTenant", err)
	}
	return nil
}

// TwoStores: a and b are two stores over one database (two nodes, or two
// Stores a process opened), and each one's waiters wake for the other's
// commits: Wait for an append, WaitSteps for a refusal's step, and
// WaitTenants for a tenant, each made through the other store. A store
// that wakes its waiters only at its own commits (and polls nothing in a
// simulation) sleeps through them. It is no Check: it needs two stores,
// which a harness opens.
func TwoStores(ctx context.Context, a, b store.Store) error {
	if err := a.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	al, ok := a.(store.StepLog)
	if !ok {
		return fmt.Errorf("two stores: %T is no store.StepLog", a)
	}
	af, ok := a.(store.TenantFeed)
	if !ok {
		return fmt.Errorf("two stores: %T is no store.TenantFeed", a)
	}
	steps, err := al.StepCount(ctx, "t1")
	if err != nil {
		return err
	}
	ts, err := a.Tenants(ctx)
	if err != nil {
		return err
	}
	var head, step, tenant, waiting atomic.Int32
	qenv.Go(ctx, "storetest.headwaiter", func(ctx context.Context) {
		waiting.Add(1)
		if a.Wait(ctx, "t1", 0) == nil {
			head.Store(1)
		}
	})
	qenv.Go(ctx, "storetest.stepwaiter", func(ctx context.Context) {
		waiting.Add(1)
		if al.WaitSteps(ctx, "t1", steps+1) == nil { // the append's row, then the refusal's
			step.Store(1)
		}
	})
	qenv.Go(ctx, "storetest.tenantwaiter", func(ctx context.Context) {
		waiting.Add(1)
		if af.WaitTenants(ctx, len(ts)) == nil {
			tenant.Store(1)
		}
	})
	if !qenv.WaitFor(ctx, func() bool { return waiting.Load() == 3 }, 10*time.Second) {
		return fmt.Errorf("two stores: the waiters never started")
	}
	qenv.Sleep(ctx, 10*time.Millisecond)
	if _, err := b.Append(ctx, "t1", store.Batch{Events: []store.Event{{Type: "a-happened", Version: 1, Body: []byte(`{}`)}}, Step: []byte(`{"n":1}`)}); err != nil {
		return err
	}
	if !qenv.WaitFor(ctx, func() bool { return head.Load() == 1 }, 10*time.Second) {
		return fmt.Errorf("two stores: a's Wait(t1, 0) did not return after b's append moved the head")
	}
	if _, err := b.Append(ctx, "t1", store.Batch{Marker: &store.Marker{ID: "m", Outcome: "no"}, Step: []byte(`{"n":2}`)}); err != nil {
		return err
	}
	if !qenv.WaitFor(ctx, func() bool { return step.Load() == 1 }, 10*time.Second) {
		return fmt.Errorf("two stores: a's WaitSteps did not return after b committed a refusal's step")
	}
	if err := b.CreateTenant(ctx, "t2"); err != nil {
		return err
	}
	if !qenv.WaitFor(ctx, func() bool { return tenant.Load() == 1 }, 10*time.Second) {
		return fmt.Errorf("two stores: a's WaitTenants did not return after b created t2")
	}
	return nil
}
