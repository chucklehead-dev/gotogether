// Package storetest is grist's store contract suite: what the model
// takes for granted of every store tier (docs/grist.md §2.10, §3.3).
// Dense positions, conditions honoured and reads answered (each checked
// against grist.CheckCondition and grist.Select, brute force over every
// event, on small random histories), checkpoints' compare-and-set,
// markers, and tenant isolation: a read of one tenant never returns
// another's events, an append to one never moves another's head.
//
// Each check is a function of a context and a fresh store returning an
// error, so a harness can run it where the store needs (inside a qenv
// task for grist/simstore), and a test can show that a check sees a
// broken store (a mutant that ignores the tenant in Read fails
// TenantIsolation).
package storetest

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
)

// A Check is one property of the contract.
type Check struct {
	Name string
	Run  func(ctx context.Context, s grist.Store) error
}

// Checks are the contract's properties, in order. Read-only.
var Checks = []Check{
	{"DensePositions", DensePositions},
	{"ConditionsAgainstBruteForce", ConditionsAgainstBruteForce},
	{"ReadsAgainstBruteForce", ReadsAgainstBruteForce},
	{"Checkpoints", Checkpoints},
	{"Markers", Markers},
	{"TenantIsolation", TenantIsolation},
	{"UnknownTenant", UnknownTenant},
}

// A Harness runs body against a fresh store and reports its error on t.
type Harness func(t *testing.T, body func(ctx context.Context, s grist.Store) error)

// Run runs every check as a subtest, each on a fresh store from h.
func Run(t *testing.T, h Harness) {
	for _, c := range Checks {
		t.Run(c.Name, func(t *testing.T) {
			h(t, func(ctx context.Context, s grist.Store) error { return c.Run(ctx, s) })
		})
	}
}

// types and values are a random history's alphabet: small, so that
// queries and conditions collide.
var (
	types  = []string{"a-happened", "b-happened"} // read-only
	values = []string{"x", "y", "z"}              // read-only
)

func randEvent(r *rand.Rand) grist.Event {
	tags := []grist.TagPair{{Key: "k", Value: values[r.IntN(len(values))]}}
	if r.IntN(2) == 0 {
		tags = append(tags, grist.TagPair{Key: "j", Value: values[r.IntN(len(values))]})
	}
	n := r.IntN(100)
	return grist.Event{Type: types[r.IntN(len(types))], Version: 1, Tags: tags, Body: []byte(fmt.Sprintf(`{"n":%d}`, n))}
}

func randQuery(r *rand.Rand, head grist.Position) grist.Query {
	q := grist.Query{Types: []string{types[r.IntN(len(types))]}}
	if r.IntN(3) == 0 {
		q.Types = types
	}
	switch r.IntN(3) {
	case 0:
		q.Tags = []grist.TagPair{{Key: "k", Value: values[r.IntN(len(values))]}}
	case 1:
		q.Tags = []grist.TagPair{{Key: "j", Value: values[r.IntN(len(values))]}}
	}
	if r.IntN(3) == 0 && head > 0 {
		q.After = grist.Position(r.Int64N(int64(head)))
	}
	q.Reverse = r.IntN(2) == 0
	if r.IntN(2) == 0 {
		q.Limit = 1 + r.IntN(3)
	}
	return q
}

// appendAll appends events one batch at a time, unconditioned, and
// returns the log as stored.
func appendAll(ctx context.Context, s grist.Store, tenant string, batches [][]grist.Event) error {
	for _, b := range batches {
		if _, err := s.Append(ctx, tenant, grist.Batch{Events: b}); err != nil {
			return err
		}
	}
	return nil
}

// stored are events as the store would hold them from position from+1:
// the reference log's entries.
func stored(tenant string, from grist.Position, events []grist.Event, time int64, cause string) []grist.Stored {
	out := make([]grist.Stored, len(events))
	for i, e := range events {
		out[i] = grist.Stored{Event: e, Tenant: tenant, Pos: from + grist.Position(i+1), Time: time, Cause: cause}
	}
	return out
}

func same(got, want []grist.Stored) bool {
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

func positions(evs []grist.Stored) []grist.Position {
	out := []grist.Position{}
	for _, e := range evs {
		out = append(out, e.Pos)
	}
	return out
}

// DensePositions: batches are appended at the next positions, 1, 2, ...;
// Append returns the head after each, OnCommit runs once with it, and a
// read through the head returns every event in order with its envelope.
func DensePositions(ctx context.Context, s grist.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	r := rand.New(rand.NewPCG(1, 2))
	var want []grist.Stored
	for i := 0; i < 6; i++ {
		batch := make([]grist.Event, r.IntN(3))
		for j := range batch {
			batch[j] = randEvent(r)
		}
		hooks, hookHead := 0, grist.Position(-1)
		head, err := s.Append(ctx, "t1", grist.Batch{Events: batch, Time: int64(1000 + i), Cause: fmt.Sprint("c", i),
			OnCommit: func(h grist.Position) { hooks++; hookHead = h }})
		if err != nil {
			return err
		}
		want = append(want, stored("t1", grist.Position(len(want)), batch, int64(1000+i), fmt.Sprint("c", i))...)
		if head != grist.Position(len(want)) {
			return fmt.Errorf("dense positions: batch %d of %d events: head %d, want %d", i, len(batch), head, len(want))
		}
		if hooks != 1 || hookHead != head {
			return fmt.Errorf("dense positions: batch %d: OnCommit ran %d times with head %d; want once with %d", i, hooks, hookHead, head)
		}
	}
	got, at, err := s.Read(ctx, "t1", grist.Query{Types: types}, 0)
	if err != nil {
		return err
	}
	if at != grist.Position(len(want)) || !same(got, want) {
		return fmt.Errorf("dense positions: a read through the head returned %v through %d; want %v through %d", positions(got), at, positions(want), len(want))
	}
	heads, err := s.Heads(ctx)
	if err != nil {
		return err
	}
	if heads["t1"] != grist.Position(len(want)) {
		return fmt.Errorf("dense positions: Heads says %d, the appends %d", heads["t1"], len(want))
	}
	return s.Wait(ctx, "t1", heads["t1"]-1) // past it already: returns at once
}

// ConditionsAgainstBruteForce: on random histories, an append with a
// random condition commits exactly when grist.CheckCondition over the
// whole log says it holds, and a failed one names the event that broke
// it.
func ConditionsAgainstBruteForce(ctx context.Context, s grist.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	r := rand.New(rand.NewPCG(3, 4))
	var log []grist.Stored
	committed, refused := 0, 0
	for i := 0; i < 120; i++ {
		head := grist.Position(len(log))
		var c grist.Condition
		if head > 0 {
			c.After = grist.Position(r.Int64N(int64(head) + 1))
		}
		for n := r.IntN(3) + 1; n > 0; n-- {
			q := randQuery(r, head)
			q.After, q.Reverse, q.Limit = 0, false, 0
			if len(q.Tags) > 1 {
				q.Tags = q.Tags[:1]
			}
			c.Queries = append(c.Queries, q)
		}
		batch := []grist.Event{randEvent(r)}
		want := grist.CheckCondition(log, c)
		_, err := s.Append(ctx, "t1", grist.Batch{Events: batch, Condition: &c})
		var cf, wcf *grist.ConditionFailed
		switch {
		case want == nil && err != nil:
			return fmt.Errorf("conditions: append %d after %d with %v: %v, but no event after %d matches", i, c.After, c.Queries, err, c.After)
		case want != nil && err == nil:
			errors.As(want, &wcf)
			return fmt.Errorf("conditions: append %d after %d with %v committed, but event %d matches %s", i, c.After, c.Queries, wcf.Event, wcf.Query)
		case want != nil && !errors.As(err, &cf):
			return fmt.Errorf("conditions: append %d: %v, not a *grist.ConditionFailed", i, err)
		case want != nil:
			errors.As(want, &wcf)
			if cf.Event <= c.After || !cf.Query.Matches(log[cf.Event-1].Event) {
				return fmt.Errorf("conditions: append %d failed naming event %d and %s, which does not break the condition after %d (the first that does: %d)", i, cf.Event, cf.Query, c.After, wcf.Event)
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
	return nil
}

// ReadsAgainstBruteForce: on a random history, every random query (types,
// a tag, after, reverse, limit) through a random position returns what
// grist.Select over the whole log returns.
func ReadsAgainstBruteForce(ctx context.Context, s grist.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	r := rand.New(rand.NewPCG(5, 6))
	var log []grist.Stored
	for i := 0; i < 30; i++ {
		e := randEvent(r)
		if _, err := s.Append(ctx, "t1", grist.Batch{Events: []grist.Event{e}}); err != nil {
			return err
		}
		log = append(log, stored("t1", grist.Position(len(log)), []grist.Event{e}, 0, "")...)
	}
	for i := 0; i < 200; i++ {
		q := randQuery(r, grist.Position(len(log)))
		at := grist.Position(r.IntN(len(log) + 1)) // 0: the head
		got, through, err := s.Read(ctx, "t1", q, at)
		if err != nil {
			return err
		}
		wantAt := at
		if at == 0 {
			wantAt = grist.Position(len(log))
		}
		if want := grist.Select(log, q, wantAt); through != wantAt || !same(got, want) {
			return fmt.Errorf("reads: %s after %d (reverse %v, limit %d) through %d: got %v through %d, want %v", q, q.After, q.Reverse, q.Limit, at, positions(got), through, positions(want))
		}
	}
	return nil
}

// Checkpoints: a checkpoint moves only from where it is (a
// compare-and-set), alone or with events, and Checkpoint reads it.
func Checkpoints(ctx context.Context, s grist.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	if _, err := s.Append(ctx, "t1", grist.Batch{Checkpoint: &grist.CheckpointMove{Processor: "p", From: 0, To: 3}}); err != nil {
		return fmt.Errorf("checkpoints: moving p from 0 to 3: %v", err)
	}
	_, err := s.Append(ctx, "t1", grist.Batch{Events: []grist.Event{{Type: "a-happened", Version: 1, Body: []byte(`{}`)}},
		Checkpoint: &grist.CheckpointMove{Processor: "p", From: 0, To: 5}})
	var cm *grist.CheckpointMoved
	if !errors.As(err, &cm) || cm.Found != 3 || cm.Expected != 0 {
		return fmt.Errorf("checkpoints: moving p from 0 when it is at 3: %v, want a *grist.CheckpointMoved found at 3", err)
	}
	if h, _ := s.Heads(ctx); h["t1"] != 0 {
		return fmt.Errorf("checkpoints: a refused move appended its events (head %d)", h["t1"])
	}
	if _, err := s.Append(ctx, "t1", grist.Batch{Events: []grist.Event{{Type: "a-happened", Version: 1, Body: []byte(`{}`)}},
		Checkpoint: &grist.CheckpointMove{Processor: "p", From: 3, To: 4}}); err != nil {
		return fmt.Errorf("checkpoints: moving p from 3 to 4 with an event: %v", err)
	}
	for p, want := range map[string]grist.Position{"p": 4, "q": 0} {
		got, err := s.Checkpoint(ctx, "t1", p)
		if err != nil || got != want {
			return fmt.Errorf("checkpoints: %s is %d (%v), want %d", p, got, err, want)
		}
	}
	return nil
}

// Markers: a batch's marker is written with its receipt, and a second
// batch with the same id fails, naming it, and appends nothing.
func Markers(ctx context.Context, s grist.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	e := grist.Event{Type: "a-happened", Version: 1, Body: []byte(`{}`)}
	head, err := s.Append(ctx, "t1", grist.Batch{Events: []grist.Event{e, e}, Marker: &grist.Marker{ID: "cmd-1", Hash: "h1"}})
	if err != nil {
		return err
	}
	m, ok, err := s.Marker(ctx, "t1", "cmd-1")
	want := grist.NewReceipt("t1", head, 2)
	if err != nil || !ok || m.Hash != "h1" || !reflect.DeepEqual(m.Receipt, want) {
		return fmt.Errorf("markers: cmd-1 is %+v (found %v, %v), want hash h1 and receipt %+v", m, ok, err, want)
	}
	_, err = s.Append(ctx, "t1", grist.Batch{Events: []grist.Event{e}, Marker: &grist.Marker{ID: "cmd-1", Hash: "h2"}})
	var cf *grist.ConditionFailed
	if !errors.As(err, &cf) || cf.Marker != "cmd-1" {
		return fmt.Errorf("markers: a second batch with id cmd-1: %v, want a *grist.ConditionFailed naming the marker", err)
	}
	if h, _ := s.Heads(ctx); h["t1"] != head {
		return fmt.Errorf("markers: the refused batch moved the head to %d", h["t1"])
	}
	if _, ok, _ := s.Marker(ctx, "t1", "cmd-2"); ok {
		return fmt.Errorf("markers: cmd-2 found, never written")
	}
	return nil
}

// TenantIsolation: a read of one tenant never returns another's events,
// and an append to one never moves another's head, checkpoints or
// markers.
func TenantIsolation(ctx context.Context, s grist.Store) error {
	for _, t := range []string{"t1", "t2"} {
		if err := s.CreateTenant(ctx, t); err != nil {
			return err
		}
	}
	r := rand.New(rand.NewPCG(7, 8))
	var b1, b2 [][]grist.Event
	for i := 0; i < 5; i++ {
		b1 = append(b1, []grist.Event{randEvent(r), randEvent(r)})
	}
	b2 = append(b2, []grist.Event{randEvent(r)})
	if err := appendAll(ctx, s, "t1", b1); err != nil {
		return err
	}
	if _, err := s.Append(ctx, "t1", grist.Batch{Marker: &grist.Marker{ID: "m"}, Checkpoint: &grist.CheckpointMove{Processor: "p", To: 7}}); err != nil {
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
		evs, _, err := s.Read(ctx, t, grist.Query{Types: types}, 0)
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
// grist.ErrUnknownTenant, by reads and appends alike.
func UnknownTenant(ctx context.Context, s grist.Store) error {
	if _, _, err := s.Read(ctx, "nobody", grist.Query{Types: types}, 0); !errors.Is(err, grist.ErrUnknownTenant) {
		return fmt.Errorf("unknown tenant: a read: %v, want grist.ErrUnknownTenant", err)
	}
	if _, err := s.Append(ctx, "nobody", grist.Batch{}); !errors.Is(err, grist.ErrUnknownTenant) {
		return fmt.Errorf("unknown tenant: an append: %v, want grist.ErrUnknownTenant", err)
	}
	return nil
}
