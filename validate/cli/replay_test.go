package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// accountRun writes a step log of the account (a deposit of 5 observing
// balance, then one of 3 observing 8) into dir and returns its path.
func accountRun(t *testing.T, dir string, balance int64) string {
	t.Helper()
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "r", Seq: 1, Time: time.Unix(1, 0), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": balance}},
		{Action: "deposit", Recorder: "r", Seq: 2, Time: time.Unix(2, 0), Args: map[string]any{"amount": int64(3)}, Obs: map[string]any{"balance": balance + 3}},
	}
	p := filepath.Join(dir, "run.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := qtrace.WriteJSONL(f, steps); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p
}

// A Check judges files as the CLI does, and its manifest replays
// (Replay) to the same verdict, passing or failing; a changed input is
// refused, naming it; a backend other than the manifest's is refused;
// and a run judged on the default backend is replayed on it, whatever
// QUINT_BACKEND says.
func TestReplay(t *testing.T) {
	skip.Quint(t)
	ctx := context.Background()
	binding, _ := filepath.Abs("../testdata/account.yaml")
	for _, c := range []struct {
		balance int64
		ok      bool
	}{{5, true}, {6, false}} {
		dir := t.TempDir()
		run := accountRun(t, dir, c.balance)
		j, err := Check{Binding: binding, Files: []string{run}, RequireFinish: false, Options: validate.Options{Dir: t.TempDir()}}.Judge(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if j.OK != c.ok || len(j.Inputs) != 1 || j.Judgment == nil || j.Temporary() {
			t.Fatalf("balance %d: OK %v, %+v", c.balance, j.OK, j)
		}
		m, err := j.Manifest()
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "run.json")
		if err := m.Write(p); err != nil {
			t.Fatal(err)
		}
		read, err := validate.ReadManifest(p)
		if err != nil {
			t.Fatal(err)
		}
		r, err := Replay(ctx, read, validate.Options{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if !r.Same || r.Got.Verdict.Verdict != m.Verdict.Verdict || (r.Got.Verdict.Verdict == validate.Pass) != c.ok || r.Got.Judgment.Report.OK() != c.ok {
			t.Errorf("balance %d: replay %s (same %v: %s), manifest %s", c.balance, r.Got.Verdict, r.Same, r.Differs, m.Verdict)
		}
		if _, err := Replay(ctx, read, validate.Options{Backend: "typescript"}); err == nil || !strings.Contains(err.Error(), "verdicts depend on it: drop -backend") {
			t.Errorf("another backend: %v", err)
		}
		if c.ok {
			continue
		}
		// a changed input: another run
		accountRun(t, dir, 5)
		if _, err := Replay(ctx, read, validate.Options{}); err == nil || !strings.Contains(err.Error(), "changed since the run was judged") {
			t.Errorf("a changed input: %v", err)
		}
	}
}

// A run judged on the default backend is replayed on it: QUINT_BACKEND
// is ignored, and said so, not passed to quint.
func TestReplayIgnoresQuintBackend(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	binding, _ := filepath.Abs("../testdata/account.yaml")
	j, err := Check{Binding: binding, Files: []string{accountRun(t, dir, 5)}, Options: validate.Options{Dir: t.TempDir()}}.Judge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m, err := j.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.Tools.Backend != "" {
		t.Skipf("QUINT_BACKEND=%s set: the run is not on the default backend", m.Tools.Backend)
	}
	t.Setenv("QUINT_BACKEND", "no-such-backend") // quint would refuse it
	r, err := Replay(context.Background(), m, validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Same || len(r.Notes) == 0 || !strings.Contains(strings.Join(r.Notes, "\n"), "QUINT_BACKEND=no-such-backend ignored") {
		t.Errorf("replay %s, notes %q", r.Got.Verdict, r.Notes)
	}
}

func TestReplayRefusesToDropCompositionChecks(t *testing.T) {
	m := &validate.Manifest{Receipt: &receipt.Summary{Composition: &receipt.Composition{}}}
	if _, err := Replay(t.Context(), m, validate.Options{}); err == nil || !strings.Contains(err.Error(), "originating registered System/test") {
		t.Fatalf("composition replay: %v", err)
	}
}
