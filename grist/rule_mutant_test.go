package grist_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/qtest"
)

type intentAdded struct {
	Item string `json:"item" grist:"tag"`
	Qty  int    `json:"qty"`
}

type intentReadError struct {
	grist.Store
	err error
}

func (s intentReadError) Read(context.Context, string, grist.Selector, grist.Position) ([]grist.Stored, grist.Position, error) {
	return nil, 0, s.err
}

// Use a separate test process: the generated mutant selector is fixed by init,
// so no test changes the process-wide selector while other tests run.
func TestGeneratedRuleRefusalBypass(t *testing.T) {
	if os.Getenv("GRIST_RULE_INTENT_CHILD") != "1" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestGeneratedRuleRefusalBypass$", "-test.v")
		cmd.Env = append(os.Environ(), "GRIST_RULE_INTENT_CHILD=1", qtest.MutantEnv+"=drop-intent-capacity")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generated rule control: %v\n%s", err, out)
		}
		return
	}
	full := grist.Rejection("full")
	other := grist.Rejection("other")
	bug := errors.New("rule callback failed")
	for _, mode := range []string{"capacity", "unexpected", "undeclared", "bad-read", "store-error"} {
		t.Run(mode, func(t *testing.T) {
			a := grist.New("intent")
			ev := grist.NewEvent[intentAdded](a, "added")
			cmd := grist.NewCommand(a, "add", func(_ *grist.Reads, c intentAdded) ([]grist.Event, error) { return []grist.Event{ev.Of(c)}, nil }, grist.Appends(ev))
			calls := 0
			grist.NewRule(a, "intent-capacity", ev, func(r *grist.Reads, e intentAdded) error {
				calls++
				switch mode {
				case "unexpected":
					return bug
				case "undeclared":
					return other
				case "bad-read":
					r.Exists(ev, grist.Tag("missing", "a"))
					return full
				}
				total := 0
				for _, old := range grist.Events(r, ev, grist.Tag("item", e.Item)) {
					total += old.Qty
				}
				if total+e.Qty > 5 {
					return full
				}
				return nil
			}, grist.Refuses(full))
			if got := gristtest.RuleMutants(a); len(got) != 1 || got[0] != "drop-intent-capacity" {
				t.Fatal(got)
			}
			st := memstore.New("intent")
			if err := st.CreateTenant(t.Context(), "t1"); err != nil {
				t.Fatal(err)
			}
			ctx, steps := recorded(t, st)
			var commandStore grist.Store = st
			if mode == "store-error" {
				commandStore = intentReadError{Store: st, err: bug}
			}
			_, err := cmd.Execute(ctx, commandStore, grist.Envelope{Tenant: "t1", ID: "first"}, intentAdded{Item: "a", Qty: 3})
			switch mode {
			case "unexpected":
				if !errors.Is(err, bug) {
					t.Fatalf("callback error lost: %v", err)
				}
			case "store-error":
				if !errors.Is(err, grist.ErrUnavailable) || !strings.Contains(err.Error(), bug.Error()) {
					t.Fatalf("persistent store error was not retried and reported: %v", err)
				}
			case "undeclared":
				if err == nil || !strings.Contains(err.Error(), "Refuses does not declare") {
					t.Fatalf("undeclared refusal ignored: %v", err)
				}
			case "bad-read":
				if err == nil || !strings.Contains(err.Error(), "missing") {
					t.Fatalf("read misuse ignored: %v", err)
				}
			case "capacity":
				if err != nil {
					t.Fatal(err)
				}
				if _, err := cmd.Execute(ctx, st, grist.Envelope{Tenant: "t1", ID: "second"}, intentAdded{Item: "a", Qty: 3}); err != nil {
					t.Fatalf("declared refusal was not bypassed: %v", err)
				}
				observed := 0
				for _, step := range steps() {
					if step.Action == "add" {
						observed++
						reads, ok := step.Args["reads"].(string)
						if !ok {
							t.Fatalf("read set is not recorded as a literal: %T", step.Args["reads"])
						}
						if reads != `[["added","item","a"]]` {
							t.Errorf("mutant omitted the rule's read: %s", reads)
						}
						if step.Outcome != "ok" {
							t.Errorf("mutant outcome %q, want ok", step.Outcome)
						}
					}
				}
				if observed != 2 || calls != 2 {
					t.Fatalf("commands=%d callback calls=%d, want 2/2", observed, calls)
				}
			}
			if calls == 0 {
				t.Fatal("the rule callback was skipped")
			}
		})
	}
}
