package qhegel

import (
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A case whose recorder lost its tail, or that starts from a snapshot,
// reuses no prefix's verdict: a failure after a lost tail's cut is no
// verdict, and where a trace starts decides every state after it. A
// whole case is still keyed.
func TestCaseKeysLost(t *testing.T) {
	c, err := validate.NewChecker(t.Context(), "../validate/testdata/snapshot.yaml", validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cc := newCaseChecker(c)
	deposit := qtrace.Step{Action: "deposit", Recorder: "r", Seq: 1, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}
	restore := qtrace.Step{Action: "restore", Recorder: "r", Seq: 1, Args: map[string]any{"b": int64(50), "n": int64(1)}}
	after := deposit
	after.Seq = 2
	for _, x := range []struct {
		name       string
		steps      []qtrace.Step
		prefixable bool
	}{
		{"whole", []qtrace.Step{deposit}, true},
		{"a lost tail", qtrace.MarkLost([]qtrace.Step{deposit}, qtrace.Lost{Recorder: "r", After: 1}), false},
		{"a snapshot start", []qtrace.Step{restore, after}, false},
	} {
		if k := cc.key(x.steps); k.prefixable != x.prefixable {
			t.Errorf("%s: prefixable %v, want %v", x.name, k.prefixable, x.prefixable)
		}
	}
	// a lost tail's case is keyed with its times: a step of another
	// recorder after the cut (no verdict) and before it (a verdict)
	run := func(ms int64) []qtrace.Step {
		other := qtrace.Step{Action: "deposit", Recorder: "s", Seq: 1, Time: time.Unix(0, ms*int64(time.Millisecond)), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(10)}}
		first := deposit
		first.Time = time.Unix(0, 10*int64(time.Millisecond))
		return qtrace.MarkLost([]qtrace.Step{first, other}, qtrace.Lost{Recorder: "r", After: 1})
	}
	full := func(k keyed) string { return k.keys[len(k.keys)-1] }
	if full(cc.key(run(30))) == full(cc.key(run(5))) {
		t.Errorf("a lost tail's cases after and before the cut share a key")
	}
}

// A case with an effect made under a call after the call completed (a
// late effect, the completed step's) reuses no prefix's verdict: the
// prefix judged that step without it. Effects made before the completion
// keep prefixes reusable.
func TestCaseKeysLateEffects(t *testing.T) {
	c, err := validate.NewChecker(t.Context(), "../validate/testdata/snapshot.yaml", validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cc := newCaseChecker(c)
	invoked := qtrace.Step{Action: "deposit", Recorder: "r", Seq: 1, Record: qtrace.Record{Invocation: true}, Args: map[string]any{"amount": int64(5)}}
	sent := qtrace.Step{Action: "effect", Recorder: "r", Seq: 2, Call: 1, Record: qtrace.Record{Effect: "rpc.bank.request"}, Args: map[string]any{"value": int64(5)}}
	done := qtrace.Step{Action: "deposit", Recorder: "r", Seq: 3, Call: 1, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}
	late := sent
	late.Seq = 4
	if k := cc.key([]qtrace.Step{invoked, sent, done}); !k.prefixable {
		t.Error("an effect made before its call completed: no prefix reused")
	}
	if k := cc.key([]qtrace.Step{invoked, sent, done, late}); k.prefixable {
		t.Error("a late effect: prefixes reused, though it changes its completed step's effects")
	}
}

// A case of an actor the binding's actors: leave out reuses no prefix's
// verdict: the checker judges no trace of it, so its constants are no
// instance's (the case checker reconstructed every actor, and planned
// the left-out one's).
func TestCaseKeysActors(t *testing.T) {
	c, err := validate.NewChecker(t.Context(), "../validate/testdata/snapshot.yaml", validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	deposit := qtrace.Step{Action: "deposit", Recorder: "r", Actor: "acct.clients", Seq: 1, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}
	if _, ok := newCaseChecker(c).constants([]qtrace.Step{deposit}); !ok {
		t.Fatal("every actor checked: the case should reuse prefixes")
	}
	b := *c.Binding
	b.Actors = []string{"!*.clients"}
	c.Binding = &b
	if _, ok := newCaseChecker(c).constants([]qtrace.Step{deposit}); ok {
		t.Error("an actor the binding leaves out: its prefixes reused")
	}
}
