package validate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Exercises the library entry points on a model unrelated to edgePublish:
// record with a MemorySink, Check with a Checker.
func TestAccountModel(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/account.yaml", Options{Backend: "typescript", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	run := func(ops ...any) []qtrace.Step {
		sink := &qobs.MemorySink{}
		r := qobs.NewRecorder(sink, qobs.WithActor("acct-1"))
		ctx := context.Background()
		for i := 0; i < len(ops); i += 3 {
			r.Record(ctx, ops[i].(string), nil, "amount", ops[i+1], "obs.balance", ops[i+2])
		}
		return sink.Steps()
	}
	cases := []struct {
		name    string
		steps   []qtrace.Step
		ok      bool
		failure string // Failure.Kind of the conformance check
		inv     string // an invariant that must fail
	}{
		{"conforms", run("deposit", 50, 50, "withdraw", 20, 30, "deposit", 5, 35), true, "", ""},
		{"overdraw", run("deposit", 10, 10, "withdraw", 20, -10), false, "disabled", ""},
		{"wrong balance", run("deposit", 10, 10, "deposit", 5, 16), false, "state", ""},
		{"over limit", run("deposit", 60, 60, "deposit", 60, 120), false, "", "underLimit"},
	}
	for _, tc := range cases {
		rep, err := c.Check(context.Background(), tc.steps)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if rep.OK() != tc.ok {
			t.Errorf("%s: OK()=%v\n%s", tc.name, rep.OK(), rep.Summary())
			continue
		}
		r := rep.Actors[0]
		if tc.failure != "" && (r.Failure == nil || r.Failure.Kind != tc.failure || r.Failure.Index != 1) {
			t.Errorf("%s: want %s failure at step 1\n%s", tc.name, tc.failure, rep.Summary())
		}
		if tc.inv != "" {
			found := false
			for _, i := range r.Invariants {
				if i.Name == tc.inv && i.Verdict == Fail {
					found = true
				}
			}
			if r.Conforms != Pass || !found || !strings.Contains(rep.Summary(), "violated after record ") || !strings.Contains(rep.VerboseSummary(), "step 2 of the replay") {
				t.Errorf("%s: want conforming trace violating %s\n%s", tc.name, tc.inv, rep.Summary())
			}
		}
	}
}

// TypeScript can judge a replay whose integers are outside Rust's supported
// range. Missing-state guidance must not promise that Rust can replay it.
func TestTypeScriptMissingStateGuidanceIsConditional(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/account.yaml", Options{Backend: "typescript", Eval: "quint", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	steps := accountSteps(3, 8)[:1]
	steps[0].Args["amount"] = json.Number("18446744073709551615")
	steps[0].Obs["balance"] = int64(0)
	report, err := c.Check(t.Context(), steps)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Actors) != 1 {
		t.Fatalf("want one rejected actor: %s", report.Summary())
	}
	failure := report.Actors[0].Failure
	if failure == nil || failure.Kind != "state" || failure.Index != 0 || failure.State != nil {
		t.Fatalf("want real TypeScript state mismatch without ITF: %s", report.VerboseSummary())
	}
	const guidance = "QUINT_BACKEND=rust may report it if the Rust backend supports this model"
	if !strings.Contains(failure.noState, guidance) || !strings.Contains(report.VerboseSummary(), guidance) {
		t.Fatalf("missing conditional backend guidance: %s", report.VerboseSummary())
	}
}
