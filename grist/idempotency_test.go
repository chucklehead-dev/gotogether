package grist_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

type Counter struct {
	Counter string `json:"counter" grist:"tag"`
}

type Bump struct {
	Counter string `json:"counter"`
}

var errUnknown = grist.Rejection("unknown-counter")

// bumpApp has open-counter (creates a counter), bump-counter (refused for
// an unknown counter) and close-counter, whose arguments are bump's.
func bumpApp() (a *grist.App, open, bump, closeC *grist.Command[Bump], bumped *grist.EventType[Counter]) {
	a = grist.New("counters")
	opened := grist.NewEvent[Counter](a, "counter-opened")
	bumped = grist.NewEvent[Counter](a, "counter-bumped")
	closed := grist.NewEvent[Counter](a, "counter-closed")
	open = grist.NewCommand(a, "open-counter", func(r *grist.Reads, c Bump) ([]grist.Event, error) {
		return []grist.Event{opened.Of(Counter(c))}, nil
	})
	bump = grist.NewCommand(a, "bump-counter", func(r *grist.Reads, c Bump) ([]grist.Event, error) {
		if !r.Exists(opened, grist.Tag("counter", c.Counter)) {
			return nil, errUnknown
		}
		return []grist.Event{bumped.Of(Counter(c))}, nil
	})
	closeC = grist.NewCommand(a, "close-counter", func(r *grist.Reads, c Bump) ([]grist.Event, error) {
		return []grist.Event{closed.Of(Counter(c))}, nil
	})
	return
}

func count(t *testing.T, s grist.Store, typ string) int {
	t.Helper()
	evs, _, err := s.Read(context.Background(), "t1", grist.Selector{Types: []string{typ}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return len(evs)
}

// A key's first answer is final, a refusal's too: a command refused
// (unknown-counter), retried under its id after the state changed so that
// it would now commit, gets the refusal again and appends nothing (a late
// duplicate of a request the client was told was refused). The refusal
// commits its marker, so it is recorded once, at that commit.
func TestRefusalIsFinal(t *testing.T) {
	_, open, bump, _, _ := bumpApp()
	s := newStore(t)
	ctx, steps := recorded(t, s)
	env := grist.Envelope{ID: "k1", Tenant: "t1"}
	if _, err := bump.Execute(ctx, s, env, Bump{Counter: "x"}); !errors.Is(err, errUnknown) {
		t.Fatalf("a bump of a counter never opened: %v, want unknown-counter", err)
	}
	if _, err := open.Execute(ctx, s, grist.Envelope{ID: "k2", Tenant: "t1"}, Bump{Counter: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := bump.Execute(ctx, s, env, Bump{Counter: "x"}); !errors.Is(err, errUnknown) {
		t.Errorf("the retry under k1, once x is open: %v, want the first answer, unknown-counter", err)
	}
	if n := count(t, s, "counter-bumped"); n != 0 {
		t.Errorf("%d bumps appended: the refused key committed later", n)
	}
	m, ok, _ := s.Marker(context.Background(), "t1", "k1")
	if !ok || m.Outcome != "unknown-counter" {
		t.Errorf("k1's marker %+v (found %v), want its outcome unknown-counter", m, ok)
	}
	st := steps()
	if len(st) != 2 || st[0].Outcome != "unknown-counter" || st[0].Order["pos"] != 1 || st[1].Outcome != "ok" {
		t.Errorf("steps %v: want the refusal (at its commit, order.pos 1) and the open, nothing for the retry", st)
	}
}

// The marker's hash covers the command, not only its arguments: another
// command under the same id, with JSON-equal arguments, is a reused key,
// not the first command's retry.
func TestKeyReusedAcrossCommands(t *testing.T) {
	_, open, _, closeC, _ := bumpApp()
	s := newStore(t)
	env := grist.Envelope{ID: "k1", Tenant: "t1"}
	if _, err := open.Execute(context.Background(), s, env, Bump{Counter: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := closeC.Execute(context.Background(), s, env, Bump{Counter: "x"}); !errors.Is(err, grist.ErrKeyReused) {
		t.Errorf("close-counter under open-counter's id: %v, want grist.ErrKeyReused", err)
	}
	if n := count(t, s, "counter-closed"); n != 0 {
		t.Errorf("%d closes appended", n)
	}
}

// lossy commits every append and says the outcome is unknown; its first
// markerFails marker reads fail.
type lossy struct {
	*memstore.Store
	markerFails int
	commits     bool
}

func (s *lossy) Marker(ctx context.Context, t, id string) (store.Marker, bool, error) {
	if s.markerFails > 0 {
		s.markerFails--
		return store.Marker{}, false, errors.New("marker read lost")
	}
	return s.Store.Marker(ctx, t, id)
}

func (s *lossy) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	if s.commits {
		if _, err := s.Store.Append(ctx, t, b); err != nil {
			return 0, err
		}
	}
	return 0, errors.New("connection lost: the commit's outcome is unknown")
}

// A commit of unknown outcome as the transient budget runs out: the
// marker, read once more, answers with the receipt. A store that never
// answers gives ErrOutcomeUnknown (retry with the same id) once an append
// may have committed, ErrUnavailable when none was tried.
func TestUnknownOutcomeAtBudget(t *testing.T) {
	_, _, cmd := app(false)
	s := &lossy{Store: newStore(t), markerFails: 10 - 1, commits: true}
	rc, err := cmd.Execute(context.Background(), s, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "x", Name: "a"})
	if err != nil || rc.Pos != 1 {
		t.Errorf("a command that committed as its budget ran out: %+v, %v; want its receipt", rc, err)
	}
	never := &lossy{Store: newStore(t), markerFails: 1 << 30}
	if _, err := cmd.Execute(context.Background(), never, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "x", Name: "a"}); !errors.Is(err, grist.ErrUnavailable) {
		t.Errorf("a store whose marker reads always fail: %v, want grist.ErrUnavailable (nothing appended)", err)
	}
	unknown := &lossy{Store: newStore(t), markerFails: 0}
	if _, err := cmd.Execute(context.Background(), unknown, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "x", Name: "a"}); !errors.Is(err, grist.ErrOutcomeUnknown) {
		t.Errorf("a store whose appends' outcomes are never known: %v, want grist.ErrOutcomeUnknown", err)
	}
}

// A commit writes its step in the batch's transaction: the store's step
// log has it, with the command's fields but those its head gives
// (obs.head, order.pos), which the relay adds.
func TestStepInTheTransaction(t *testing.T) {
	_, _, cmd := app(false)
	s := newStore(t)
	if _, err := cmd.Execute(context.Background(), s, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "x", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Steps(context.Background(), "t1", 0, 0)
	if err != nil || len(rows) != 1 || rows[0].Head != 1 {
		t.Fatalf("step rows %v (%v), want one at head 1", rows, err)
	}
	var row struct {
		Steps []struct {
			Action string
			KV     [][]any
			Events int
		}
	}
	err = json.Unmarshal(rows[0].Step, &row)
	fields := map[string]any{}
	if err == nil && len(row.Steps) == 1 {
		for _, kv := range row.Steps[0].KV {
			fields[kv[0].(string)] = kv[1]
		}
	}
	if err != nil || len(row.Steps) != 1 || row.Steps[0].Action != "createCounter" || row.Steps[0].Events != 1 ||
		fields["id"] != "c1" || fields["name"] != "a" || fields["obs.head"] != nil || fields["order.pos"] != nil {
		t.Errorf("step row %s (%v): want createCounter's one step, with its id and arguments, its event, and no field the head gives", rows[0].Step, err)
	}
}

// storeBatch is an unconditioned batch of events (another writer's).
func storeBatch(events ...grist.Event) store.Batch { return store.Batch{Events: events} }
