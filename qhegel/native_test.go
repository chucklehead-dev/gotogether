package qhegel_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"hegel.dev/go/hegel"
)

// Re-execute actual failing public tests instead of manufacturing a testing.T.
func TestNativeTestChild(t *testing.T) {
	scenario := os.Getenv("QHEGEL_NATIVE_CHILD")
	if scenario == "" {
		t.Skip("subprocess control")
	}
	modes := map[string]qhegel.Mode{"Each": qhegel.Each, "After": qhegel.After, "Batched": qhegel.Batched, "Off": qhegel.Off}
	cfg := qhegel.Config{Mode: modes[os.Getenv("QHEGEL_NATIVE_MODE")], AllowEmpty: true, Rounds: 3, Seed: 1, Opts: []qtest.Option{qtest.Global()}}
	if cfg.Mode != qhegel.Off {
		cfg.Binding = "testdata/bank.yaml"
	}
	var calls, cleaned atomic.Int64
	var afterError atomic.Bool
	defer assertRecorderRestored(t)
	opts := []hegel.Option{hegel.WithDatabase(""), hegel.WithTestCases(60), hegel.WithSeed(1), hegel.WithReproductionBlob(false)}
	if scenario == "budget" {
		opts = append(opts, hegel.WithTestCases(17))
	}
	if scenario == "corpus" {
		opts = []hegel.Option{hegel.WithTestCases(3), hegel.WithReproductionBlob(false)} // exercise the default database without a caller override
		if database := os.Getenv("QHEGEL_NATIVE_DATABASE"); database != "" {
			opts = append(opts, hegel.WithDatabase(database))
		}
		if os.Getenv("QHEGEL_NATIVE_REUSE") == "1" {
			opts = append(opts, hegel.WithPhases(hegel.PhaseReuse))
		}
	}
	invoke := func(t *testing.T) {
		qhegel.Test(t, cfg, func(tc hegel.TestCase, c *qhegel.Case) {
			_ = hegel.Draw(tc, hegel.Integers(0, 1000000))
			call := calls.Add(1)
			defer cleaned.Add(1)
			switch scenario {
			case "health":
				tc.Assume(false)
			case "partial":
				if call > 1 {
					tc.Assume(false)
				}
				c.Step("deposit", nil, "account", "alice", "amount", 1, "obs.alice", 1, "obs.bob", 0)
			case "assertion", "corpus":
				tc.Errorf("native assertion sentinel")
				afterError.Store(true)
			}
		}, opts...)
	}
	if scenario == "corpus" {
		t.Run(os.Getenv("QHEGEL_NATIVE_PROPERTY"), invoke)
		return
	}
	invoke(t)
	if cleaned.Load() != calls.Load() || afterError.Load() {
		t.Errorf("native cleanup/abort lost: calls=%d cleanup=%d continued=%t", calls.Load(), cleaned.Load(), afterError.Load())
	} else {
		t.Log("native invocation cleanup and abort verified")
	}
	if scenario == "budget" {
		want := int64(17)
		if testing.Short() {
			want = 10
		}
		if got := calls.Load(); got != want {
			t.Errorf("case budget: got%d want%d", got, want)
		}
	}
}

func nativeChild(t *testing.T, scenario, mode string, short bool, dir string, extra ...string) ([]byte, error) {
	t.Helper()
	args := []string{"-test.run=^TestNativeTestChild$", "-test.v", "-test.count=1"}
	if short {
		args = append(args, "-test.short")
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = os.Environ()
	if scenario == "corpus" {
		cmd.Env = nil
		for _, value := range os.Environ() {
			// libhegel's CI detection recognizes GitHub's marker even when
			// generic CI is absent. Select the non-CI default before explicit
			// per-control environment overrides below.
			if !strings.HasPrefix(value, "CI=") && !strings.HasPrefix(value, "GITHUB_ACTIONS=") {
				cmd.Env = append(cmd.Env, value)
			}
		}
	}
	cmd.Env = append(cmd.Env, "QHEGEL_NATIVE_CHILD="+scenario, "QHEGEL_NATIVE_MODE="+mode)
	cmd.Env = append(cmd.Env, extra...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	for _, diagnostic := range []string{"case recorder leaked", "native cleanup/abort lost"} {
		if strings.Contains(string(out), diagnostic) {
			t.Errorf("native lifecycle control failed:\n%s", out)
		}
	}
	return out, err
}

func TestNativeFailuresRemainFailedInEveryMode(t *testing.T) {
	qtest.RequireQuint(t)
	for _, mode := range []string{"Each", "After", "Batched", "Off"} {
		t.Run(mode, func(t *testing.T) {
			out, err := nativeChild(t, "health", mode, false, "")
			if err == nil {
				t.Fatalf("health-check failure lost: %v\n%s", err, out)
			}
			if strings.Contains(string(out), "all satisfy the model") || strings.Contains(string(out), "distinct runs against the model") {
				t.Fatalf("failed generation became model evidence:\n%s", out)
			}
		})
	}
}

func TestNativeAssertionAndPartialAfterEvidence(t *testing.T) {
	qtest.RequireQuint(t)
	for _, scenario := range []string{"assertion", "partial"} {
		t.Run(scenario, func(t *testing.T) {
			out, err := nativeChild(t, scenario, "After", false, "")
			if err == nil {
				t.Fatalf("native failure lost: %v\n%s", err, out)
			}
			if scenario == "assertion" && !strings.Contains(string(out), "native assertion sentinel") {
				t.Fatalf("native assertion diagnostic missing:\n%s", out)
			}
			want := "native invocation cleanup and abort verified"
			if scenario == "partial" {
				want = "checking 1 completed distinct runs (generation may be partial)"
			}
			if scenario == "partial" {
				for _, diagnostic := range []string{"checked 1 distinct runs against the model, 0 violations"} {
					if !strings.Contains(string(out), diagnostic) {
						t.Fatalf("partial generation lost %q:\n%s", diagnostic, out)
					}
				}
			}
			if !strings.Contains(string(out), want) {
				t.Fatalf("missing %q:\n%s", want, out)
			}
		})
	}
}

func TestNativeShortBudgetOverridesCaller(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(fmt.Sprint(short), func(t *testing.T) {
			out, err := nativeChild(t, "budget", "Off", short, "")
			if err != nil {
				t.Fatalf("case budget: %v\n%s", err, out)
			}
		})
	}
}

func assertRecorderRestored(t *testing.T) {
	t.Helper()
	restore, err := qobs.InstallGlobal(qobs.NewRecorder(&qobs.MemorySink{}))
	if err != nil {
		t.Errorf("case recorder leaked: %v", err)
		return
	}
	restore()
}

func TestNativeControlFlowRestoresRecorder(t *testing.T) {
	cfg := qhegel.Config{Mode: qhegel.Off, AllowEmpty: true, Opts: []qtest.Option{qtest.Global()}}
	opts := []hegel.Option{hegel.WithDatabase(""), hegel.WithTestCases(3), hegel.WithReproductionBlob(false)}
	t.Run("pass", func(t *testing.T) {
		qhegel.Test(t, cfg, func(tc hegel.TestCase, _ *qhegel.Case) { _ = hegel.Draw(tc, hegel.Booleans()) }, opts...)
		assertRecorderRestored(t)
	})
	t.Run("panic", func(t *testing.T) {
		sentinel := new(int)
		defer func() {
			if got := recover(); got != sentinel {
				t.Errorf("panic changed: %v", got)
			}
			assertRecorderRestored(t)
		}()
		qhegel.Test(t, cfg, func(hegel.TestCase, *qhegel.Case) { panic(sentinel) }, opts...)
	})
	t.Run("Goexit", func(t *testing.T) {
		done := make(chan struct{})
		var returned atomic.Bool
		go func() { //qenv:ok native Goexit control outside simulation
			defer close(done)
			qhegel.Test(t, cfg, func(hegel.TestCase, *qhegel.Case) { runtime.Goexit() }, opts...)
			returned.Store(true)
		}()
		<-done
		if returned.Load() {
			t.Error("Goexit returned")
		}
		assertRecorderRestored(t)
	})
}

// A failed property with explicit disabled persistence writes no hidden corpus.
func TestNativeExplicitDatabaseDisable(t *testing.T) {
	dir := t.TempDir()
	out, err := nativeChild(t, "assertion", "Off", false, dir)
	if err == nil || !strings.Contains(string(out), "native assertion sentinel") {
		t.Fatalf("failure control: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".hegel")); !os.IsNotExist(err) {
		t.Fatalf("explicit disabled database wrote corpus: %v", err)
	}
}

// A shared database must not replay one test's examples under another name.
// This exercises actual persistence and reuse, not just directory construction.
func TestNativeCorpusPersistsAndIsolatesProperties(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprint(custom), func(t *testing.T) {
			dir := t.TempDir()
			var options []string
			if custom {
				database := filepath.Join(dir, "shared-corpus")
				if err := os.Mkdir(database, 0700); err != nil {
					t.Fatal(err)
				}
				options = append(options, "QHEGEL_NATIVE_DATABASE="+database)
			}
			run := func(property string, reuse, fail bool) {
				t.Helper()
				env := append([]string{}, options...)
				env = append(env, "QHEGEL_NATIVE_PROPERTY="+property)
				if reuse {
					env = append(env, "QHEGEL_NATIVE_REUSE=1")
				}
				out, err := nativeChild(t, "corpus", "Off", false, dir, env...)
				if (err != nil) != fail {
					t.Fatalf("property=%s reuse=%t fail=%t: %v\n%s", property, reuse, fail, err, out)
				}
				if fail && !strings.Contains(string(out), "native assertion sentinel") {
					t.Fatalf("missing reproduced assertion:\n%s", out)
				}
			}
			run("a", false, true)
			run("b", true, false) // no examples have ever been saved for b
			run("b", false, true)
			run("a", true, true)
			run("b", true, true)
		})
	}
}

// The hosted-runner default disables persistence, while an explicit database
// still saves examples. The non-CI control above checks the inverse default.
func TestNativeCICorpusDefaultsAndOverrides(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprint(custom), func(t *testing.T) {
			dir := t.TempDir()
			options := []string{"CI=true", "GITHUB_ACTIONS=true", "QHEGEL_NATIVE_PROPERTY=a"}
			if custom {
				database := filepath.Join(dir, "shared-corpus")
				if err := os.Mkdir(database, 0700); err != nil {
					t.Fatal(err)
				}
				options = append(options, "QHEGEL_NATIVE_DATABASE="+database)
			}
			out, err := nativeChild(t, "corpus", "Off", false, dir, options...)
			if err == nil || !strings.Contains(string(out), "native assertion sentinel") {
				t.Fatalf("failure control: %v\n%s", err, out)
			}
			options = append(options, "QHEGEL_NATIVE_REUSE=1")
			out, err = nativeChild(t, "corpus", "Off", false, dir, options...)
			if (err != nil) != custom {
				t.Fatalf("CI reuse with custom=%t: %v\n%s", custom, err, out)
			}
			if custom && !strings.Contains(string(out), "native assertion sentinel") {
				t.Fatalf("explicit CI database did not reproduce its saved assertion:\n%s", out)
			}
		})
	}
}
