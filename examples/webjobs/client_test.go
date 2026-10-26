package harness

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/webjobs/webjobs"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// clientRun runs the server (as this binary was built: a mutant's
// overlay or none) behind httptest and drives it with Client, no browser,
// then stops it, drains it and checks both recorders' steps.
func clientRun(t *testing.T, seed int64) *qtest.Run {
	t.Helper()
	mem := &qobs.MemorySink{}
	opt := []qobs.Option{qobs.WithActor("webjobs"), qobs.WithProcess("webjobs")}
	// jobs of 100 to 300ms, and a cancel within the first 100ms of a
	// journey that cancels: it finds its job running
	app := webjobs.New(webjobs.Config{Recorder: qobs.NewRecorder(mem, append(opt, qobs.WithID("server"))...), Seed: seed,
		MinWork: 100 * time.Millisecond, MaxWork: 300 * time.Millisecond})
	srv := httptest.NewServer(app)
	c := &Client{Base: srv.URL, Recorder: qobs.NewRecorder(mem, append(opt, qobs.WithID("ui"))...), Seed: seed,
		Journeys: 8, CancelWithin: 100 * time.Millisecond, Stuck: time.Second}
	err := c.Run(t.Context())
	srv.Close()
	app.Drain()
	if err != nil {
		t.Fatal(err)
	}
	return qtest.Validate(t, "webjobs.binding.yaml", mem.Steps())
}

// The real server, driven through the page's API without a browser (eight
// journeys, each a double-clicked submission, polls, a cancel in half of
// them): a finished run. Each mutant's TestMutant runs the same against
// its server, so a regression of webjobs/ fails per push.
func TestClient(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is; a mutant's client run is TestMutant's")
	}
	t.Parallel()
	qtest.RequireQuint(t)
	clientRun(t, 1).Expect(t, qtest.Finishes())
}
