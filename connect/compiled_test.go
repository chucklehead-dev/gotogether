package connect

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/itf"
)

// noQuint hides quint (an empty PATH) and asks for compiled models
// (GOTOGETHER_COMPILED=1): what a consumer's CI without Node runs. Not
// parallel: it sets the process's environment.
func noQuint(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("QUINT_BIN", "")
	t.Setenv(compiledEnv, "1")
}

// connect.Run with no quint: the traces are the Go evaluator's, from
// testdata/bank.mbt.json (gotogether compile -spec testdata/bank.qnt),
// replayed as quint's are, with the state compared, the model's
// balanceOf evaluated after every step, Config.Require's vals counted and
// steered to, and a bound driver checked against the model and run.
func TestCompiledRunsWithoutQuint(t *testing.T) {
	noQuint(t)
	Run(t, Config{Spec: "testdata/bank.qnt", MaxSamples: 10, MaxSteps: 15, Seed: "0x1", Require: []string{"nonNegative", "bobSaves"}},
		func(testing.TB) Driver { return evalStateDriver{evalChecker{&bankDriver{}}} })
	b := bankBinding(false, (*bank).depositBalance).Returns("deposit", "balanceOf", "account")
	b.Run(t, Config{Spec: "testdata/bank.qnt", MaxSamples: 5, MaxSteps: 12, Seed: "0x2"})
	// an instance module: its vals and defs, which quint's flattening
	// drops unless kept, are in the compiled model
	Run(t, Config{Spec: "testdata/instance.qnt", Main: "design", MaxSamples: 3, MaxSteps: 6, Seed: "7", Require: []string{"atMax", "half"}},
		func(testing.TB) Driver {
			return DriverFunc(func(s *Step) error {
				if s.Index > 0 && MustEval[int](s, "twice", 2) != 4 {
					return errors.New("twice(2) is not 4")
				}
				return nil
			})
		})
}

// The Go evaluator's traces find what quint's do: the transfer bug (a
// transfer of 3 credits twice) diverges in some trace, and the faithful
// bank in none. Each trace is written as quint run --mbt writes one: the
// first state's action init, every step's a named action with its picks,
// every nondet of step in the record.
func TestCompiledTracesFindTheBug(t *testing.T) {
	noQuint(t)
	files, c, err := GenerateRun(t.Context(), Config{Spec: "testdata/bank.qnt", MaxSamples: 30, MaxSteps: 20, Seed: "0x1", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if c.compiled == nil || len(files) != 30 {
		t.Fatalf("compiled %v, %d files: want the Go evaluator's 30 traces", c.compiled != nil, len(files))
	}
	traces, err := ReadTraces(t.Context(), c, files)
	if err != nil {
		t.Fatal(err)
	}
	caught := 0
	for i, tr := range traces {
		if tr.States[0].Action != "init" {
			t.Fatalf("trace %d starts with %q, not init", i, tr.States[0].Action)
		}
		for _, st := range tr.States[1:] {
			switch st.Action {
			case "deposit", "withdraw", "transfer":
			default:
				t.Fatalf("trace %d state %d: action %q is none of step's", i, st.Index, st.Action)
			}
			if _, ok := st.Picks["amount"]; !ok {
				t.Fatalf("trace %d state %d: no amount picked: %v", i, st.Index, st.Picks)
			}
		}
		if err := Replay(tr, &bankDriver{}, ReplayOptions{Trace: i}); err != nil {
			t.Fatalf("the faithful bank diverges: %v", err)
		}
		if err := Replay(tr, &bankDriver{buggy: true}, ReplayOptions{Trace: i}); err != nil {
			caught++
		}
	}
	if caught == 0 {
		t.Fatal("no trace of 30 catches the buggy transfer")
	}
	raw, err := itf.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"account", "amount", "src", "dst"} {
		if _, ok := raw.States[0].Picks[n]; ok {
			t.Errorf("init's state picks %s: quint's None", n)
		}
	}
	// the same seed is the same traces; trace i depends on the seed and i
	again, _, err := GenerateRun(t.Context(), Config{Spec: "testdata/bank.qnt", MaxSamples: 2, MaxSteps: 20, Seed: "0x1", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range again {
		a, _ := ReadTraces(t.Context(), c, []string{f})
		if len(a[0].States) != len(traces[i].States) || a[0].States[len(a[0].States)-1].Vars["balances"].String() != traces[i].States[len(traces[i].States)-1].Vars["balances"].String() {
			t.Errorf("trace %d differs at the same seed", i)
		}
	}
}

// What the compiled model cannot do fails with the fix, never quietly:
// a quint test's run, a quint argument the simulation does not take, a
// violated invariant, a spec with no compiled model under "1", and a
// stale one.
func TestCompiledRefusals(t *testing.T) {
	noQuint(t)
	dir := t.TempDir()
	check := func(err error, want string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v, want one saying %q", err, want)
		}
	}
	_, _, err := generateTest(t.Context(), Config{Spec: "testdata/bank.qnt", Dir: dir}, "aliceSavesTest")
	check(err, "is a quint test's, which the Go evaluator does not run")
	_, _, err = GenerateRun(t.Context(), Config{Spec: "testdata/bank.qnt", Dir: dir, Args: []string{"--backend", "rust"}})
	check(err, `Config.Args "--backend" is quint's`)
	files, _, err := GenerateRun(t.Context(), Config{Spec: "testdata/bank.qnt", Dir: dir, MaxSamples: 3, Args: []string{"--invariant", "nonNegative"}})
	if err != nil || len(files) != 3 {
		t.Fatalf("an invariant that holds: %d files, %v", len(files), err)
	}
	_, _, err = GenerateRun(t.Context(), Config{Spec: "testdata/bank.qnt", Dir: dir, MaxSamples: 3, Args: []string{"--invariant=nonNegative,bobSaves"}})
	check(err, "violates invariant bobSaves in its state 0")
	_, _, err = GenerateRun(t.Context(), Config{Spec: "testdata/terminal_samples.qnt", Dir: dir})
	check(err, "gotogether compile -spec testdata/terminal_samples.qnt")
	// a copy whose source changed
	stale := t.TempDir()
	for _, f := range []string{"bank.qnt", "bank.mbt.json"} {
		data, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if f == "bank.qnt" {
			data = append(data, "\n// changed\n"...)
		}
		if err := os.WriteFile(filepath.Join(stale, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err = GenerateRun(t.Context(), Config{Spec: filepath.Join(stale, "bank.qnt"), Dir: dir})
	check(err, "is stale: bank.qnt changed since it was compiled")
	t.Setenv(compiledEnv, "2")
	_, _, err = GenerateRun(t.Context(), Config{Spec: "testdata/bank.qnt", Dir: dir})
	check(err, `GOTOGETHER_COMPILED="2"`)
}

// Unset, the compiled model is used only where quint is not installed;
// "0" never uses it, and RequireTraces skips where neither can run.
func TestCompiledModeSelection(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("QUINT_BIN", "")
	t.Setenv(compiledEnv, "")
	c, err := Config{Spec: "testdata/bank.qnt", Dir: t.TempDir()}.resolve(t.Context())
	if err != nil || c.compiled == nil {
		t.Fatalf("unset, no quint, a compiled model: compiled %v, %v", c.compiled != nil, err)
	}
	t.Setenv(compiledEnv, "0")
	if _, err := (Config{Spec: "testdata/bank.qnt", Dir: t.TempDir()}).resolve(t.Context()); err == nil {
		t.Fatal(`"0" with no quint resolved: it must need quint`)
	}
	t.Setenv(compiledEnv, "")
	t.Run("skips", func(t *testing.T) {
		RequireTraces(t, Config{Spec: "testdata/terminal_samples.qnt"})
		t.Fatal("RequireTraces did not skip a spec with neither quint nor a compiled model")
	})
	RequireTraces(t, Config{Spec: "testdata/bank.qnt"}) // does not skip
}
