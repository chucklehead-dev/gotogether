package qdriver

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// The Playwright adapter (adapters/playwright) without a browser: a
// recorded sequence of its events (journey: a real Chromium's, recorded
// by examples/webjobs' TestPlaywright; failures: written by hand) fed
// through it gives the golden log, which reads as the protocol says: a
// double click's second request answered first is completed by its own
// answer, a failed request by code 0 and its error, a request never
// answered pending, a correlation id the page set kept.
func TestPlaywrightGolden(t *testing.T) {
	dir := filepath.Join("..", "adapters", "playwright")
	for _, c := range []struct {
		name    string
		check   func(t *testing.T, steps []qtrace.Step, tr *qtrace.Trace)
		pending int
	}{
		{"journey", func(t *testing.T, steps []qtrace.Step, tr *qtrace.Trace) {
			// requests 1 and 2 are the double click's; 2 is answered first
			if s := steps[2]; s.Call != 2 || s.Args["code"] != int64(202) || s.Args["key"] != steps[1].Args["key"] || s.Ref != "POST /jobs corr=pw-2" {
				t.Errorf("the first answer: %+v, want request 2's, 202", s)
			}
			if s := steps[4]; s.Call != 1 || s.Args["code"] != int64(200) || s.Args["id"] != steps[2].Args["id"] {
				t.Errorf("the second answer: %+v, want request 1's, 200, the same job", s)
			}
			if len(tr.Steps) != 11 {
				t.Errorf("%d steps, want 11: five calls completed, six shows", len(tr.Steps))
			}
		}, 0},
		{"failures", func(t *testing.T, steps []qtrace.Step, tr *qtrace.Trace) {
			if s := steps[1]; s.Call != 1 || s.Args["code"] != int64(0) || s.Error != "net::ERR_CONNECTION_REFUSED" || s.Outcome != "ok" || s.Args["id"] != "" {
				t.Errorf("a failed request: %+v, want completed with code 0 and the error", s)
			}
			if s := steps[2]; s.Ref != "POST /jobs corr=app-7" {
				t.Errorf("a request with its own correlation id: ref %q", s.Ref)
			}
			if s := steps[4]; s.Call != 3 || s.Args["code"] != int64(500) || s.Args["key"] != "k2" {
				t.Errorf("a 500: %+v", s)
			}
			if tr.Pending[0].Action != "cancel" {
				t.Errorf("pending %+v, want the cancel", tr.Pending)
			}
		}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			golden := filepath.Join(dir, "testdata", c.name+".requests.jsonl")
			l, err := ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			steps := l.Steps()
			traces, issues := qtrace.Reconstruct(steps)
			if len(issues) > 0 || len(traces) != 1 || len(traces[0].Pending) != c.pending || len(traces[0].Issues) > 0 {
				t.Fatalf("reconstructed: %v %+v", issues, traces)
			}
			for _, s := range steps {
				if s.Recorder != "ui" || s.Actor != "webjobs" || s.Thread != "page" {
					t.Fatalf("%+v: want the header's names", s)
				}
			}
			c.check(t, steps, traces[0])
			// the adapter, fed the events, writes the golden log
			node, err := exec.LookPath("node")
			if err != nil {
				skip.Optional(t, "node not on PATH: the adapter's replay of its events is not checked against the golden log")
				return
			}
			out, err := exec.Command(node, filepath.Join(dir, "gotogether-requests.cjs"), "replay",
				filepath.Join(dir, "testdata", c.name+".events.jsonl"), filepath.Join(dir, "testdata", "config.cjs")).Output()
			if err != nil {
				t.Fatalf("replay: %v", err)
			}
			want, _ := os.ReadFile(golden)
			if !bytes.Equal(out, want) {
				t.Errorf("the adapter's log differs from %s (regenerate it with node gotogether-requests.cjs replay, if the change is meant):\n%s", golden, diffLines(want, out))
			}
		})
	}
}

// diffLines names the first line two logs differ at.
func diffLines(a, b []byte) string {
	la, lb := bytes.Split(a, []byte("\n")), bytes.Split(b, []byte("\n"))
	for i := 0; i < len(la) || i < len(lb); i++ {
		var x, y []byte
		if i < len(la) {
			x = la[i]
		}
		if i < len(lb) {
			y = lb[i]
		}
		if !bytes.Equal(x, y) {
			return fmt.Sprintf("line %d:\n  golden: %s\n  got:    %s", i+1, x, y)
		}
	}
	return ""
}
