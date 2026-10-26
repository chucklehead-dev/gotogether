package harness

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/webjobs/webjobs"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// client is the Client of a run against base: jobs of 100 to 300ms, a
// cancel within the first 100ms of a journey that cancels (it finds its
// job running), journeys alternating under -short.
func client(base string, rec *qobs.Recorder, seed int64) *Client {
	return &Client{Base: base, Recorder: rec, Seed: seed,
		Journeys: clientJourneys(), CancelWithin: 100 * time.Millisecond, Stuck: time.Second, Alternate: testing.Short()}
}

var workOpts = []qobs.Option{qobs.WithActor("webjobs"), qobs.WithProcess("webjobs")}

// clientRun runs the server (as this binary was built: a mutant's
// overlay or none) behind httptest and drives it with Client, no browser,
// then stops it, drains it and checks both recorders' steps.
func clientRun(t *testing.T, seed int64) *qtest.Run {
	t.Helper()
	mem := &qobs.MemorySink{}
	app := webjobs.New(webjobs.Config{Recorder: qobs.NewRecorder(mem, append(workOpts, qobs.WithID("server"))...), Seed: seed,
		MinWork: 100 * time.Millisecond, MaxWork: 300 * time.Millisecond})
	srv := httptest.NewServer(app)
	err := client(srv.URL, qobs.NewRecorder(mem, append(workOpts, qobs.WithID("ui"))...), seed).Run(t.Context())
	srv.Close()
	app.Drain()
	if err != nil {
		t.Fatal(err)
	}
	return qtest.Validate(t, "webjobs.binding.yaml", mem.Steps())
}

// clientRunOf runs the server as the program bin (cmd/webjobs, built with
// a mutant's overlay: a small binary, linked in a second), drives it with
// Client from this process, stops it with SIGINT (it drains and settles)
// and checks its step log with the client's steps.
func clientRunOf(t *testing.T, bin string, seed int64) *qtest.Run {
	t.Helper()
	log := filepath.Join(t.TempDir(), ServerFile)
	cmd := exec.Command(bin, "-addr", "127.0.0.1:0", "-steps", log, "-seed", fmt.Sprint(seed), "-min-work", "100ms", "-max-work", "300ms")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	base, ok := strings.CutPrefix(strings.TrimSpace(line), "webjobs: ")
	if err != nil || !ok {
		cmd.Process.Kill()
		t.Fatalf("%s printed %q (%v), not its address: %s", bin, line, err, stderr.String())
	}
	mem := &qobs.MemorySink{}
	runErr := client(strings.TrimSuffix(base, "/"), qobs.NewRecorder(mem, append(workOpts, qobs.WithID("ui"))...), seed).Run(t.Context())
	cmd.Process.Signal(os.Interrupt)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("%s: %v: %s", bin, err, stderr.String())
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	f, err := os.Open(log)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	server, err := qtrace.ReadJSONL(f)
	if err != nil {
		t.Fatal(err)
	}
	return qtest.Validate(t, "webjobs.binding.yaml", append(server, mem.Steps()...))
}

// The real server, driven through the page's API without a browser
// (eight journeys at random, or twelve alternating under -short; each a
// double-clicked submission, polls, maybe a cancel): a finished run.
// TestMutants drives each mutant's server the same way, so a regression
// of webjobs/ fails per push.
func TestClient(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is; a mutant's client run is TestMutant's")
	}
	t.Parallel()
	qtest.RequireQuint(t)
	clientRun(t, 1).Expect(t, qtest.Finishes())
}

// clientJourneys is a client run's journeys: 8 at random, or 12 under
// -short, which alternate between running to ready and cancelling: four
// such journeys caught each mutant at 10 of 10 seeds, so twelve are three
// tries (README: the miss chance).
func clientJourneys() int {
	if testing.Short() {
		return 12
	}
	return 8
}
