package harness

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/webjobs/webjobs"
	"github.com/chucklehead-dev/gotogether/qdriver"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A real Chromium driven by Playwright (playwright/journey.cjs: three
// journeys, a double click, a cancel) against the real server, the
// page's calls written by the adapter in adapters/playwright as a
// request log (package qdriver), read beside the server's steps: a
// finished run. The adapter's events, replayed without a browser, give
// the same log. Full mode; node and Playwright (NODE_PATH, or npm root
// -g) are optional, a failure under GOTOGETHER_STRICT.
func TestPlaywright(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is")
	}
	if testing.Short() {
		t.Skip("a real browser: not under -short")
	}
	qtest.RequireQuint(t)
	node, env := playwright(t)
	dir := t.TempDir()
	sink, err := qobs.FileSink(filepath.Join(dir, ServerFile), qobs.PerStep)
	if err != nil {
		t.Fatal(err)
	}
	rec := qobs.NewRecorder(sink, qobs.WithID("server"), qobs.WithActor("webjobs"), qobs.WithProcess("webjobs"))
	app := webjobs.New(webjobs.Config{Recorder: rec, Seed: 1, MinWork: 300 * time.Millisecond, MaxWork: 600 * time.Millisecond})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: app}
	go srv.Serve(ln) //qenv:ok the real server, no simulation
	cmd := exec.CommandContext(t.Context(), node, "playwright/journey.cjs", "http://"+ln.Addr().String()+"/", dir)
	cmd.Env = env
	out, runErr := cmd.CombinedOutput()
	srv.Close()
	app.Drain()
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("playwright: %v\n%s", runErr, out)
	}
	f, err := os.Open(filepath.Join(dir, ServerFile))
	if err != nil {
		t.Fatal(err)
	}
	steps, err := qtrace.ReadJSONL(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	log, err := qdriver.ReadFile(filepath.Join(dir, "requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	page := log.Steps()
	calls := map[string]int{}
	for _, s := range page {
		if s.Invocation {
			calls[s.Action]++
		}
	}
	if calls["submit"] != 4 || calls["cancel"] != 1 {
		t.Errorf("calls %v, want four submissions (one a double click's second) and a cancel", calls)
	}
	// each job's first submission was written before the server created
	// it: an invocation precedes its effect (the adapter writes a call
	// when Playwright routes it, before the browser sends it on; a check
	// of the order, not of that choice, which a request written 2ms after
	// it was sent also passed here)
	first := map[any]time.Time{}
	for _, s := range page {
		if _, seen := first[s.Args["key"]]; s.Invocation && s.Action == "submit" && !seen {
			first[s.Args["key"]] = s.Time
		}
	}
	for _, s := range steps {
		if s.Action != "created" {
			continue
		}
		if at, ok := first[s.Args["key"]]; !ok || !at.Before(s.Time) {
			t.Errorf("job %v created at %v, its first submission written at %v: an invocation after its effect", s.Args["id"], s.Time, at)
		}
	}
	qtest.Validate(t, "webjobs.binding.yaml", append(steps, page...)).Expect(t, qtest.Finishes())

	// the events replayed through the adapter make the same log
	replayed, err := exec.Command(node, "../../adapters/playwright/gotogether-requests.cjs", "replay", filepath.Join(dir, "events.jsonl"), "playwright/webjobs.cjs").Output()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if live, _ := os.ReadFile(filepath.Join(dir, "requests.jsonl")); !bytes.Equal(live, replayed) {
		t.Errorf("the replay's log differs from the live one:\n%s\nreplayed:\n%s", live, replayed)
	}
	if dst := os.Getenv("WEBJOBS_RECORD_PLAYWRIGHT"); dst != "" { // the adapter's golden events (adapters/playwright/testdata)
		for _, name := range []string{"events.jsonl", "requests.jsonl"} {
			b, _ := os.ReadFile(filepath.Join(dir, name))
			if err := os.WriteFile(filepath.Join(dst, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// playwright finds node and the playwright package, and the environment
// that resolves it, or skips (fails under GOTOGETHER_STRICT).
func playwright(t *testing.T) (string, []string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		qtest.Optional(t, "node not on PATH: the Playwright journey needs node and the playwright package (npm i -g playwright)")
	}
	env := os.Environ()
	if os.Getenv("NODE_PATH") == "" {
		if root, err := exec.Command("npm", "root", "-g").Output(); err == nil {
			env = append(env, "NODE_PATH="+strings.TrimSpace(string(root)))
		}
	}
	check := exec.Command(node, "-e", "require.resolve('playwright')")
	check.Env = env
	if out, err := check.CombinedOutput(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			qtest.Optional(t, fmt.Sprintf("the playwright package is not installed (npm i -g playwright, and npx playwright install chromium): %s", bytes.TrimSpace(out)))
		}
		t.Fatal(err)
	}
	return node, env
}
