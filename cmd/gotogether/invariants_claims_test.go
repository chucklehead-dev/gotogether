package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// A design document's marked claims (ROADMAP CW3's doc lint): the
// fixture's document passes, its claims each citing the model, under
// -require cited too, with no table printed; a claim citing nothing, a
// property or an assumption the model lacks (or of the other kind, or
// the val that states an assumption, or no boolean val), and an empty
// citation each fail at the claim's line with the fix; a cited property
// nothing enforces or checks and a cited assumption nothing targets are
// notes, FAIL lines under -require cited, and a cited val a binding
// leaves out is named; -claims beside -check checks both.
func TestInvariantsClaims(t *testing.T) {
	skip.Quint(t)
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errw bytes.Buffer
		n, err := invariants(t.Context(), &out, &errw, args)
		if err != nil {
			return 2, "", err.Error()
		}
		return n, out.String(), errw.String()
	}
	spec, code, design := fixture+"/model/counter.qnt", fixture+"/counter", fixture+"/docs/design.md"
	for _, req := range []string{"", "cited"} {
		n, out, errw := run("-spec", spec, "-claims", design, "-require", req, code)
		if n != 0 || out != "ok   "+design+": 3 claims, citing 2 properties and 2 assumptions of model counter\n" || errw != "" {
			t.Errorf("the fixture's claims, -require %q: %d\n%s%s", req, n, out, errw)
		}
	}

	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.md")
	src := "# Bad\n\n" +
		"Claim: the counter is fast.\n\n" + // 3
		"Claim: it stays under its cap (prop: withinCapp).\n\n" + // 5
		"Claim: one caller (prop: oneCaller).\n\n" + // 7
		"Claim: within the cap (assume: withinCap).\n\n" + // 9
		"Claim: no overflow (prop: belowMaxInt).\n\n" + // 11
		"Claim: the cap is fixed (assume: capFixd).\n\n" + // 13
		"Claim: it reaches its cap (prop: atCap), whose cap is fixed (assume: capFixed).\n\n" + // 15
		"Claim: nothing named (prop: ).\n\n" + // 17
		"Claim: doubled (prop: twice).\n\n" + // 19
		"Claim: the cap holds (prop: counterBase::withinCap).\n" // 21
	if err := os.WriteFile(bad, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	at := func(line string) string { return bad + ":" + line + ": claim " }
	wantErrs := at("3") + `"the counter is fast." cites no property or assumption: cite the property that makes it true, (prop: name), or the assumption it rests on, (assume: name); model counter's properties are atCap, nonNegative, withinCap, its assumptions noOverflow, oneCaller, capFixed; if the model has neither, add it to the model, or the sentence is no claim (drop Claim:)` + "\n" +
		at("5") + `"it stays under its cap." cites property withinCapp: model counter has no property withinCapp: did you mean withinCap? cite one of atCap, nonNegative, withinCap, or state the claim as a val in the model` + "\n" +
		at("7") + `"one caller." cites property oneCaller: oneCaller is an assumption of model counter (what it leaves out), not a property: cite it as (assume: oneCaller)` + "\n" +
		at("9") + `"within the cap." cites assumption withinCap: withinCap is a property of model counter, not an assumption: cite it as (prop: withinCap)` + "\n" +
		at("11") + `"no overflow." cites property belowMaxInt: belowMaxInt states assumption noOverflow of model counter, which is no property: cite the assumption, (assume: noOverflow)` + "\n" +
		at("13") + `"the cap is fixed." cites assumption capFixd: model counter has no assumption capFixd: did you mean capFixed? cite one of model counter's assumptions (noOverflow, oneCaller, capFixed), or declare it in the model (// @assume capFixd: what the model leaves out)` + "\n" +
		at("17") + `"nothing named.": (prop: ) names nothing: give the names it cites, (prop: name, ...)` + "\n" +
		at("19") + `"doubled." cites property twice: twice is no boolean val of model counter (a property is one): cite one of atCap, nonNegative, withinCap, or state the claim as a val in the model` + "\n"
	unbacked := []string{
		at("15") + `"it reaches its cap, whose cap is fixed." cites property atCap, which nothing enforces and no test checks: put //quint:enforces atCap on the code that does, and //quint:checks atCap on the test`,
		at("15") + `"it reaches its cap, whose cap is fixed." cites assumption capFixed, which nothing targets: put //quint:exercises capFixed on a test or double that goes past it, or //quint:monitors capFixed on the production check that watches it`,
	}
	wantOut := "FAIL " + bad + ": 8 problems in its claims, each citing nothing or what model counter lacks (fix each as it says)\n"
	n, out, errw := run("-spec", spec, "-claims", bad, code)
	if want := wantErrs + "note: " + unbacked[0] + " (cited is not required)\nnote: " + unbacked[1] + " (cited is not required)\n"; n != 1 || out != wantOut || errw != want {
		t.Errorf("bad claims: %d\n%s%s\nwant\n%s%s", n, out, errw, wantOut, want)
	}
	n, out, errw = run("-spec", spec, "-claims", bad, "-require", "cited", code)
	if want := wantErrs + "FAIL " + unbacked[0] + "\nFAIL " + unbacked[1] + "\n"; n != 1 || out != wantOut || errw != want {
		t.Errorf("bad claims, -require cited: %d\n%s%s\nwant\n%s", n, out, errw, want)
	}

	// only the unbacked claim: a note passes, -require cited fails it
	backed := filepath.Join(dir, "unbacked.md")
	if err := os.WriteFile(backed, []byte("Claim: it reaches its cap (prop: atCap), whose cap is fixed (assume: capFixed).\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, out, errw := run("-spec", spec, "-claims", backed, code); n != 0 || !strings.HasPrefix(out, "ok   "+backed+": 1 claim, citing 1 property and 1 assumption") || strings.Count(errw, "note: ") != 2 {
		t.Errorf("unbacked, not required: %d\n%s%s", n, out, errw)
	}
	if n, out, errw := run("-spec", spec, "-claims", backed, "-require", "cited", code); n != 1 || strings.Count(errw, "FAIL ") != 2 || out != "FAIL "+backed+": 2 citations the code does not back (-require cited; fix each as it says)\n" {
		t.Errorf("unbacked, required: %d\n%s%s", n, out, errw)
	}
	// a val the binding does not check: no row to back it
	if n, _, errw := run("-binding", fixture+"/counter.binding.yaml", "-claims", backed, "-require", "cited", code); n != 1 || !strings.Contains(errw, `FAIL `+backed+`:1: claim "it reaches its cap, whose cap is fixed." cites property atCap, which this table leaves out (the binding does not check it, or -props, -invariant or -exclude left it out): add it to the selection`) {
		t.Errorf("left out by the binding: %d\n%s", n, errw)
	}

	// beside -check: the tables checked as before, and the claims
	n, out, _ = run("-spec", spec, "-check", fixture+"/docs/walkthrough.md", "-claims", design+","+bad, code)
	if n != 1 || !strings.Contains(out, "ok   "+fixture+"/docs/walkthrough.md: current") || !strings.Contains(out, "ok   "+design+": 3 claims") || !strings.Contains(out, wantOut) {
		t.Errorf("with -check: %d\n%s", n, out)
	}

	// bad invocations
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-spec", spec, "-require", "cited", code}, "invariants: -require cited is of the claims a design document marks: name the documents with -claims doc.md"},
		{[]string{"-spec", spec, "-claims", design, "-require", "enforced", code}, "invariants: -require enforced is of the tables, and -claims alone prints none: require only cited of the claims, or give -check doc.md with the document's tables"},
		{[]string{"-spec", spec, "-claims", filepath.Join(dir, "none.md"), code}, "invariants: -claims " + filepath.Join(dir, "none.md") + ": "},
	} {
		if n, _, errw := run(c.args...); n != 2 || !strings.HasPrefix(errw, c.want) {
			t.Errorf("%v: %d %s\nwant %s", c.args, n, errw, c.want)
		}
	}
}
