package raftkv

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// realCluster is the real tier on localhost: three raftkv-node
// processes, each with its log and its step logs in dir.
type realCluster struct {
	t     *testing.T
	bin   string
	dir   string
	addrs []string
	procs []*exec.Cmd
	incs  []int // each node's incarnation
	net   HTTPNet
}

// start starts node i's next incarnation, on its log and its address.
func (c *realCluster) start(i int) {
	c.t.Helper()
	c.incs[i]++
	name := Nodes[i]
	args := []string{"-name", name, "-incarnation", fmt.Sprint(c.incs[i]), "-listen", c.addrs[i],
		"-wal", filepath.Join(c.dir, "wal", name), "-steps", filepath.Join(c.dir, "steps", fmt.Sprintf("%s.%d.jsonl", name, c.incs[i]))}
	for j, n := range Nodes {
		if j != i {
			args = append(args, "-peer", n+"="+c.addrs[j])
		}
	}
	out, err := os.Create(filepath.Join(c.dir, fmt.Sprintf("%s.%d.log", name, c.incs[i])))
	if err != nil {
		c.t.Fatal(err)
	}
	cmd := exec.Command(c.bin, args...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		c.t.Fatal(err)
	}
	c.procs[i] = cmd
}

// kill is kill -9: no deferred code, no flush; what the log committed
// survives.
func (c *realCluster) kill(i int) {
	if p := c.procs[i]; p != nil {
		_ = p.Process.Kill()
		_ = p.Wait()
		c.procs[i] = nil
	}
}

// agreed waits until every node is up, they name one leader and one
// term, and every node has applied the same index; it returns that
// status.
func (c *realCluster) agreed(ctx context.Context, d time.Duration) Status {
	c.t.Helper()
	deadline := time.Now().Add(d)
	var last []Status
	for time.Now().Before(deadline) {
		last = last[:0]
		for i := range Nodes {
			st, err := c.net.Status(ctx, i)
			if err != nil {
				break
			}
			last = append(last, st)
		}
		if len(last) == len(Nodes) && last[0].Up && last[0].Leader != "" {
			same := true
			for _, st := range last[1:] {
				same = same && st.Up && st.Leader == last[0].Leader && st.Term == last[0].Term && st.Applied == last[0].Applied
			}
			if same {
				return last[0]
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.t.Fatalf("the nodes did not agree within %s: %+v (node logs in %s)", d, last, c.dir)
	return Status{}
}

// freeAddrs are n localhost addresses free a moment ago.
func freeAddrs(t *testing.T, n int) []string {
	var out []string
	for range n {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ln.Addr().String())
		ln.Close()
	}
	return out
}

// readSteps reads every step log matching glob.
func readSteps(t *testing.T, glob string) []qtrace.Step {
	t.Helper()
	files, err := filepath.Glob(glob)
	if err != nil || len(files) == 0 {
		t.Fatalf("no step logs %s: %v", glob, err)
	}
	var steps []qtrace.Step
	for _, f := range files {
		r, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		s, err := qtrace.ReadJSONL(bufio.NewReader(r))
		r.Close()
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		steps = append(steps, s...)
	}
	return steps
}

// The real tier: the node's code as three processes over TCP, each with
// a bbolt log. Clients write through it, the leader is killed (kill -9)
// and restarted on its log, and the cluster must catch it up: every node
// agrees on the leader, the term and the applied index. Then the
// clients' history is checked linearized, with what each node holds
// observed after it (raftkv.binding.yaml), and the nodes' logs by the
// cluster's model (cluster.binding.yaml), as a simulated run's are.
// RAFTKV_REAL=1 runs it; it builds cmd/raftkv-node.
func TestRealCluster(t *testing.T) {
	if os.Getenv("RAFTKV_REAL") != "1" {
		t.Skip("RAFTKV_REAL=1 runs the real tier: three raftkv-node processes on localhost")
	}
	qtest.RequireQuint(t)
	dir := t.TempDir()
	if keep := os.Getenv("RAFTKV_REAL_DIR"); keep != "" {
		dir = keep
	}
	for _, d := range []string{"steps", "wal"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "raftkv-node")
	build := []string{"build", "-o", bin}
	if raceBuild {
		build = append(build, "-race") // the nodes' goroutines are where the races would be
	}
	if out, err := exec.Command("go", append(build, "./cmd/raftkv-node")...).CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/raftkv-node: %v\n%s", err, out)
	}
	c := &realCluster{t: t, bin: bin, dir: dir, addrs: freeAddrs(t, len(Nodes)), procs: make([]*exec.Cmd, len(Nodes)), incs: make([]int, len(Nodes))}
	c.net = HTTPNet{Addrs: c.addrs}
	t.Cleanup(func() {
		for i := range Nodes {
			c.kill(i)
		}
		// a node built with -race reports a race on its stderr and runs on
		logs, _ := filepath.Glob(filepath.Join(dir, "n*.log"))
		for _, l := range logs {
			if b, _ := os.ReadFile(l); bytes.Contains(b, []byte("WARNING: DATA RACE")) {
				t.Errorf("%s: a data race in the node:\n%s", filepath.Base(l), b)
			}
		}
	})
	for i := range Nodes {
		c.start(i)
	}
	ctx := context.Background()
	st := c.agreed(ctx, 10*time.Second)
	t.Logf("elected: %+v", st)

	sink, err := qobs.FileSink(filepath.Join(dir, "steps", "clients.jsonl"), qobs.PerStep)
	if err != nil {
		t.Fatal(err)
	}
	cctx := qobs.WithRecorder(ctx, qobs.NewRecorder(sink, qobs.WithID("clients")))
	client := Client{Net: c.net, Nodes: len(Nodes)}
	const clients, ops = 3, 6
	phase := func(p int) { // each client's ops p*ops .. (p+1)*ops-1
		var wg sync.WaitGroup
		for id := 1; id <= clients; id++ {
			wg.Go(func() {
				for k := p * ops; k < (p+1)*ops; k++ {
					client.Do(cctx, id, k+1, Workload(id, k), -1)
				}
			})
		}
		wg.Wait()
	}
	phase(0)
	victim := -1
	for i, n := range Nodes {
		if n == st.Leader {
			victim = i
		}
	}
	c.kill(victim)
	t.Logf("killed the leader %s", Nodes[victim])
	phase(1) // the others elect, and take these writes
	c.start(victim)
	phase(2)
	st = c.agreed(ctx, 10*time.Second)
	t.Logf("after the restart: %+v", st)

	// the probe, as the simulation's: what each node holds, then the settle
	for i := range Nodes {
		s, err := c.net.State(ctx, i)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range Keys {
			qobs.Invoke(cctx, "observe", "node", s.Node, "key", k, "value", s.KV[k]).Done(nil)
		}
	}
	qobs.Settled(cctx)
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	qtest.Validate(t, "raftkv.binding.yaml", readSteps(t, filepath.Join(dir, "steps", "clients.jsonl")),
		qtest.Linearize(), qtest.ResolvePending()).Expect(t, qtest.Conforms(), qtest.Settles(), qtest.Completes())
	nodes := readSteps(t, filepath.Join(dir, "steps", "n*.jsonl"))
	boots := 0
	for _, s := range nodes {
		if s.Action == "boot" {
			boots++
		}
	}
	if boots != len(Nodes)+1 {
		t.Fatalf("the nodes' logs hold %d boots, want %d (three starts and the restart)", boots, len(Nodes)+1)
	}
	qtest.Validate(t, "cluster.binding.yaml", nodes).Expect(t, qtest.Conforms())
}
