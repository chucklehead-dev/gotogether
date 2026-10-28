package counters

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// podEnv makes the test binary a pod: grist.Main(App, Serve()...) on
// its arguments, as cmd/counters is (TestRealTwoPods' processes).
const podEnv = "COUNTERS_POD"

func TestMain(m *testing.M) {
	if os.Getenv(podEnv) != "" {
		grist.Main(App, Serve()...)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A pod is one process serving the app (the test binary as cmd/counters).
type pod struct {
	cmd *exec.Cmd
	url string
	out chan error // the process's end
}

// startPod starts the pod named name (POD_NAME, as the downward API
// sets it) on the SQLite file db, its steps into steps, and waits until
// it serves.
func startPod(t *testing.T, name, db, steps string) *pod {
	t.Helper()
	cmd := exec.Command(os.Args[0], "serve", "-db", "sqlite:"+db, "-addr", "127.0.0.1:0", "-tenant", "t1",
		"-steps", steps, "-actor-header", "X-Actor", "-poll", "20ms")
	cmd.Env = append(os.Environ(), podEnv+"=1", "POD_NAME="+name, "GRIST_NODE=", "KUBERNETES_SERVICE_HOST=")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &pod{cmd: cmd, out: make(chan error, 1)}
	urls := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if _, after, ok := strings.Cut(sc.Text(), "serving on "); ok {
				urls <- strings.TrimSpace(after)
			}
		}
		io.Copy(io.Discard, stderr)
		p.out <- cmd.Wait()
	}()
	select {
	case p.url = <-urls:
	case err := <-p.out:
		t.Fatalf("pod %s ended before it served: %v", name, err)
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("pod %s did not serve within 30 s", name)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	return p
}

// stop sends the pod SIGTERM and waits for its drain to end.
func (p *pod) stop(t *testing.T) {
	t.Helper()
	p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-p.out:
		if err != nil {
			t.Errorf("pod's drain: %v", err)
		}
	case <-time.After(45 * time.Second): // the grace the generated manifests give
		p.cmd.Process.Kill()
		t.Fatal("a pod's drain outlived its grace")
	}
}

// twoPods runs the app as two pods, two processes of its binary on one
// SQLite file, as a Deployment's two replicas share a database: four
// clients, two per pod, create, increment and show counters of tenant
// t1, each call recorded where the client sees it; then each pod is
// sent SIGTERM in turn, and drains (its settle recorded). It returns the
// pods' step files and the clients' merged, as a collector gathering
// every pod's file would.
func twoPods(t *testing.T) []qtrace.Step {
	dir := t.TempDir()
	db := filepath.Join(dir, "counters.db")
	pods := []*pod{startPod(t, "counters-0", db, filepath.Join(dir, "pod0.jsonl"))}
	pods = append(pods, startPod(t, "counters-1", db, filepath.Join(dir, "pod1.jsonl")))
	clientsPath := filepath.Join(dir, "clients.jsonl")
	sink, err := qobs.FileSink(clientsPath, qobs.PerStep)
	if err != nil {
		t.Fatal(err)
	}
	clients := qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink, qobs.WithID("clients")))
	var wg sync.WaitGroup
	for c := 1; c <= 4; c++ {
		wg.Go(func() {
			ctx := qobs.Thread(clients) // one thread per goroutine
			cl := &client.Client{App: App, URL: pods[c%2].url, ActorHeader: "X-Actor", RetryDelay: 10 * time.Millisecond}
			r := rand.New(rand.NewPCG(7, uint64(c)))
			for op := 1; op <= 6; op++ {
				env := grist.Envelope{ID: fmt.Sprintf("c%d-%d", c, op), Tenant: "t1", Actor: fmt.Sprint("client", c)}
				counter := []string{"a", "b", "c"}[r.IntN(3)]
				var name string
				var args string
				switch r.IntN(3) {
				case 0:
					name, args = "create-counter", fmt.Sprintf(`{"counter":%q,"name":%q}`, counter, counter)
				case 1:
					name, args = "increment-counter", fmt.Sprintf(`{"counter":%q,"by":%d}`, counter, 1+r.IntN(3))
				default:
					name, args = "show-counter", fmt.Sprintf(`{"counter":%q}`, counter)
				}
				if err := cl.Do(ctx, env, name, []byte(args)); err != nil {
					var rej *grist.Rejected
					if !errors.As(err, &rej) && !errors.Is(err, grist.ErrConflict) {
						t.Errorf("%s %s: %v", name, env.ID, err)
					}
				}
			}
		})
	}
	wg.Wait()
	for _, p := range pods {
		p.stop(t)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	var steps []qtrace.Step
	for _, f := range []string{"pod0.jsonl", "pod1.jsonl", "clients.jsonl"} {
		steps = append(steps, readSteps(t, filepath.Join(dir, f))...)
	}
	return steps
}

// readSteps reads the step file at path.
func readSteps(t *testing.T, path string) []qtrace.Step {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	steps, err := qtrace.ReadJSONL(f)
	if err != nil {
		t.Fatal(err)
	}
	return steps
}

// duplicates are Reconstruct's errors of one recorder numbering two
// different records, over the run and each trace.
func duplicates(steps []qtrace.Step) []string {
	traces, issues := qtrace.Reconstruct(steps)
	for _, tr := range traces {
		issues = append(issues, tr.Issues...)
	}
	var out []string
	for _, is := range issues {
		if is.Severity == "error" && is.Kind == "duplicate" { // a copy is info
			out = append(out, is.Msg)
		}
	}
	return out
}

// Two pods, two processes of the app's binary on one database, their
// step files and the clients' merged: each pod records under its own
// recorder (counters/<pod>/<incarnation>), so the merge reconstructs
// with no duplicate, conforms to the store's binding and settles, the
// clients' calls linearize, and every answer a client got has its
// store-side step. The mutant pidIdentity (both pods one node, as
// <app>-<pid> named them in containers) gets Reconstruct's duplicate
// error and no verdict (TestMutant).
func TestRealTwoPods(t *testing.T) {
	qtest.RequireQuint(t)
	start := time.Now()
	steps := twoPods(t)
	t.Logf("two pods ran in %v, %d steps", time.Since(start), len(steps))
	if d := duplicates(steps); len(d) > 0 {
		t.Fatalf("the pods' files merged: %d duplicate errors, the first: %s", len(d), d[0])
	}
	nodes := map[string]bool{}
	for _, s := range steps {
		if strings.HasPrefix(s.Recorder, "counters/counters-") {
			nodes[strings.Join(strings.SplitN(s.Recorder, "/", 4)[:3], "/")] = true
		}
	}
	if len(nodes) != 2 {
		t.Errorf("node recorders %v: want one per pod, counters/<pod>/<incarnation>", nodes)
	}
	traces, _ := qtrace.Reconstruct(steps)
	gristtest.Answered(t, App, traces)
	qtest.Validate(t, commits, steps).Expect(t, qtest.Conforms(), qtest.Settles())
	qtest.Validate(t, calls, steps, qtest.Linearize()).Expect(t, qtest.Conforms())
}
