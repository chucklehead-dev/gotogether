package grist_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
