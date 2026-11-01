package validate

import (
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

func navReconstructionChecker(t *testing.T, file string) *Checker {
	t.Helper()
	skip.Quint(t)
	c, err := NewChecker(file, Options{Dir: t.TempDir(), NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func navReconstructionDeposit(actor, recorder string, seq uint64, amount int64) qtrace.Step {
	return qtrace.Step{Actor: actor, Recorder: recorder, Seq: seq, Process: "p", Action: "deposit", Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": amount}}
}

func navReconstructionIssue(t *testing.T, c *Checker, steps []qtrace.Step, actor, kind string) qtrace.Issue {
	t.Helper()
	traces, _ := c.Binding.Reconstruct(steps)
	for _, tr := range traces {
		if tr.Actor != actor {
			continue
		}
		for _, issue := range tr.Issues {
			if issue.Severity == "error" && issue.Kind == kind {
				return issue
			}
		}
	}
	t.Fatalf("fixture has no error %s for actor %q: %+v", kind, actor, traces)
	return qtrace.Issue{}
}

func navReconstructionRefuses(t *testing.T, c *Checker, steps []qtrace.Step, actor string, issue qtrace.Issue) {
	t.Helper()
	for _, at := range []int{0, 1, -1} {
		n, err := c.Nav(NavStart{Steps: steps, Actor: actor, At: at})
		if err == nil || n != nil {
			t.Errorf("At=%d: got navigator=%v error=%v; want no navigator and reconstruction error", at, n != nil, err)
			continue
		}
		if !strings.Contains(err.Error(), issue.Kind) || !strings.Contains(err.Error(), issue.Msg) {
			t.Errorf("At=%d: error %q lost reconstruction issue %+v", at, err, issue)
		}
	}
}

func TestNavRefusesFatalReconstruction(t *testing.T) {
	c := navReconstructionChecker(t, "testdata/account.yaml")
	first := navReconstructionDeposit("a", "r", 1, 5)
	conflict := navReconstructionDeposit("a", "r", 1, 9)
	cycle := []qtrace.Step{first, navReconstructionDeposit("a", "r", 2, 1)}
	cycle[0].Thread, cycle[1].Thread = "T", "T"
	cycle[0].Order, cycle[1].Order = map[string]int64{"batch": 2}, map[string]int64{"batch": 1}
	overlap := []qtrace.Step{first, navReconstructionDeposit("a", "r", 2, 1), navReconstructionDeposit("a", "r", 3, 1)}
	overlap[0].Process, overlap[1].Process, overlap[2].Process = "p1", "p2", "p1"
	at := func(ms int64) time.Time { return time.Unix(0, ms*int64(time.Millisecond)) }
	invocation := []qtrace.Step{navReconstructionDeposit("a", "r", 2, 1), first}
	invocation[0].Invocation, invocation[0].Time = true, at(50)
	invocation[1].Call, invocation[1].Time = 2, at(5)
	realtime := []qtrace.Step{first, navReconstructionDeposit("a", "r", 2, 1)}
	realtime[0].Thread, realtime[1].Thread = "T", "T"
	realtime[0].Invoked, realtime[0].Time = at(90), at(100)
	realtime[1].Invoked, realtime[1].Time = at(10), at(20)
	for _, tc := range []struct {
		kind  string
		steps []qtrace.Step
	}{
		{"duplicate", []qtrace.Step{first, conflict}}, {"cycle", cycle}, {"overlap", overlap}, {"invocation", invocation}, {"realtime", realtime},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			issue := navReconstructionIssue(t, c, tc.steps, "a", tc.kind)
			navReconstructionRefuses(t, c, tc.steps, "a", issue)
		})
	}
}

func TestNavReconstructionActorIsolation(t *testing.T) {
	c := navReconstructionChecker(t, "testdata/account.yaml")
	a := navReconstructionDeposit("a", "r", 1, 5)
	conflict := navReconstructionDeposit("other", "r", 1, 9)
	clean := navReconstructionDeposit("clean", "clean-recorder", 1, 4)
	steps := []qtrace.Step{a, conflict, clean}
	for _, actor := range []string{"a", "other"} {
		issue := navReconstructionIssue(t, c, steps, actor, "duplicate")
		navReconstructionRefuses(t, c, steps, actor, issue)
	}
	if n, err := c.Nav(NavStart{Steps: steps, Actor: "clean", At: -1}); err != nil || n == nil {
		t.Fatalf("unrelated clean actor: %v", err)
	}
}

func TestNavPartitionInheritsWholeReconstructionFailure(t *testing.T) {
	c := navReconstructionChecker(t, "testdata/ledger.yaml")
	a := qtrace.Step{Actor: "ledger", Recorder: "r", Seq: 1, Action: "open", Args: map[string]any{"acct": "a"}}
	b := a
	b.Args = map[string]any{"acct": "b"}
	steps := []qtrace.Step{a, b}
	issue := navReconstructionIssue(t, c, steps, "ledger", "duplicate")
	navReconstructionRefuses(t, c, steps, "ledger acct=a", issue)
}

func TestNavReconstructionSafeControls(t *testing.T) {
	c := navReconstructionChecker(t, "testdata/account.yaml")
	a := navReconstructionDeposit("a", "r", 1, 5)
	n, err := c.Nav(NavStart{Steps: []qtrace.Step{a, a}, Actor: "a", At: -1})
	if err != nil || n == nil {
		t.Fatalf("identical retry copies: %v", err)
	}
	node, err := n.Node()
	if err != nil {
		t.Fatal(err)
	}
	if len(node.Path) != 1 || node.Path[0] != "deposit(5)" {
		t.Fatalf("identical copies replayed more than once: %v", node.Path)
	}
	if n, err := c.Nav(NavStart{}); err != nil || n == nil {
		t.Fatalf("model-only navigation: %v", err)
	}
	// A later disabled model call must not prevent opening its valid prefix.
	bad := navReconstructionDeposit("a", "r", 2, 9)
	bad.Action = "withdraw"
	if n, err := c.Nav(NavStart{Steps: []qtrace.Step{a, bad}, Actor: "a", At: 1}); err != nil || n == nil {
		t.Fatalf("valid prefix before a model failure: %v", err)
	}
}

// Loss has separate start/resume policies; the reconstruction guard must not
// silently change them while rejecting contradictory capture evidence.
func TestNavReconstructionKeepsLossPolicy(t *testing.T) {
	c := navReconstructionChecker(t, "testdata/account.yaml")
	for _, tc := range []struct {
		kind  string
		steps []qtrace.Step
	}{
		{"lost-prefix", []qtrace.Step{navReconstructionDeposit("a", "r", 3, 1)}},
		{"gap", []qtrace.Step{navReconstructionDeposit("a", "r", 1, 1), navReconstructionDeposit("a", "r", 3, 1)}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			navReconstructionIssue(t, c, tc.steps, "a", tc.kind)
			if n, err := c.Nav(NavStart{Steps: tc.steps, Actor: "a", At: -1}); err != nil || n == nil {
				t.Fatalf("existing loss policy changed: %v", err)
			}
		})
	}
}

func TestNavReconstructionHeadSnapshot(t *testing.T) {
	c := navReconstructionChecker(t, "testdata/snapshot.yaml")
	steps, _, _ := snapshotRuns()
	navReconstructionIssue(t, c, steps, "", "lost-prefix")
	n, err := c.Nav(NavStart{Steps: steps, At: -1})
	if err != nil {
		t.Fatal(err)
	}
	node, err := n.Node()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range node.State {
		if value.Name == "balance" && value.Value == "55" {
			return
		}
	}
	t.Fatalf("snapshot was not used as the navigation start: %+v", node.State)
}
