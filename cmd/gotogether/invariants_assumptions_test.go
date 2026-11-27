package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// The model's listed assumptions (ROADMAP CW3): the golden's table, its
// assumption nothing targets failing -require targeted (and exercised,
// for each without a //quint:exercises, with no targeted note), printed
// when no document is given; a directive naming no assumption, one
// naming a property, a property directive naming an assumption or the
// val that states one, each an error at its line; an @assume line that
// declares none fails with no table written, at a literate model's
// document line.
func TestInvariantsAssumptions(t *testing.T) {
	skip.Quint(t)
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errw bytes.Buffer
		n, err := invariants(t.Context(), &out, &errw, args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return n, out.String(), errw.String()
	}
	spec, doc, code := fixture+"/model/counter.qnt", fixture+"/docs/walkthrough.md", fixture+"/counter"
	n, out, errw := run("-spec", spec, "-check", doc, "-require", "targeted", code)
	if n != 1 || !strings.Contains(out, "ok   "+doc+": current") || !strings.HasSuffix(errw, "FAIL assumption capFixed: nothing targets it: put //quint:exercises counter.capFixed on a test or double that goes past it, or //quint:monitors counter.capFixed on the production check that watches it\n") {
		t.Errorf("-require targeted: %d\n%s%s", n, out, errw)
	}
	n, _, errw = run("-spec", spec, "-check", doc, "-require", "exercised", code)
	if n != 1 || strings.Contains(errw, "targeted is not required") || !strings.Contains(errw, "FAIL assumption oneCaller: nothing exercises it: put //quint:exercises counter.oneCaller on a test, a double's fault or a generator that goes past it\nFAIL assumption capFixed: nothing exercises it:") {
		t.Errorf("-require exercised: %d\n%s", n, errw)
	}
	if n, out, _ := run("-spec", spec, code); n != 0 || !strings.Contains(out, "\n| assumption | statement | stated by | exercised by | monitored by |\n") || !strings.Contains(out, "| `noOverflow` | n + 1 never overflows; Go's int wraps past math.MaxInt | `belowMaxInt` | `TestIncPastMaxInt` [") || strings.Contains(out, "\n| `belowMaxInt` |") {
		t.Errorf("printed: %d\n%s", n, out)
	}

	dir := copyFixture(t)
	spec, doc, code = filepath.Join(dir, "model", "counter.qnt"), filepath.Join(dir, "docs", "walkthrough.md"), filepath.Join(dir, "counter")
	edit(t, filepath.Join(code, "counter_test.go"), "//quint:exercises noOverflow\n", "//quint:exercises noOverflw\n")
	edit(t, filepath.Join(code, "counter.go"), "//quint:monitors oneCaller label:counter.busy\n", "//quint:monitors withinCap\n//quint:enforces oneCaller belowMaxInt\n")
	n, out, errw = run("-spec", spec, "-check", doc, code)
	want := filepath.Join(code, "counter.go") + ":44: //quint:monitors names withinCap: withinCap is a property of model counter, not an assumption: //quint:enforces and //quint:checks name properties, //quint:exercises and //quint:monitors what the model leaves out (its // @assume lines): name the assumption it goes past (noOverflow, oneCaller, capFixed), or use //quint:enforces or //quint:checks\n" +
		filepath.Join(code, "counter.go") + ":45: //quint:enforces names oneCaller: oneCaller is an assumption of model counter (what it leaves out), not a property: target it with //quint:exercises oneCaller or //quint:monitors oneCaller\n" +
		filepath.Join(code, "counter.go") + ":45: //quint:enforces names belowMaxInt: belowMaxInt states assumption noOverflow of model counter, which is no property: target the assumption with //quint:exercises noOverflow or //quint:monitors noOverflow (or select belowMaxInt with -props to table it as a property)\n" +
		filepath.Join(code, "counter_test.go") + ":45: //quint:exercises names noOverflw: model counter has no assumption noOverflw: did you mean noOverflow? name one of model counter's assumptions (noOverflow, oneCaller, capFixed), or declare it in the model (// @assume noOverflw: what the model leaves out)\n"
	if n != 1 || errw != want || out != "FAIL 4 annotations naming no property, action or assumption of model counter, or annotating nothing: the tables are not written (fix each as it says)\n" {
		t.Errorf("unknown assumptions: %d\n%s%s\nwant\n%s", n, out, errw, want)
	}
	// an assumption with a property's name: refused, the document kept
	before, _ := os.ReadFile(doc)
	edit(t, spec, "// @assume capFixed:", "// @assume withinCap:")
	n, out, errw = run("-spec", spec, "-write", doc, code)
	if n != 1 || errw != spec+":22: @assume withinCap has the name of model counter's definition counterBase::withinCap: rename the assumption, so that a directive naming it cannot mean a property (withinCapAssumed, say)\n" || out != "FAIL 1 @assume line of model counter declaring no assumption the table can show: no table written (fix each as it says)\n" {
		t.Errorf("a clash: %d\n%s%s", n, out, errw)
	}
	if now, _ := os.ReadFile(doc); !bytes.Equal(now, before) {
		t.Errorf("the document was written")
	}

	// a literate model: the problem at the document's line
	lit := t.TempDir()
	md := filepath.Join(lit, "queue.md")
	src := "# A queue\n\nIt never fills.\n\n```quint\nmodule queue {\n  var n: int\n  /// @assume unboundedQueue: the queue never fills\n  val small = n < 10\n  action init = n' = 0\n  action step = n' = n + 1\n}\n```\n"
	if err := os.WriteFile(md, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	n, out, errw = run("-spec", md, lit)
	if n != 1 || !strings.HasPrefix(errw, md+":8: /// @assume is a doc comment") || !strings.Contains(out, "no table printed") {
		t.Errorf("literate: %d\n%s%s", n, out, errw)
	}
}
