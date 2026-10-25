package qhegel

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The case checker's column of the verdict-path matrix (validate's
// TestVerdictMatrix): every kind of verdict a case can meet, its passing
// and failing run checked by one caseChecker in turn, then each again and
// an extension of the failing one, so a cache that gave one run another's
// verdict (a key without times, a failing prefix reused past an eventual
// val) shows. Pending calls are no case's: a case is checked as it
// records, with no search.
func TestCaseCheckerVerdictMatrix(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	at := func(s int) time.Time { return time.Unix(0, 0).Add(time.Duration(s) * time.Second) }
	seq := func(steps ...qtrace.Step) []qtrace.Step {
		for i := range steps {
			steps[i].Recorder, steps[i].Seq = "r", uint64(i+1)
			if steps[i].Time.IsZero() {
				steps[i].Time = at(i)
			}
		}
		return steps
	}
	acct := func(action string, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: action, Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	timed := func(action string, s int) qtrace.Step { return qtrace.Step{Action: action, Time: at(s)} }
	gossip := func(node string, value int, seen []int) qtrace.Step {
		s := qtrace.Step{Actor: "g", Args: map[string]any{"node": node}, Obs: map[string]any{}}
		if seen == nil {
			s.Action, s.Args["value"] = "write", int64(value)
		} else {
			b, _ := json.Marshal(seen)
			s.Action, s.Obs["seen"] = "read", string(b)
		}
		return s
	}
	settled := qtrace.Step{Action: "settled", Settled: true}
	td := "../validate/testdata/"
	for _, x := range []struct {
		name, binding        string
		pass, fail, extended []qtrace.Step
		extendedOK           bool // whether the failing run, extended, passes
	}{
		{"conformance", "account.yaml", seq(acct("deposit", 5, 5)), seq(acct("deposit", 5, 6)),
			seq(acct("deposit", 5, 6), acct("deposit", 1, 7)), false},
		{"invariant", "account.yaml", seq(acct("deposit", 50, 50)), seq(acct("deposit", 150, 150)),
			seq(acct("deposit", 150, 150), acct("withdraw", 100, 50)), false},
		// the extension recorded after the settle: no longer settled last
		{"eventual", "funds.binding.yaml", seq(acct("deposit", 5, 5), acct("withdraw", 5, 0)),
			seq(acct("deposit", 5, 5), acct("withdraw", 5, 0), settled), seq(acct("deposit", 5, 5), acct("withdraw", 5, 0), settled, acct("deposit", 3, 3)), true},
		{"within", "within.yaml", seq(timed("crash", 0), timed("heal", 1), timed("elect", 2)),
			seq(timed("crash", 0), timed("heal", 1), timed("elect", 5)), seq(timed("crash", 0), timed("heal", 1), timed("elect", 5), timed("tick", 6)), false},
		{"hidden", "gossip.yaml", seq(gossip("a", 1, nil), gossip("b", 0, []int{1})), seq(gossip("a", 1, nil), gossip("b", 0, []int{9})),
			seq(gossip("a", 1, nil), gossip("b", 0, []int{9}), gossip("c", 2, nil)), false},
	} {
		c, err := validate.NewChecker(td+x.binding, validate.Options{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		cc := newCaseChecker(c)
		for i, run := range []struct {
			steps []qtrace.Step
			ok    bool
		}{{x.fail, false}, {x.pass, true}, {x.fail, false}, {x.pass, true}, {x.extended, x.extendedOK}} {
			v, err := cc.Check(context.Background(), run.steps)
			if err != nil {
				t.Fatalf("%s, check %d: %v", x.name, i, err)
			}
			if v.OK != run.ok {
				t.Errorf("%s, check %d (cached %v): OK %v, want %v:\n%s", x.name, i, v.Cached, v.OK, run.ok, v.Summary)
			}
		}
		if _, hits := cc.Stats(); hits < 2 {
			t.Errorf("%s: %d cache hits, want the repeated runs from the cache", x.name, hits)
		}
	}
}
