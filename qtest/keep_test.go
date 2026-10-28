package qtest

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/validate"
)

// failureIn runs f in a subtest with a failTB and returns its failure
// messages once the subtest has ended, its temporary directory removed.
func failureIn(t *testing.T, f func(t *testing.T, ft *failTB)) string {
	t.Helper()
	var msg string
	t.Run("sub", func(t *testing.T) {
		ft := &failTB{TB: t}
		done := make(chan struct{})
		go func() { defer close(done); f(t, ft) }() //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		<-done
		msg = strings.Join(ft.errs, "\n")
	})
	return msg
}

// reportedPaths are the files a report names: its step logs, ITF and
// modules.
func reportedPaths(msg string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`(?m)(?:step log|model-level ITF|module): (\S+)$`).FindAllStringSubmatch(msg, -1) {
		out = append(out, strings.Split(m[1], ",")...)
	}
	return out
}

// A failing Record's report names its step log, ITF and module, which
// were in the test's t.TempDir(): they are copied out before the test
// ends, and the report names the copies, which outlive it.
func TestFailureReportsNameFilesThatOutliveTheTest(t *testing.T) {
	RequireQuint(t)
	if os.Getenv("GOTOGETHER_KEEP") != "" {
		t.Skip("GOTOGETHER_KEEP keeps the files already")
	}
	b := "../validate/testdata/account.yaml"
	overdraw := func(r *qobs.Recorder) {
		r.Record(context.Background(), "deposit", nil, "amount", 5, "obs.balance", 5)
		r.Record(context.Background(), "withdraw", nil, "amount", 9, "obs.balance", -4)
	}
	for name, f := range map[string]func(t *testing.T, ft *failTB){
		"Run.Expect": func(t *testing.T, ft *failTB) { Record(t, b, overdraw).Expect(ft, Conforms()) },
		"Runs": func(t *testing.T, ft *failTB) {
			Runs(Record(t, b, overdraw)).Expect(ft, All(Conforms()))
		},
	} {
		msg := failureIn(t, f)
		paths := reportedPaths(msg)
		if len(paths) < 2 || !strings.Contains(msg, "copied out of the test's temporary directory, which the test's end removes, into "+filepath.Join(os.TempDir(), "gotogether")+", which stays") {
			t.Fatalf("%s: want a report naming its files and where they were copied, got:\n%s", name, msg)
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("%s: the report names %s, gone after the test: %v\n%s", name, p, err, msg)
			}
		}
		for _, p := range paths {
			os.RemoveAll(keptRoot(p))
		}
	}
}

// keptRoot is the copy's directory of a kept path (os.TempDir()/gotogether-*).
func keptRoot(p string) string {
	rest, ok := strings.CutPrefix(p, os.TempDir()+string(os.PathSeparator))
	if !ok {
		return p
	}
	root := filepath.Join(os.TempDir(), "gotogether")
	if rest, ok := strings.CutPrefix(p, root+string(os.PathSeparator)); ok { // keptDir's
		return filepath.Join(root, strings.SplitN(rest, string(os.PathSeparator), 2)[0])
	}
	return os.TempDir() + string(os.PathSeparator) + strings.SplitN(rest, string(os.PathSeparator), 2)[0]
}

// A claim naming what the run cannot have says so, with the name it most
// likely meant: "want a violation of nonNegativ, got: ..." once read as
// the code's failure.
func TestRunClaimNamesSayWhyTheyNeverMatch(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	r := Record(t, "../validate/testdata/account.yaml", func(r *qobs.Recorder) {
		r.Record(context.Background(), "deposit", nil, "amount", 5, "obs.balance", 5)
	})
	for _, c := range []struct {
		e    Expectation
		want string
	}{
		{Violates("nonNegativ"), "nonNegativ is not an invariant of the run (did you mean nonNegative?; checked: nonNegative, underLimit)"},
		{Rejects("withdrw"), "withdrw is not an action of the model (did you mean withdraw?"},
		{Rejects("zzz"), "zzz is not an action of the model (its actions: "},
		// a name of the other kind, said as such: a lax "did you mean"
		// once offered an unrelated name instead
		{Violates("withdraw"), `withdraw is an action, not an invariant (Rejects("withdraw") or Mismatch("withdraw") name an action`},
		{Mismatch("nonNegative"), `nonNegative is an invariant, not an action (Violates("nonNegative") names an invariant`},
		{Rejects("dep"), "dep is not an action of the model (its actions: "}, // not "did you mean"
	} {
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { defer close(done); r.Expect(f, c.e) }() //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		<-done
		if msg := strings.Join(f.errs, "\n"); len(f.errs) != 1 || !strings.Contains(msg, c.want) || strings.Contains(msg, "got:") {
			t.Errorf("%s: want a failure saying %q, got %v", c.e, c.want, f.errs)
		}
	}
}

// A claim naming an eventual val without its prefix says the name to
// write: the verdict of an eventual val is "eventually <val>".
func TestClaimNamesEventualVal(t *testing.T) {
	t.Parallel()
	got := unknownNames([]Expectation{Violates("allPosted")}, map[string]bool{"post": true},
		map[string]bool{"noOverdraft": true, validate.EventuallyName("allPosted"): true}, "the runs")
	if len(got) != 1 || !strings.Contains(got[0], `Violates(validate.EventuallyName("allPosted"))`) {
		t.Errorf("Violates(\"allPosted\") of an eventual val: %v, want the claim to write", got)
	}
}
