package validate

import (
	"fmt"
	"slices"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// snapshotFirst reports whether tr starts with a step of b's snapshot
// action: the state its replay starts from (binding.Plan.Start), which
// every order a search tries keeps first.
func snapshotFirst(b *binding.Binding, tr *qtrace.Trace) bool {
	return b.Snapshot != "" && len(tr.Steps) > 0 && tr.Steps[0].Action == b.Snapshot && !tr.Steps[0].Ambiguous
}

// startIssues are tr's issues as its start says them: the lost prefix of
// the recorder whose snapshot step the trace starts with is what that
// step recorded, judged from there (info, no error). Another recorder's
// lost prefix stays an error: its lost steps may have come after the
// snapshot.
func startIssues(b *binding.Binding, tr *qtrace.Trace) []qtrace.Issue {
	issues := slices.Clone(tr.Issues)
	if !snapshotFirst(b, tr) {
		return issues
	}
	s := &tr.Steps[0]
	for _, l := range tr.Lost {
		if !l.Prefix || l.Recorder != s.Recorder || l.After+1 != s.Seq && !(l.After == 0 && s.Seq == 0) {
			continue
		}
		if i := slices.Index(issues, l.Issue()); i >= 0 {
			issues[i] = qtrace.Issue{Severity: "info", Kind: "lost-prefix", Msg: fmt.Sprintf("judged from the state recorded by %s at seq %d (%s)", s.Action, s.Seq, l)}
		}
	}
	return issues
}
