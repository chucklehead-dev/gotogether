package model

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// veilFixtures are quint 0.35.0's --out results (modules and tables cut)
// and ITF traces, captured from real runs of `quint verify --backend veil
// --inductive-invariant` on testdata/veil's models (undecided.json is
// written in the shape verifyVeil.js gives it), by inductive invariant.
var veilFixtures = map[string]struct {
	result, itf string
	exit        int
}{
	"nonNegative":                       {"bank.json", "", 0},
	"xNonNegative":                      {"cti.json", "cti.itf.json", 1},
	"xNonNegative and yNonNegative":     {"lemma.json", "", 0},
	"xBounded":                          {"lemma.json", "", 0},
	"settledCorrectly and neverSettled": {"vac.json", "", 0},
	"settledCorrectly":                  {"vac.json", "", 0},
	"xPositive":                         {"initcti.json", "initcti.itf.json", 1},
	"yNonNegative":                      {"undecided.json", "", 1},
	"exactlyOnce":                       {"refused.json", "", 1},
	"xSmall":                            {"lemma.json", "", 1},
	"xNonNegative and nope":             {"unknown.json", "", 1},
	// written in the shape verifyVeil.js gives a counterexample Veil
	// cannot decode (Induction.lean's statesOf and map reader)
	"xNonNegative and xSmall": {"nopost.json", "", 1},
	"xSmall and xNonNegative": {"mapdomain.json", "", 1},
	"bounded":                 {"lemma.json", "", 0},
}

// fakeVeil is a quint standing in for quint 0.35's Veil backend: its
// version is version, `quint veil status` answers ready when ready, and
// `quint verify --backend veil` writes the fixture of its inductive
// invariant; anything else goes to the real quint, when there is one
// (the vacuity check's search of the instance). Each verify appends its
// argv to the returned log.
func fakeVeil(t *testing.T, version string, ready bool) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake Quint uses POSIX shell")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	fixtures, _ := filepath.Abs("testdata/veil")
	real, _ := exec.LookPath("quint")
	log := filepath.Join(dir, "argv.log")
	var cases strings.Builder
	for inv, f := range veilFixtures {
		cases.WriteString(" '" + inv + "')\n  cp " + fixtures + "/" + f.result + " \"$out\"\n")
		if f.itf != "" {
			cases.WriteString("  cp " + fixtures + "/" + f.itf + " \"$itf\"\n")
		}
		cases.WriteString("  exit " + string(rune('0'+f.exit)) + ";;\n")
	}
	status := "echo 'not installed /home/u/.quint/veil/workspace-1f711585ad60c8a2'; exit 1"
	if ready {
		status = "echo 'ready /home/u/.quint/veil/workspace-1f711585ad60c8a2'; exit 0"
	}
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then sleep ${FAKE_VERSION_SLEEP:-0}; echo " + version + "; exit 0; fi\n" +
		"if [ \"$1\" = veil ] && [ \"$2\" = status ]; then " + status + "; fi\n" +
		"if [ \"$1\" = verify ] && [ \"$2\" = --backend ] && [ \"$3\" = veil ]; then\n echo \"$*\" >> " + log + "\n inv=''; out=''; itf=''\n" +
		" while [ $# -gt 0 ]; do case \"$1\" in --inductive-invariant) inv=$2; shift;; --out) out=$2; shift;; --out-itf) itf=$2; shift;; esac; shift; done\n" +
		" case \"$inv\" in\n" + cases.String() + " esac\n echo \"no fixture for $inv\" >&2; exit 3\nfi\n"
	if real != "" {
		script += "exec " + real + " \"$@\"\n"
	} else {
		script += "echo 'no real quint for' \"$@\" >&2; exit 3\n"
	}
	bin := filepath.Join(dir, "quint")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func inductive(t *testing.T, quint, b string, reqs ...InductiveRequest) *Report {
	t.Helper()
	rep, err := Simulate(t.Context(), "testdata/veil/"+b, SimulateOptions{Quint: quint, Inductive: reqs, InductiveOut: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// Each outcome is its own, said as such, and none but proved passes:
// bank's nonNegative is proved as it is (with the keys Veil added); a
// counterexample to induction names the action, shows both states and
// keeps the ITF; init's counterexample says the state is reachable; a
// refusal gives Veil's reason without the Lean file's path; an undecided
// check gives the solver's.
func TestInductiveOutcomes(t *testing.T) {
	quint, log := fakeVeil(t, "0.35.0", true)
	rep := inductive(t, quint, "bank.yaml", InductiveRequest{Name: "nonNegative"})
	v := rep.Inductive[0]
	if v.Outcome != InductiveProved || rep.Code != 0 || !slices.Equal(v.Added, []string{"balances.keys() == ACCOUNTS"}) || v.Module != "bank" {
		t.Errorf("bank: %+v (code %d)\n%s", v, rep.Code, rep.Text)
	}
	if !strings.Contains(rep.Text, "proved   nonNegative: inductive, so it holds in every reachable state (the module has no constants;") ||
		!strings.Contains(rep.Text, "Veil added balances.keys() == ACCOUNTS from init") {
		t.Errorf("bank's report:\n%s", rep.Text)
	}
	argv, _ := os.ReadFile(log)
	if !strings.Contains(string(argv), "verify --backend veil --main bank --inductive-invariant nonNegative --out ") {
		t.Errorf("argv: %s", argv)
	}

	rep = inductive(t, quint, "induct.yaml", InductiveRequest{Name: "xNonNegative"}, InductiveRequest{Name: "xPositive"}, InductiveRequest{Name: "yNonNegative"})
	if rep.Code != 2 {
		t.Errorf("code %d, want 2 (no verdict, never 1)", rep.Code)
	}
	cti, init, undecided := rep.Inductive[0], rep.Inductive[1], rep.Inductive[2]
	if cti.Outcome != InductiveCounterexample || cti.Action != "shift" || cti.Broken != "the inductive invariant" ||
		!slices.Equal(cti.States, []string{"x = 0, y = -1, settled = true", "x = -1, y = 0, settled = true"}) {
		t.Errorf("cti: %+v", cti)
	}
	if _, err := os.Stat(cti.ITF); err != nil {
		t.Errorf("the ITF is not kept: %v", err)
	}
	for _, want := range []string{
		"no verdict xNonNegative: not inductive: a counterexample to induction after shift breaks the inductive invariant (xNonNegative may still hold: add a lemma, simulate.inductive.lemmas or qtest.Inductive(...).With)",
		"    before: x = 0, y = -1, settled = true\n    after shift: x = -1, y = 0, settled = true\n    ITF: " + cti.ITF,
		"no verdict xPositive: not inductive: a counterexample to induction at the initializer",
		"a state init reaches is outside xPositive: if xPositive itself fails there the model violates it (simulate -graph shows it)",
		"    init: x = 0, y = 0, settled = false",
		"no verdict yNonNegative: Veil's solver decided neither way: the inductive invariant after shift: unknown: incomplete quantifier instantiation",
	} {
		if !strings.Contains(rep.Text, want) {
			t.Errorf("report lacks %q:\n%s", want, rep.Text)
		}
	}
	if init.Outcome != InductiveCounterexample || init.Action != "" || len(init.States) != 1 {
		t.Errorf("init cti: %+v", init)
	}
	if undecided.Outcome != InductiveUndecided {
		t.Errorf("undecided: %+v", undecided)
	}

	rep = inductive(t, quint, "refused.yaml", InductiveRequest{Name: "exactlyOnce"})
	if v := rep.Inductive[0]; v.Outcome != InductiveRefused || v.Reason != "the size of a set whose elements are not known is not supported" || rep.Code != 2 {
		t.Errorf("refused: %+v (code %d)", v, rep.Code)
	}
	if !strings.Contains(rep.Text, "refused  exactlyOnce: not in Veil's fragment: the size of a set whose elements are not known is not supported (no verdict, not a failure: gotogether lint -veil names the idioms") {
		t.Errorf("refused's report:\n%s", rep.Text)
	}
	d := rep.Doc()
	if len(d.Checks) != 1 || d.Checks[0].Kind != "inductive" || d.Checks[0].Inductive == nil || d.Checks[0].Inductive.Outcome != InductiveRefused || d.Verdict != "no verdict" {
		t.Errorf("refused's document: %+v", d)
	}
}

// A counterexample to induction Veil cannot decode (no post-state in the
// solver's model, a map keyed by int) is a counterexample all the same:
// no verdict, Veil's reason in place of the states, never a run error.
func TestInductiveCounterexampleUnshown(t *testing.T) {
	quint, _ := fakeVeil(t, "0.35.0", true)
	rep := inductive(t, quint, "induct.yaml",
		InductiveRequest{Name: "xNonNegative", Lemmas: []string{"xSmall"}}, InductiveRequest{Name: "xSmall", Lemmas: []string{"xNonNegative"}})
	if rep.Code != 2 {
		t.Errorf("code %d, want 2 (no verdict)", rep.Code)
	}
	for i, want := range []string{
		"the model has no post-state; a complete transition witness could not be decoded",
		"the model's map domain jobs has non-enumerable keys",
	} {
		v := rep.Inductive[i]
		if v.Outcome != InductiveCounterexample || v.Action != "shift" || v.Broken != "the inductive invariant" || v.Reason != want || len(v.States) != 0 || v.ITF != "" {
			t.Errorf("%s: %+v", v.Invariant, v)
		}
	}
	for _, want := range []string{
		"  no verdict xNonNegative with xSmall: not inductive: a counterexample to induction after shift breaks the inductive invariant (xNonNegative may still hold: add a lemma, simulate.inductive.lemmas or qtest.Inductive(...).With)\n" +
			"    its states: Veil could not show them: the model has no post-state; a complete transition witness could not be decoded (simulate -graph says whether the instance reaches a state that breaks it)\n",
		"    its states: Veil could not show them: the model's map domain jobs has non-enumerable keys (Veil reads no map keyed by int or a constant's type: keyed by str or an enumeration it shows the states; simulate -graph says whether the instance reaches a state that breaks it)\n",
	} {
		if !strings.Contains(rep.Text, want) {
			t.Errorf("report lacks %q:\n%s", want, rep.Text)
		}
	}
}

// A proof claims what it proved: for every value of the constants only
// when the module leaves them open; for an instance module, for its
// constants, named; for one that fixes some, those named and the others
// open; for a module with none, no constants at all.
func TestInductiveScope(t *testing.T) {
	quint, _ := fakeVeil(t, "0.35.0", true)
	for _, tc := range []struct{ binding, header, proved string }{
		{"instance-open.yaml", "counter by induction with Veil (quint verify --backend veil), for every value of the constants MAX, ACCOUNTS\n",
			"proved   bounded: inductive, so it holds in every reachable state for every value of the constants MAX, ACCOUNTS ("},
		{"instance.yaml", `counterInstance by induction with Veil (quint verify --backend veil), for this instance's constants only (MAX = 3, ACCOUNTS = Set("a", "b"))`,
			`proved   bounded: inductive, so it holds in every reachable state for this instance's constants only (MAX = 3, ACCOUNTS = Set("a", "b")), not for every value of them (`},
		{"instance-partial.yaml", `counterLimit by induction with Veil (quint verify --backend veil), for every value of the constants LIMIT, with MAX = LIMIT, ACCOUNTS = Set("a", "b")`,
			`proved   bounded: inductive, so it holds in every reachable state for every value of the constants LIMIT, with MAX = LIMIT, ACCOUNTS = Set("a", "b") (`},
		{"bank.yaml", "bank by induction with Veil (quint verify --backend veil), the module has no constants",
			"proved   nonNegative: inductive, so it holds in every reachable state (the module has no constants; "},
	} {
		name := "bounded"
		if tc.binding == "bank.yaml" {
			name = "nonNegative"
		}
		rep := inductive(t, quint, tc.binding, InductiveRequest{Name: name})
		if !strings.Contains(rep.Text, tc.header) || !strings.Contains(rep.Text, tc.proved) {
			t.Errorf("%s: want %q and %q:\n%s", tc.binding, tc.header, tc.proved, rep.Text)
		}
	}
	rep := inductive(t, quint, "instance.yaml", InductiveRequest{Name: "bounded"})
	if v := rep.Inductive[0]; len(v.Open) != 0 || !slices.Equal(v.Fixed, []string{"MAX = 3", `ACCOUNTS = Set("a", "b")`}) {
		t.Errorf("instance: open %v, fixed %v", v.Open, v.Fixed)
	}
	if d := rep.Doc(); d.Checks[0].Inductive == nil || !slices.Equal(d.Checks[0].Inductive.Fixed, []string{"MAX = 3", `ACCOUNTS = Set("a", "b")`}) {
		t.Errorf("instance's document: %+v", d.Checks[0].Inductive)
	}
}

// Lemmas join the invariant as its inductive invariant, the binding's or
// the request's; an unknown lemma is an error naming the module, not an
// outcome.
func TestInductiveLemmas(t *testing.T) {
	quint, log := fakeVeil(t, "0.35.0", true)
	rep := inductive(t, quint, "induct.yaml", InductiveRequest{Name: "xNonNegative", Lemmas: []string{"yNonNegative"}})
	if v := rep.Inductive[0]; v.Outcome != InductiveProved || v.Invariant != "xNonNegative and yNonNegative" {
		t.Errorf("with a lemma: %+v", v)
	}
	if !strings.Contains(rep.Text, "proved   xNonNegative with yNonNegative: inductive") {
		t.Errorf("report:\n%s", rep.Text)
	}
	argv, _ := os.ReadFile(log)
	if !strings.Contains(string(argv), "--inductive-invariant xNonNegative and yNonNegative --out") {
		t.Errorf("argv: %s", argv)
	}
	_, err := Simulate(t.Context(), "testdata/veil/induct.yaml", SimulateOptions{Quint: quint, Inductive: []InductiveRequest{{Name: "xNonNegative", Lemmas: []string{"nope"}}}})
	if err == nil || !strings.Contains(err.Error(), "[QNT404] Name 'nope' not found") || !strings.Contains(err.Error(), "is a val of module induct (simulate.inductive.module, its lemmas simulate.inductive.lemmas)") {
		t.Errorf("unknown lemma: %v", err)
	}
	for _, tc := range []struct{ name, want string }{
		{"nonNegative", "nonNegative is not in testdata/veil/induct.yaml's simulate.holds"},
	} {
		_, err := Simulate(t.Context(), "testdata/veil/induct.yaml", SimulateOptions{Quint: quint, Inductive: []InductiveRequest{{Name: tc.name}}})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if _, err := Simulate(t.Context(), "testdata/veil/induct.yaml", SimulateOptions{Quint: quint, Graph: true, Inductive: []InductiveRequest{{Name: "xNonNegative"}}}); err == nil || !strings.Contains(err.Error(), "pick one") {
		t.Errorf("-graph with -inductive: %v", err)
	}
}

// Without quint 0.35, or without Veil, nothing is proved and nothing
// fails: not done, saying how to install it, never installing it.
func TestInductiveNotDone(t *testing.T) {
	for _, tc := range []struct {
		version string
		ready   bool
		want    string
	}{
		{"0.33.1", true, "quint 0.33.1 has no Veil backend: install quint ≥ 0.35.0 (npm i -g @informalsystems/quint@0.35.0), then Veil: install it once with `quint veil install` (about 5 GB and 10 minutes"},
		{"0.35.0", false, "Veil is not installed (quint veil status: not installed /home/u/.quint/veil/workspace-1f711585ad60c8a2): install it once with `quint veil install` (about 5 GB and 10 minutes; QUINT_HOME chooses where); gotogether never installs it"},
	} {
		quint, log := fakeVeil(t, tc.version, tc.ready)
		rep := inductive(t, quint, "bank.yaml", InductiveRequest{Name: "nonNegative"})
		if v := rep.Inductive[0]; v.Outcome != InductiveNotDone || !strings.Contains(v.Reason, tc.want) || rep.Code != 2 || len(rep.NotDone) != 1 {
			t.Errorf("%s ready=%v: %+v, code %d, not done %v", tc.version, tc.ready, v, rep.Code, rep.NotDone)
		}
		if argv, _ := os.ReadFile(log); len(argv) > 0 {
			t.Errorf("%s ready=%v: verify ran: %s", tc.version, tc.ready, argv)
		}
	}
}

// A quint that does not answer --version in time is there, on a loaded
// machine: an error naming the timeout and how to raise it, never "no
// quint" (not done), which a test would skip. A quint that is not there
// is not done.
func TestInductiveProbeTimeout(t *testing.T) {
	quint, log := fakeVeil(t, "0.35.0", true)
	t.Setenv("FAKE_VERSION_SLEEP", "3")
	t.Setenv(quintcmd.ProbeTimeoutEnv, "300ms")
	_, err := Simulate(t.Context(), "testdata/veil/bank.yaml", SimulateOptions{Quint: quint, Inductive: []InductiveRequest{{Name: "nonNegative"}}})
	want := "simulate -inductive: cannot ask quint whether Veil is installed: " + quint + " --version did not answer within 300ms (" + quintcmd.ProbeTimeoutEnv + "=300ms): a loaded machine; raise it"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a slow quint: %v, want %q", err, want)
	}
	if argv, _ := os.ReadFile(log); len(argv) > 0 {
		t.Errorf("verify ran: %s", argv)
	}
	missing := filepath.Join(t.TempDir(), "quint")
	rep := inductive(t, missing, "bank.yaml", InductiveRequest{Name: "nonNegative"})
	if v := rep.Inductive[0]; v.Outcome != InductiveNotDone || !strings.HasPrefix(v.Reason, "no quint (") || !strings.Contains(v.Reason, "install quint ≥ 0.35.0") {
		t.Errorf("no quint: %+v", v)
	}
}

// A proof of an implication is checked for vacuity: a lemma negating its
// antecedent and a settle flag step never sets warn, and so does an
// antecedent the instance's complete graph never reaches; a reached one
// is said with its witness, and warns of nothing.
func TestInductiveVacuity(t *testing.T) {
	skip.Quint(t) // the instance's search compiles the model with the real quint
	quint, _ := fakeVeil(t, "0.35.0", true)
	rep := inductive(t, quint, "induct.yaml", InductiveRequest{Name: "settledCorrectly"}, InductiveRequest{Name: "xBounded"})
	vac, bounded := rep.Inductive[0], rep.Inductive[1]
	if vac.Outcome != InductiveProved || vac.Antecedent != "settled" || vac.Reached != "unreached" || len(vac.Vacuous) != 3 {
		t.Fatalf("settledCorrectly: %+v\n%s", vac, rep.Text)
	}
	for i, want := range []string{
		"settledCorrectly is conditioned on settled, which init and step never set true",
		"the lemma neverSettled negates settledCorrectly's antecedent settled: the proof rests on settled never holding",
		"no state of the instance reaches settledCorrectly's antecedent settled",
	} {
		if !strings.Contains(vac.Vacuous[i], want) {
			t.Errorf("warning %d: %q, want %q", i, vac.Vacuous[i], want)
		}
	}
	if !strings.Contains(rep.Text, "    warning: vacuous? the lemma neverSettled negates") {
		t.Errorf("report:\n%s", rep.Text)
	}
	if bounded.Outcome != InductiveProved || bounded.Antecedent != "x > 0" || bounded.Reached != "reached" || len(bounded.Vacuous) != 0 {
		t.Errorf("xBounded: %+v\n%s", bounded, rep.Text)
	}
	if rep.Code != 0 {
		t.Errorf("code %d: a vacuity warning is a warning", rep.Code)
	}
}

// The binding's lemmas must belong to holds invariants, and be names.
func TestInductiveBindingLemmas(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ yaml, want string }{
		{"simulate: {holds: [a], inductive: {lemmas: {b: [c]}}}", "simulate.inductive.lemmas names b, which is not in simulate.holds (a)"},
		{"simulate: {holds: [a], inductive: {lemmas: {a: ['x > 0']}}}", `simulate.inductive.lemmas.a: "x > 0" is not a name: a lemma is a val of m (write the expression as a val)`},
	} {
		p := filepath.Join(dir, "b.yaml")
		if err := os.WriteFile(p, []byte("spec: m.qnt\nmodule: m\ninvariants: [a]\n"+tc.yaml+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := binding.Load(p); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.yaml, err)
		}
	}
}

// With quint 0.35 and Veil installed (QUINT_HOME, `quint veil status`
// ready), the same outcomes come from Veil itself. Skipped otherwise,
// even under GOTOGETHER_STRICT: CI does not install Veil (5 GB).
func TestInductiveRealVeil(t *testing.T) {
	if testing.Short() {
		t.Skip("Veil takes seconds a proof, minutes on its first")
	}
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("no quint")
	}
	if missing, err := VeilStatus(t.Context(), "quint", "."); err != nil || missing != "" {
		t.Skipf("no Veil: %s %v", missing, err)
	}
	rep := inductive(t, "quint", "bank.yaml", InductiveRequest{Name: "nonNegative"})
	if v := rep.Inductive[0]; v.Outcome != InductiveProved {
		t.Errorf("bank: %+v\n%s", v, rep.Text)
	}
	rep = inductive(t, "quint", "induct.yaml", InductiveRequest{Name: "xNonNegative"}, InductiveRequest{Name: "xNonNegative", Lemmas: []string{"yNonNegative"}}, InductiveRequest{Name: "settledCorrectly"})
	if v := rep.Inductive[0]; v.Outcome != InductiveCounterexample || v.Action != "shift" || len(v.States) != 2 {
		t.Errorf("cti: %+v\n%s", v, rep.Text)
	}
	if v := rep.Inductive[1]; v.Outcome != InductiveProved {
		t.Errorf("with the lemma: %+v\n%s", v, rep.Text)
	}
	// the IR's two warnings and the instance's complete graph's
	if v := rep.Inductive[2]; v.Outcome != InductiveProved || v.Reached != "unreached" || len(v.Vacuous) != 3 {
		t.Errorf("vacuous: %+v\n%s", v, rep.Text)
	}
	rep = inductive(t, "quint", "refused.yaml", InductiveRequest{Name: "exactlyOnce"})
	if v := rep.Inductive[0]; v.Outcome != InductiveRefused || !strings.Contains(v.Reason, "the size of a set") {
		t.Errorf("refused: %+v\n%s", v, rep.Text)
	}
}

// A counterexample at the initializer is init's whatever the order of
// Veil's checks; a keys invariant Veil added is named by its text.
func TestInductiveCounterexampleWhere(t *testing.T) {
	type check = veilCheck
	res := veilResult{InductionChecks: []check{{"q::inductiveInv", "shift", "disproven"}, {"q::inductiveInv", "initializer", "disproven"}}}
	if broken, action := ctiWhere(res); action != "" || broken != "the inductive invariant" {
		t.Errorf("init after shift: %q %q", broken, action)
	}
	res = veilResult{AddedInvariants: []string{"balances.keys() == ACCOUNTS"}, InductionChecks: []check{{"q::inductiveInv", "initializer", "proven"}, {"q::keys::balances", "deposit", "disproven"}}}
	if broken, action := ctiWhere(res); action != "deposit" || broken != "balances.keys() == ACCOUNTS (Veil added it from init: state the keys of balances in a lemma)" {
		t.Errorf("keys: %q %q", broken, action)
	}
}

// A result that says ok from a quint that exited 1 is no proof: an error.
func TestInductiveContradictoryExit(t *testing.T) {
	quint, _ := fakeVeil(t, "0.35.0", true)
	_, err := Simulate(t.Context(), "testdata/veil/induct.yaml", SimulateOptions{Quint: quint, Inductive: []InductiveRequest{{Name: "xSmall"}}})
	if err == nil || !strings.Contains(err.Error(), `status "ok"`) {
		t.Errorf("ok with exit 1: %v", err)
	}
}
