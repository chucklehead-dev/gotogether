package grist_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // serve's driver

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// catalog prints the registry as JSON, each type a JSON Schema; verify
// checks it; anything else is a usage error.
func TestMainCatalog(t *testing.T) {
	var out bytes.Buffer
	if err := grist.MainArgs(context.Background(), apiApp(), []string{"catalog"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	var c struct {
		Name     string `json:"name"`
		Commands []struct {
			Name       string         `json:"name"`
			Args       map[string]any `json:"args"`
			Authorized bool           `json:"authorized"`
			Internal   bool           `json:"internal"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &c); err != nil {
		t.Fatalf("catalog: %v\n%s", err, out.Bytes())
	}
	props, _ := c.Commands[0].Args["properties"].(map[string]any)
	if c.Name != "names" || len(c.Commands) != 2 || c.Commands[0].Name != "name" || props["name"] == nil || !c.Commands[0].Authorized || !c.Commands[1].Internal {
		t.Errorf("catalog %s: want names' two commands, name's arguments as a schema, authorized, secret internal", out.Bytes())
	}
	if err := grist.MainArgs(context.Background(), apiApp(), []string{"verify"}, &out, &out); err != nil {
		t.Error(err)
	}
	var usage *grist.UsageError
	for _, args := range [][]string{nil, {"nope"}, {"serve"}, {"serve", "-db", "mysql:x"}} {
		if err := grist.MainArgs(context.Background(), apiApp(), args, &out, &out); !errors.As(err, &usage) {
			t.Errorf("args %q: %v, want a usage error", args, err)
		}
	}
}

// syncBuffer is a buffer written by serve's goroutine and read by the
// test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// serve runs the API and the node on a SQLite file: a client's command
// commits, and at shutdown every committed step is in the steps file,
// relayed from the step log.
func TestMainServe(t *testing.T) {
	dir := t.TempDir()
	steps := filepath.Join(dir, "steps.jsonl")
	ctx, cancel := context.WithCancel(context.Background())
	var errOut syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- grist.MainArgs(ctx, apiApp(), []string{"serve", "-db", "sqlite:" + filepath.Join(dir, "names.db"), "-addr", "127.0.0.1:0",
			"-tenant", "t1", "-steps", steps, "-actor-header", "X-Actor", "-poll", "10ms"}, &errOut, &errOut)
	}()
	var url string
	for deadline := time.Now().Add(10 * time.Second); url == ""; {
		if time.Now().After(deadline) {
			t.Fatalf("serve did not start: %s", errOut.String())
		}
		if _, after, ok := strings.Cut(errOut.String(), "serving on "); ok {
			url = strings.TrimSpace(after)
		}
		time.Sleep(5 * time.Millisecond)
	}
	c := &client.Client{App: apiApp(), URL: url, ActorHeader: "X-Actor"}
	rc, err := c.Command(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Actor: "editor"}, "name", []byte(`{"name":"a"}`))
	if err != nil || rc.Pos != 1 {
		t.Fatalf("a command over the served API: %v %v", rc, err)
	}
	if _, err := c.Command(ctx, grist.Envelope{ID: "k2", Tenant: "t1", Actor: "editor"}, "name", []byte(`{"name":"a"}`)); !errors.Is(err, grist.Rejection("taken")) {
		t.Errorf("a taken name: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(steps)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recorded, err := qtrace.ReadJSONL(f)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]string{}
	for _, s := range recorded {
		if s.Action == "name" {
			outcomes[s.Args["id"].(string)] = s.Outcome
		}
	}
	if outcomes["k1"] != "ok" || outcomes["k2"] != "taken" {
		t.Errorf("steps in the file %v: want k1's commit and k2's refusal, relayed", outcomes)
	}
}

// startServe runs MainArgs serve of a on a SQLite file in dir until ctx
// ends, returning its URL, the steps file and its result.
func startServe(t *testing.T, ctx context.Context, a *grist.App, dir string) (string, string, <-chan error) {
	t.Helper()
	steps := filepath.Join(dir, "steps.jsonl")
	var errOut syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- grist.MainArgs(ctx, a, []string{"serve", "-db", "sqlite:" + filepath.Join(dir, "app.db"), "-addr", "127.0.0.1:0",
			"-tenant", "t1", "-steps", steps, "-actor-header", "X-Actor", "-poll", "10ms"}, &errOut, &errOut)
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("serve did not start: %s", errOut.String())
		}
		if _, after, ok := strings.Cut(errOut.String(), "serving on "); ok {
			return strings.TrimSpace(after), steps, done
		}
	}
}

// A graceful shutdown keeps the node running while the server drains:
// an at-most-once effect in flight at SIGTERM, while a client trickles a
// request's body across it, runs to its end with its context live and
// its outcome is appended and recorded; the trickled request is
// answered. The node was stopped at the signal: the effect's context
// ended in flight, its outcome never appended (a payment made, recorded
// nowhere).
func TestMainShutdownDrains(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var cancelled error
	a, added := amoApp(func(ctx context.Context, m grist.Meta) error {
		if m.Pos != 1 {
			return nil
		}
		close(started)
		<-release
		time.Sleep(50 * time.Millisecond) // the node had all the time it needed
		cancelled = ctx.Err()
		return nil
	})
	grist.NewCommand(a, "add", func(r *grist.Reads, c Added) ([]grist.Event, error) {
		return []grist.Event{added.Of(c)}, nil
	}, grist.Authorize(grist.Authenticated))
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	url, steps, done := startServe(t, ctx, a, t.TempDir())
	c := &client.Client{App: a, URL: url, ActorHeader: "X-Actor"}
	if _, err := c.Command(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Actor: "x"}, "add", []byte(`{"by":1}`)); err != nil {
		t.Fatal(err)
	}
	<-started
	// a client trickling its body: in flight when the signal comes
	pr, pw := io.Pipe()
	req, _ := http.NewRequest("POST", url+"/t/t1/commands/add", pr)
	req.Header.Set("X-Actor", "x")
	req.Header.Set("Idempotency-Key", "k2")
	answered := make(chan int, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			answered <- 0
			return
		}
		resp.Body.Close()
		answered <- resp.StatusCode
	}()
	pw.Write([]byte(`{"by":`))
	time.Sleep(50 * time.Millisecond)
	stop() // SIGTERM
	time.Sleep(50 * time.Millisecond)
	close(release)
	time.Sleep(200 * time.Millisecond)
	pw.Write([]byte(`2}`))
	pw.Close()
	if code := <-answered; code != 200 {
		t.Errorf("the request trickled across the signal: %d, want 200", code)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if cancelled != nil {
		t.Errorf("the effect in flight at the signal saw its context end (%v): the node stopped before the server drained", cancelled)
	}
	f, err := os.Open(steps)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recorded, err := qtrace.ReadJSONL(f)
	if err != nil {
		t.Fatal(err)
	}
	outcome := false
	for _, s := range recorded {
		outcome = outcome || s.Action == "noteOutcome" && s.Args["trigger"] == int64(1) && s.Outcome == "ok"
	}
	if !outcome {
		t.Errorf("no noteOutcome of trigger 1 in the file: the effect's outcome was lost at shutdown")
	}
}

// serve records a settle on shutdown, after the node's processors are
// done with what the requests committed and the relay has recorded it:
// an at-most-once effect still in flight at the signal (its checkpoint
// committed, its outcome not yet) is waited for, so the settle is the
// file's last step and the bindings' eventual vals are judged on it.
func TestMainServeSettles(t *testing.T) {
	a, added := amoApp(func(ctx context.Context, m grist.Meta) error {
		time.Sleep(300 * time.Millisecond) // in flight when the signal comes
		return nil
	})
	grist.NewCommand(a, "add", func(r *grist.Reads, c Added) ([]grist.Event, error) {
		return []grist.Event{added.Of(c)}, nil
	}, grist.Authorize(grist.Authenticated))
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	url, steps, done := startServe(t, ctx, a, t.TempDir())
	c := &client.Client{App: a, URL: url, ActorHeader: "X-Actor"}
	if _, err := c.Command(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Actor: "x"}, "add", []byte(`{"by":1}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // the effect under way
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(steps)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recorded, err := qtrace.ReadJSONL(f)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, s := range recorded {
		if s.Settled {
			actions = append(actions, "settled")
		} else {
			actions = append(actions, s.Action)
		}
	}
	if n := len(actions); n == 0 || actions[n-1] != "settled" || !slices.Contains(actions, "noteOutcome") {
		t.Errorf("steps %v: want the effect's outcome (noteOutcome) and then a settle, last", actions)
	}
}
