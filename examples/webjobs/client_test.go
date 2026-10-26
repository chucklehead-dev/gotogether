package harness

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/webjobs/bombadiltrace"
	"github.com/chucklehead-dev/gotogether/examples/webjobs/journey"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// client is the journey.Client of a run: a cancel within the first 100ms
// of a journey that cancels (it finds its job running: jobs run 100 to
// 300ms), journeys alternating under -short.
func client() journey.Client {
	return journey.Client{Journeys: clientJourneys(), CancelWithin: 100 * time.Millisecond, Stuck: time.Second, Alternate: testing.Short()}
}

// clientRun runs seed's plan against the server in this process (as
// this binary was built: a mutant's overlay or none) and checks both
// recorders' steps.
func clientRun(t *testing.T, seed int64) *qtest.Run {
	t.Helper()
	return played(t, journey.InProcess{Seed: seed, MinWork: 100 * time.Millisecond, MaxWork: 300 * time.Millisecond}, seed)
}

// clientRunOf runs it against the program bin (cmd/webjobs, built with a
// mutant's overlay: a small binary, linked in a second), driven from this
// process.
func clientRunOf(t *testing.T, bin string, seed int64) *qtest.Run {
	t.Helper()
	return played(t, journey.Program{Bin: bin, Dir: t.TempDir(), Seed: seed, MinWork: 100 * time.Millisecond, MaxWork: 300 * time.Millisecond}, seed)
}

func played(t *testing.T, s journey.Server, seed int64) *qtest.Run {
	t.Helper()
	c := client()
	steps, err := journey.Rerun(t.Context(), s, c, c.Plan(seed))
	if err != nil {
		t.Fatal(err)
	}
	return qtest.Validate(t, "webjobs.binding.yaml", steps)
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

// The recorded faithful journey's actions (bombadiltrace.Actions), played
// by the client against a fresh server: a finished run, as Bombadil's
// was. The bridge a Bombadil failure is shrunk through. Full mode.
func TestRecordedActions(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is")
	}
	if testing.Short() {
		t.Skip("a recorded journey's actions played again (about 6s of them): full mode")
	}
	t.Parallel()
	qtest.RequireQuint(t)
	f, err := os.Open(filepath.Join("testdata", "runs", "faithful", TraceFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	as, err := bombadiltrace.Actions(f, bombadiltrace.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d actions: %v", len(as), as)
	steps, err := journey.Rerun(t.Context(), journey.InProcess{Seed: 1, MinWork: 200 * time.Millisecond, MaxWork: 600 * time.Millisecond}, client(), as)
	if err != nil {
		t.Fatal(err)
	}
	qtest.Validate(t, "webjobs.binding.yaml", steps).Expect(t, qtest.Finishes())
}
