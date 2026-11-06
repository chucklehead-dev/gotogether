package connect

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func lifecycleTraces(t *testing.T, count int) []string {
	t.Helper()
	dir := t.TempDir()
	files := make([]string, count)
	for i := range files {
		files[i] = filepath.Join(dir, fmt.Sprintf("run_%d.itf.json", i))
		if err := os.WriteFile(files[i], []byte(`{"vars":["mbt::actionTaken"],"states":[{"mbt::actionTaken":"init"}]}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func TestTraceLifetimes(t *testing.T) {
	type resource struct {
		ctx    context.Context
		dir    string
		url    string
		closed bool
	}
	var previous *resource
	created := 0
	replayAll(t, Config{}, lifecycleTraces(t, 3), func(tb testing.TB) Driver {
		if previous != nil {
			if previous.ctx.Err() != context.Canceled || !previous.closed {
				tb.Fatal("previous trace's context and cleanup are still active")
			}
			if _, err := os.Stat(previous.dir); !os.IsNotExist(err) {
				tb.Fatalf("previous trace's temporary directory is retained: %v", err)
			}
			client := &http.Client{Timeout: time.Second}
			if response, err := client.Get(previous.url); err == nil {
				response.Body.Close()
				tb.Fatal("previous trace's HTTP server is still serving")
			}
		}
		r := &resource{ctx: tb.Context(), dir: tb.TempDir()}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
		r.url = server.URL
		tb.Cleanup(func() {
			if r.ctx.Err() != context.Canceled {
				tb.Error("trace cleanup ran before context cancellation")
			}
			server.Close()
			r.closed = true
		})
		previous = r
		created++
		return DriverFunc(func(*Step) error {
			if err := r.ctx.Err(); err != nil {
				return fmt.Errorf("trace context is not active: %w", err)
			}
			return nil
		})
	}, "run")
	if previous != nil && (previous.ctx.Err() != context.Canceled || !previous.closed) {
		t.Fatalf("last trace was not cleaned up: %d factories, %+v", created, previous)
	}
}

func TestTraceSelectionDoesNotReadOrConstructOtherTraces(t *testing.T) {
	output, err := connectProcess(t, "selected", "^TestConnectProcessHelper$/trace_1$")
	if err != nil {
		t.Fatalf("selected trace: %v\n%s", err, output)
	}
	if strings.Count(output, "selected factory:") != 1 || !strings.Contains(output, "selected factory: TestConnectProcessHelper/trace_1") {
		t.Fatalf("factory did not belong only to selected trace:\n%s", output)
	}
}

func TestTraceFailureCleansUpAndStops(t *testing.T) {
	output := connectFailureOutput(t, "first")
	for _, want := range []string{"intentional trace failure", "trace cleanup: context canceled", "QUINT_SEED=42"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in failure:\n%s", want, output)
		}
	}
	if strings.Count(output, "factory started") != 1 || strings.Contains(output, "continued after divergence") {
		t.Fatalf("replay continued after its first failure:\n%s", output)
	}
}

// Expected test failures run in their own real testing.T lifecycle. Intercepting
// Fatalf on a fake testing.TB cannot exercise child context/cleanup ordering.
func connectProcess(t *testing.T, mode, run string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run="+run, "-test.v")
	cmd.Env = append(os.Environ(), "GOTOGETHER_CONNECT_HELPER="+mode)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func connectFailureOutput(t *testing.T, mode string) string {
	t.Helper()
	output, err := connectProcess(t, mode, "^TestConnectProcessHelper$")
	if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 {
		t.Fatalf("expected test failure, got %v:\n%s", err, output)
	}
	return output
}

func TestConnectProcessHelper(t *testing.T) {
	switch os.Getenv("GOTOGETHER_CONNECT_HELPER") {
	case "":
		return
	case "bug":
		Run(t, Config{Spec: "testdata/bank.qnt", MaxSamples: 50, MaxSteps: 20, Seed: "0x1"}, func(testing.TB) Driver { return &bankDriver{buggy: true} })
	case "unimplemented":
		Run(t, Config{Spec: "testdata/bank.qnt", MaxSamples: 5, MaxSteps: 10, Seed: "0x1"}, func(testing.TB) Driver {
			d := &bankDriver{}
			return DriverFunc(func(s *Step) error {
				if s.Action == "transfer" {
					return Switch{}.Step(s)
				}
				return d.Step(s)
			})
		})
	case "nondet":
		Test(t, Config{Spec: "testdata/bank.qnt", MaxSamples: 1}, "aliceSavesTest", func(testing.TB) Driver { return &bankDriver{} })
	case "selected":
		files := lifecycleTraces(t, 3)
		for _, i := range []int{0, 2} {
			if err := os.WriteFile(files[i], []byte("invalid unselected trace"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		replayAll(t, Config{Verbose: 1}, files, func(tb testing.TB) Driver {
			tb.Logf("selected factory: %s", tb.Name())
			return DriverFunc(func(s *Step) error {
				if s.Trace != 1 {
					return fmt.Errorf("selected trace lost its original index: %d", s.Trace)
				}
				return nil
			})
		}, "run")
	case "first":
		replayAll(t, Config{Seed: "42"}, lifecycleTraces(t, 3), func(tb testing.TB) Driver {
			tb.Log("factory started")
			tb.Cleanup(func() { tb.Logf("trace cleanup: %v", tb.Context().Err()) })
			return DriverFunc(func(*Step) error { return errors.New("intentional trace failure") })
		}, "run")
		t.Fatal("continued after divergence")
	default:
		t.Fatal("unknown helper mode")
	}
}
