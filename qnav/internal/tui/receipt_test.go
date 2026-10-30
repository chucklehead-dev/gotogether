package tui

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func TestRunReceiptCategoriesAndCursorScope(t *testing.T) {
	for _, status := range []receipt.Status{receipt.Pass, receipt.Fail, receipt.Inconclusive, receipt.NotChecked, receipt.Error} {
		t.Run(string(status), func(t *testing.T) {
			m := New(Config{NoColor: true})
			m.w, m.h = 100, 60
			s := receipt.Summary{Status: status, Reason: "judgment reason", Scope: "collected_history", OtherExecutions: receipt.NotChecked}
			n := &navproto.Node{Run: "run", ID: "0@1", Receipt: s, Model: []navproto.ActorState{{Actor: "a"}}}
			screen := m.runScreen(&entry{r: n})
			head := strings.Join(screen.head, "\n")
			detail := strings.Join(screen.detail, "\n")
			if !strings.Contains(head, string(status)+" · judgment reason") || !strings.Contains(detail, "whole-run judgment · independent of cursor") || !strings.Contains(detail, "other executions were not checked") {
				t.Fatalf("%s\n%s", head, detail)
			}
			// Moving the cursor changes model state, not the whole-run receipt.
			n.At = 0
			n.Model[0].State = []navproto.NamedValue{{Name: "balance", Value: "999"}}
			next := m.runScreen(&entry{r: n})
			if !strings.Contains(strings.Join(next.detail, "\n"), s.String()[:len("what was checked:")]) || n.Receipt.Status != status {
				t.Fatal("cursor changed judgment")
			}
		})
	}
}
