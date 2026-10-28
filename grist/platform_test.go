package grist_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// serving runs MainArgs serve of a on dir's SQLite file until ctx ends,
// with flags args (after -db and -tenant t1) and opts, and returns its
// stderr once it serves, its API's URL and its result.
func serving(t *testing.T, ctx context.Context, a *grist.App, dir string, args []string, opts ...grist.ServeOption) (*syncBuffer, string, <-chan error) {
	t.Helper()
	var errOut syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- grist.MainArgs(ctx, a, append([]string{"serve", "-db", "sqlite:" + filepath.Join(dir, "app.db"), "-tenant", "t1",
			"-actor-header", "X-Actor", "-poll", "10ms"}, args...), &errOut, &errOut, opts...)
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("serve did not start: %s", errOut.String())
		}
		select {
		case err := <-done:
			t.Fatalf("serve ended at start: %v\n%s", err, errOut.String())
		default:
		}
		if _, after, ok := strings.Cut(errOut.String(), "serving on "); ok {
			url, _, _ := strings.Cut(after, "\n")
			return &errOut, strings.TrimSpace(url), done
		}
	}
}

// healthURL is the side port's URL in serve's stderr.
func healthURL(t *testing.T, errOut *syncBuffer) string {
	t.Helper()
	m := regexp.MustCompile(`health on (http://\S+)`).FindStringSubmatch(errOut.String())
	if m == nil {
		t.Fatalf("no side port in serve's output: %s", errOut.String())
	}
	return m[1]
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// errorIssues are the errors Reconstruct finds in steps, the whole
// run's and each trace's.
func errorIssues(steps []qtrace.Step) []string {
	traces, issues := qtrace.Reconstruct(steps)
	for _, tr := range traces {
		issues = append(issues, tr.Issues...)
	}
	var out []string
	for _, is := range issues {
		if is.Severity == "error" {
			out = append(out, is.String())
		}
	}
	return out
}

func readFile(t *testing.T, path string) []qtrace.Step {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := qtrace.ReadJSONL(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return steps
}

// A node records under <app>/<node>/<incarnation>, never its pid: two
// starts of one pod (a restarted container keeps its pod name, and every
// container's pid is 1) write files whose merge reconstructs, each
// start's own steps under its own recorder, -steps naming a directory
// giving each start a file of its own. Without the incarnation the two
// files number one recorder twice (Reconstruct's duplicate error, no
// verdict).
func TestMainIdentity(t *testing.T) {
	t.Setenv("POD_NAME", "names-0")
	t.Setenv("GRIST_NODE", "")
	dir, stepsDir := t.TempDir(), t.TempDir()
	for i, name := range []string{"a", "b"} {
		ctx, stop := context.WithCancel(context.Background())
		errOut, url, done := serving(t, ctx, apiApp(), dir, []string{"-addr", "127.0.0.1:0", "-steps", stepsDir})
		if !strings.Contains(errOut.String(), "node names-0, incarnation ") {
			t.Errorf("start %d's startup line: %s", i, errOut.String())
		}
		c := &client.Client{App: apiApp(), URL: url, ActorHeader: "X-Actor"}
		if _, err := c.Command(ctx, grist.Envelope{ID: "k" + name, Tenant: "t1", Actor: "editor"}, "name", []byte(`{"name":"`+name+`"}`)); err != nil {
			t.Fatal(err)
		}
		// a refusal: a step of the node's own recorder (the commit's is the relay's)
		if _, err := c.Command(ctx, grist.Envelope{ID: "r" + name, Tenant: "t1", Actor: "editor"}, "name", []byte(`{"name":"`+name+`"}`)); !errors.Is(err, grist.Rejection("taken")) {
			t.Fatalf("a taken name: %v", err)
		}
		stop()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(stepsDir, "*.jsonl"))
	if len(files) != 2 {
		t.Fatalf("files %v: want one per start", files)
	}
	var steps []qtrace.Step
	recorders := map[string]bool{}
	for _, f := range files {
		if !regexp.MustCompile(`/names\.names-0\.[0-9a-f]{12}\.jsonl$`).MatchString(f) {
			t.Errorf("file %s: want <app>.<node>.<incarnation>.jsonl", f)
		}
		for _, s := range readFile(t, f) {
			if strings.HasPrefix(s.Recorder, "names/") {
				recorders[s.Recorder] = true
			}
			steps = append(steps, s)
		}
	}
	if len(recorders) != 2 {
		t.Errorf("node recorders %v: want one per start, names/names-0/<incarnation>", recorders)
	}
	for r := range recorders {
		if !regexp.MustCompile(`^names/names-0/[0-9a-f]{12}$`).MatchString(r) {
			t.Errorf("recorder %s: want names/names-0/<incarnation>", r)
		}
	}
	for _, is := range errorIssues(steps) {
		t.Errorf("the two starts' files merged: %s", is)
	}
}

// In a pod (KUBERNETES_SERVICE_HOST set) a server on loopback has no
// endpoints: serve refuses it, naming -addr :8080, the default address
// included; outside one it serves there.
func TestMainLoopbackInPod(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	for _, args := range [][]string{{"-addr", "127.0.0.1:0"}, {"-addr", "localhost:8080"}, {"-addr", "[::1]:0"}, nil} {
		// bounded: a serve that does not refuse serves until its ctx ends
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := grist.MainArgs(ctx, apiApp(), append([]string{"serve", "-db", "sqlite:" + filepath.Join(t.TempDir(), "x.db"), "-health-addr", ""}, args...), io.Discard, io.Discard)
		cancel()
		var usage *grist.UsageError
		if !errors.As(err, &usage) || !strings.Contains(err.Error(), "give -addr :8080") {
			t.Errorf("serve %v in a pod: %v, want a usage error naming -addr :8080", args, err)
		}
	}
}

// The side port: /livez and /readyz answer ok once the node runs;
// /metrics has the processors' lag and the oldest trigger's age, the
// effects in flight and the relay's backlog. A processor whose effect
// hangs past the live bound fails /livez, naming it, while /readyz stays
// ok (never processor lag); at SIGTERM /readyz fails at once while the
// API keeps serving through the drain's delay.
func TestMainHealth(t *testing.T) {
	defer grist.SetLiveBound(200 * time.Millisecond)()
	hang, release := make(chan struct{}), make(chan struct{})
	a, added := amoApp(func(ctx context.Context, m grist.Meta) error {
		close(hang)
		<-release
		return nil
	})
	grist.NewCommand(a, "add", func(r *grist.Reads, c Added) ([]grist.Event, error) {
		return []grist.Event{added.Of(c)}, nil
	}, grist.Authorize(grist.Authenticated))
	grist.NewQuery(a, "count", func(r *grist.Reads, q Name) (Many, error) {
		return Many{N: len(r.Select(grist.Selector{Types: []string{"added"}}))}, nil
	}, grist.Authorize(grist.Authenticated))
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	errOut, url, done := serving(t, ctx, a, t.TempDir(), []string{"-addr", "127.0.0.1:0", "-health-addr", "127.0.0.1:0"},
		grist.WithDrain(grist.Drain{Delay: 400 * time.Millisecond}))
	hu := healthURL(t, errOut)
	for _, p := range []string{"/livez", "/readyz"} {
		if code, body := get(t, hu+p); code != 200 {
			t.Errorf("%s at start: %d %s", p, code, body)
		}
	}
	c := &client.Client{App: a, URL: url, ActorHeader: "X-Actor"}
	if _, err := c.Command(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Actor: "x"}, "add", []byte(`{"by":1}`)); err != nil {
		t.Fatal(err)
	}
	<-hang
	time.Sleep(300 * time.Millisecond) // past the live bound
	if code, body := get(t, hu+"/livez"); code != 503 || !strings.Contains(body, "processor note of tenant t1") {
		t.Errorf("/livez with an effect hung past the bound: %d %s, want 503 naming the processor", code, body)
	}
	if code, body := get(t, hu+"/readyz"); code != 200 {
		t.Errorf("/readyz with a processor stuck: %d %s, want ok (never processor lag)", code, body)
	}
	_, metrics := get(t, hu+"/metrics")
	for _, want := range []string{
		`grist_effects_in_flight 1`,
		`grist_processor_lag{processor="note",tenant="t1"} 0`, // the checkpoint moved before the effect (at most once)
		`grist_relay_backlog `,
		`grist_ready 1`,
		`grist_store_up 1`,
		"# TYPE grist_processor_oldest_trigger_age_seconds gauge",
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("/metrics has no %q:\n%s", want, metrics)
		}
	}
	close(release)
	// lag: a trigger appended while the node is stopped would show it; a
	// second add, handled at once, leaves 0
	stop() // SIGTERM
	time.Sleep(100 * time.Millisecond)
	if code, body := get(t, hu+"/readyz"); code != 503 || !strings.Contains(body, "draining") {
		t.Errorf("/readyz after SIGTERM: %d %s, want 503 draining", code, body)
	}
	if code, _ := get(t, hu+"/livez"); code != 200 {
		t.Errorf("/livez through the drain: %d, want ok", code)
	}
	if got, _, err := c.Query(context.Background(), grist.Envelope{Tenant: "t1", Actor: "x"}, "count", []byte(`{"name":""}`)); err != nil || !strings.Contains(string(got), `1`) {
		t.Errorf("a query in the drain's delay: %s %v, want it served (the router still sends requests)", got, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The drain's budget: the defaults need 35 s on an orchestrator; a
// grace of 20 s keeps the flush its 10 s and shares the rest, a grace of
// 45 s fits them.
func TestDrainBudget(t *testing.T) {
	b, line := grist.DrainShares(grist.Drain{Grace: 20 * time.Second}, true)
	want := grist.Drain{Delay: 2 * time.Second, Shutdown: 4 * time.Second, Settle: 4 * time.Second, Flush: 10 * time.Second, Grace: 20 * time.Second}
	if b != want {
		t.Errorf("shares of 20 s: %+v, want %+v", b, want)
	}
	if !strings.Contains(line, "needs a grace of 35s") || !strings.Contains(line, "-grace 20s leaves delay 2s, shutdown 4s, settle 4s, flush 10s") {
		t.Errorf("the startup line: %s", line)
	}
	if b, _ := grist.DrainShares(grist.Drain{Grace: 45 * time.Second}, true); b.Shutdown != grist.ShutdownTimeout || b.Delay != grist.DrainDelay {
		t.Errorf("shares of 45 s: %+v, want the defaults", b)
	}
	if b, _ := grist.DrainShares(grist.Drain{}, false); b.Delay != 0 || b.Needs() != 30*time.Second {
		t.Errorf("off an orchestrator: %+v, want no delay", b)
	}
	if b, _ := grist.DrainShares(grist.Drain{Delay: -1}, true); b.Delay != 0 {
		t.Errorf("a negative delay: %+v, want none", b)
	}
}

// killedAt runs serve with a client trickling a request's body across
// the signal (the shutdown waits for it to its bound) and returns what
// its steps file holds grace after the signal, as a SIGKILL there leaves
// it (PerStep: every step written as it is recorded), and whether serve
// had ended by then.
func killedAt(t *testing.T, grace time.Duration, args []string) ([]qtrace.Step, bool) {
	a, added := amoApp(func(ctx context.Context, m grist.Meta) error { return nil })
	grist.NewCommand(a, "add", func(r *grist.Reads, c Added) ([]grist.Event, error) {
		return []grist.Event{added.Of(c)}, nil
	}, grist.Authorize(grist.Authenticated))
	dir := t.TempDir()
	steps := filepath.Join(dir, "steps.jsonl")
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	// the phases scaled down: delay and shutdown alone need more than the grace
	_, url, done := serving(t, ctx, a, dir, append([]string{"-addr", "127.0.0.1:0", "-steps", steps}, args...),
		grist.WithDrain(grist.Drain{Delay: 500 * time.Millisecond, Shutdown: 3 * time.Second, Settle: 2 * time.Second, Flush: 2 * time.Second}))
	c := &client.Client{App: a, URL: url, ActorHeader: "X-Actor"}
	if _, err := c.Command(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Actor: "x"}, "add", []byte(`{"by":1}`)); err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	defer pw.Close()
	req, _ := http.NewRequest("POST", url+"/t/t1/commands/add", pr)
	req.Header.Set("X-Actor", "x")
	req.Header.Set("Idempotency-Key", "k2")
	go func() {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	pw.Write([]byte(`{"by":`))
	time.Sleep(100 * time.Millisecond)
	stop() // SIGTERM
	ended := false
	select {
	case err := <-done:
		ended = true
		if err != nil {
			t.Logf("serve: %v", err)
		}
	case <-time.After(grace):
	}
	recorded := readFile(t, steps) // what the kill leaves
	pw.Close()
	if !ended {
		<-done
	}
	return recorded, ended
}

// -grace budgets the drain into the orchestrator's kill deadline: with a
// request trickling across the signal and phases needing 7.5 s, a
// -grace 2s run ends within it, its settle and every commit recorded
// (the flush done), where the schedule without -grace is still draining
// the request at the kill, no settle in its file.
func TestMainGraceBudgetsTheDrain(t *testing.T) {
	const grace = 2 * time.Second
	settled := func(steps []qtrace.Step) bool {
		for _, s := range steps {
			if s.Settled {
				return true
			}
		}
		return false
	}
	steps, ended := killedAt(t, grace, []string{"-grace", grace.String()})
	if !ended || !settled(steps) {
		t.Errorf("-grace %v: ended %v, settled %v: want the drain within the grace, its settle recorded", grace, ended, settled(steps))
	}
	committed := false
	for _, s := range steps {
		committed = committed || s.Action == "add" && s.Outcome == "ok"
	}
	if !committed {
		t.Errorf("-grace %v: no commit of k1 in the file: the flush was cut", grace)
	}
	if steps, ended := killedAt(t, grace, nil); ended || settled(steps) {
		t.Errorf("without -grace: ended %v, settled %v at the kill: want the unbudgeted drain still in its shutdown (the contrast)", ended, settled(steps))
	}
}

// GOMEMLIMIT unset, serve sets the runtime's memory limit to 90% of the
// cgroup's memory.max; set, or with no limit ("max"), it leaves it.
func TestMemoryLimit(t *testing.T) {
	old := debug.SetMemoryLimit(-1)
	defer debug.SetMemoryLimit(old)
	dir := t.TempDir()
	write := func(v string) string {
		p := filepath.Join(dir, "memory.max")
		if err := os.WriteFile(p, []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Setenv("GOMEMLIMIT", "")
	os.Unsetenv("GOMEMLIMIT")
	if line := grist.SetMemoryLimit(write("1000000000")); debug.SetMemoryLimit(-1) != 900000000 || !strings.Contains(line, "90%") {
		t.Errorf("memory.max 1e9: limit %d (%s), want 9e8", debug.SetMemoryLimit(-1), line)
	}
	debug.SetMemoryLimit(old)
	if grist.SetMemoryLimit(write("max")); debug.SetMemoryLimit(-1) != old {
		t.Errorf("memory.max max: the limit changed to %d", debug.SetMemoryLimit(-1))
	}
	t.Setenv("GOMEMLIMIT", "2GiB")
	if grist.SetMemoryLimit(write("1000000000")); debug.SetMemoryLimit(-1) != old {
		t.Errorf("GOMEMLIMIT set: the limit changed to %d", debug.SetMemoryLimit(-1))
	}
}
