package lease

import (
	"os"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate/model"
)

// The design (design/lease.md) compares fencing with trusting the lease,
// each a variant of one model, and records the decision as the table
// simulate -matrix writes into the document. The table must be the
// model's: a change to the model that changes a verdict fails here until
// the document says so too (gotogether simulate -matrix -graph
// -write-matrix design/lease.md -binding design/lease.binding.yaml).
func TestDesignMatrixIsCurrent(t *testing.T) {
	qtest.RequireQuint(t)
	m, err := model.Matrix(t.Context(), "design/lease.binding.yaml", model.SimulateOptions{Graph: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.Code != 0 {
		t.Fatalf("a design is not as the binding says (exit %d):\n%s", m.Code, m.Text())
	}
	doc, err := os.ReadFile("design/lease.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "<!-- gotogether:matrix -->\n"+m.Text()+"<!-- /gotogether:matrix -->") {
		t.Fatalf("design/lease.md's table is not the model's: run gotogether simulate -matrix -graph -write-matrix design/lease.md -binding design/lease.binding.yaml\nthe model's:\n%s", m.Text())
	}
	// the decision, cell by cell: fencing holds the requirement whatever
	// the clocks, a lease alone only with true clocks
	want := map[string][]string{
		"noStaleWrite": {"holds", "holds", "breaks"},
		"noLateWrite":  {"breaks", "holds", "breaks"},
	}
	for _, r := range m.Rows {
		for i, c := range r.Cells {
			if c.Verdict != want[r.Check][i] || !c.OK {
				t.Errorf("%s under %s: %s (as expected: %v), want %s", r.Check, c.Variant, c.Verdict, c.OK, want[r.Check][i])
			}
		}
	}
}
