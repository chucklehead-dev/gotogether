package qtest

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qmut"
)

// NoSurvivors names each unmarked survivor with its mark, and each mark
// that matched no survivor.
func TestNoSurvivors(t *testing.T) {
	t.Parallel()
	dropAck := qmut.Mutant{ID: 1, File: "/src/shop/pay.go", Line: 42, Func: "example.com/shop.(Payer).Pay", Op: "drop call", Code: "ack()"}
	boundary := qmut.Mutant{ID: 2, File: "/src/shop/retry.go", Line: 7, Func: "example.com/shop.retry", Op: "<= for <", Code: "n < 3"}
	rep := &MutationReport{Survived: []qmut.Mutant{dropAck, boundary}}
	f := &failTB{TB: t}
	rep.Expect(f, NoSurvivors("pay.go:42 drop call", "retry.go:7"))
	if len(f.errs) != 0 {
		t.Errorf("both marked: %v", f.errs)
	}
	f = &failTB{TB: t}
	rep.Expect(f, NoSurvivors("pay.go:42", "stock.go:9"))
	if len(f.errs) != 2 || !strings.Contains(f.errs[0], `NoSurvivors("retry: <= for <: n < 3")`) || !strings.Contains(f.errs[1], "stock.go:9") {
		t.Errorf("an unmarked survivor and a stale mark: %v", f.errs)
	}
	// by function, change and code, which a line added above does not move
	moved := dropAck
	moved.Line = 50
	f = &failTB{TB: t}
	(&MutationReport{Survived: []qmut.Mutant{moved}}).Expect(f, NoSurvivors("(Payer).Pay: drop call: ack()"))
	if len(f.errs) != 0 {
		t.Errorf("marked by function and code: %v", f.errs)
	}
	// a mark that names two mutants of one text is refused
	twin := moved
	twin.ID, twin.Line = 3, 60
	f = &failTB{TB: t}
	(&MutationReport{Survived: []qmut.Mutant{moved}, Killed: []qmut.Mutant{twin}}).Expect(f, NoSurvivors("(Payer).Pay: drop call: ack()"))
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], `names 2 mutants (pay.go:50, pay.go:60): mark the equivalent one by its position, "pay.go:50"`) {
		t.Errorf("an ambiguous mark: %v", f.errs)
	}
}
