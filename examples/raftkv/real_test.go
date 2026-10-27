package raftkv

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	pipes []*os.File // each node's stdin's write end: -exit-with-parent
	incs  []int      // each node's incarnation
	net   HTTPNet
}

// start starts node i's next incarnation, on its log and its address.
func (c *realCluster) start(i int) {
	c.t.Helper()
	c.incs[i]++
	name := Nodes[i]
	args := []string{"-exit-with-parent", "-name", name, "-incarnation", fmt.Sprint(c.incs[i]), "-listen", c.addrs[i],
		"-wal", filepath.Join(c.dir, "wal", name), "-steps", filepath.Join(c.dir, "steps", fmt.Sprintf("node-%s.%d.jsonl", name, c.incs[i]))}
	for j, n := range Nodes {
		if j != i {
			args = append(args, "-peer", n+"="+c.addrs[j])
		}
	}
	out, err := os.Create(filepath.Join(c.dir, fmt.Sprintf("%s.%d.log", name, c.incs[i])))
	if err != nil {
		c.t.Fatal(err)
	}
	cmd, w := startNode(c.t, c.bin, args, out)
	out.Close()
	c.procs[i], c.pipes[i] = cmd, w
}

// startNode starts a raftkv-node with -exit-with-parent's pipe on its
// stdin, in a process group of its own (nodeSysProcAttr), and returns the
// pipe's write end: closed, or this process gone (kill -9, a test
// binary's -timeout, whose panic runs no Cleanup), the node exits.
func startNode(t *testing.T, bin string, args []string, out *os.File) (*exec.Cmd, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = r, out, out
	cmd.SysProcAttr = nodeSysProcAttr()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r.Close() // the node's copy is the only one: EOF comes when w closes
	return cmd, w
}

// kill is kill -9: no deferred code, no flush; what the log committed
// survives.
func (c *realCluster) kill(i int) {
	if p := c.procs[i]; p != nil {
		killGroup(p)
		_ = p.Wait()
		c.pipes[i].Close()
		c.procs[i], c.pipes[i] = nil, nil
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
		if st, ok := agreement(last); ok {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.t.Fatalf("the nodes did not agree within %s: %+v%s (node logs in %s)", d, last, impostors(last), c.dir)
	return Status{}
}

// agreement is the status every node agrees on, if they do: each of
// Nodes answering at its own address as itself, up, with one leader, term
// and applied index. A node's address answering as another node is no
// agreement: something else holds its port (impostors).
func agreement(last []Status) (Status, bool) {
	if len(last) != len(Nodes) || last[0].Leader == "" {
		return Status{}, false
	}
	for i, st := range last {
		if st.Node != Nodes[i] || !st.Up || st.Leader != last[0].Leader || st.Term != last[0].Term || st.Applied != last[0].Applied {
			return Status{}, false
		}
	}
	return last[0], true
}

// impostors names the nodes whose address another node answered at, for
// agreed's failure.
func impostors(last []Status) string {
	var out string
	for i, st := range last {
		if st.Node != Nodes[i] {
			out += fmt.Sprintf("; %s's address answered as %q: its port is another process's (a proxy's?), see %s's log", Nodes[i], st.Node, Nodes[i])
		}
	}
	return out
}

// freeAddrs are n localhost addresses free a moment ago, their ports
// below the kernel's ephemeral range (ephemeralLow) and never handed out
// twice by this test binary. A port picked by listening on ":0" and
// released is one the kernel may hand out again, to a toxiproxy proxy
// made with ":0" or an outgoing connection, before the node binds it:
// CI's n3 once found its address in use, held by a proxy to another
// node, which answered for it, so the cluster seemed to agree with n3
// never up.
func freeAddrs(t *testing.T, n int) []string {
	t.Helper()
	lo, hi := nodePorts()
	portsMu.Lock()
	defer portsMu.Unlock()
	var out []string
	start := rand.IntN(hi - lo)
	for k := 0; k < hi-lo && len(out) < n; k++ {
		port := lo + (start+k)%(hi-lo)
		if portsGiven[port] {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue // in use
		}
		out = append(out, ln.Addr().String())
		ln.Close()
		portsGiven[port] = true
	}
	if len(out) < n {
		t.Fatalf("no %d free ports in [%d, %d), below the ephemeral range", n, lo, hi)
	}
	return out
}

// portsGiven are the ports freeAddrs has handed out (portsMu held):
// process-wide, so two runs of the binary's tests never share one.
var (
	portsMu    sync.Mutex
	portsGiven = map[int]bool{}
)

// nodePorts are the ports freeAddrs picks from: from 10000 to the
// ephemeral range's start, at most 32768 (Linux's default start; BSD's
// and macOS's is 49152).
func nodePorts() (lo, hi int) {
	lo, hi = 10000, min(ephemeralLow(), 32768)
	if hi-lo < 1000 {
		lo = 1024 // an ephemeral range starting low: what is below it
	}
	return lo, hi
}

// ephemeralLow is where the kernel's ephemeral ports begin: Linux's
// ip_local_port_range, else IANA's 49152.
func ephemeralLow() int {
	b, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	var low int
	if err == nil {
		if _, err := fmt.Sscan(string(b), &low); err == nil && low > 1024 {
			return low
		}
	}
	return 49152
}

// buildNode builds cmd/raftkv-node into dir, with the race detector when
// the test has it: the nodes' goroutines are where the races would be.
func buildNode(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "raftkv-node")
	build := []string{"build", "-o", bin}
	if raceBuild {
		build = append(build, "-race")
	}
	if out, err := exec.Command("go", append(build, "./cmd/raftkv-node")...).CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/raftkv-node: %v\n%s", err, out)
	}
	return bin
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
	bin := buildNode(t, dir)
	c := &realCluster{t: t, bin: bin, dir: dir, addrs: freeAddrs(t, len(Nodes)), procs: make([]*exec.Cmd, len(Nodes)),
		pipes: make([]*os.File, len(Nodes)), incs: make([]int, len(Nodes))}
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
	// the cluster settled too: the harness says so in a log of its own,
	// after every node's last step in time, every node frozen first
	// (SIGSTOP): a node's step later than the settle would void it
	// (Trace.Settled), and an apply or an election can come late (CI
	// once saw n2.2's apply after the settle). The cluster's eventually
	// hasLeader is judged.
	for i, p := range c.procs {
		if err := stopGroup(p); err != nil {
			t.Fatalf("%s: %v", Nodes[i], err)
		}
	}
	hsink, err := qobs.FileSink(filepath.Join(dir, "steps", "harness.jsonl"), qobs.PerStep)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // past RealTimeSlack after the nodes' last step
	qobs.NewRecorder(hsink, qobs.WithID("harness"), qobs.WithProcess("cluster")).Settled(ctx)
	if err := hsink.Close(); err != nil {
		t.Fatal(err)
	}
	for i := range Nodes {
		c.kill(i)
	}

	qtest.Validate(t, "raftkv.binding.yaml", readSteps(t, filepath.Join(dir, "steps", "clients.jsonl")),
		qtest.Linearize(), qtest.ResolvePending(), qtest.LinearizeBudget(50000)).Expect(t, qtest.Conforms(), qtest.Settles(), qtest.Completes())
	nodes := readSteps(t, filepath.Join(dir, "steps", "node-*.jsonl"))
	boots := 0
	for _, s := range nodes {
		if s.Action == "boot" {
			boots++
		}
	}
	if boots != len(Nodes)+1 {
		t.Fatalf("the nodes' logs hold %d boots, want %d (three starts and the restart)", boots, len(Nodes)+1)
	}
	settle := readSteps(t, filepath.Join(dir, "steps", "harness.jsonl"))
	qtest.Validate(t, "cluster.binding.yaml", append(nodes, settle...)).Expect(t, qtest.Conforms(), qtest.Settles())
	// without the settle the cluster's liveness is not judged, and the
	// claim fails: the settle is what makes eventually hasLeader count
	if r := qtest.Validate(t, "cluster.binding.yaml", nodes); qtest.Settles().Holds(r) {
		t.Error("the nodes' logs without the harness's settle settle: Settles judges nothing")
	}
}

// A node started with -exit-with-parent exits when its parent dies: its
// stdin's write end is held by a stand-in parent alone (sleep), killed
// with SIGKILL, as a harness or a test binary at its -timeout dies.
func TestRealNodeExitsWithParent(t *testing.T) {
	if os.Getenv("RAFTKV_REAL") != "1" {
		t.Skip("RAFTKV_REAL=1 runs the real tier")
	}
	dir := t.TempDir()
	bin := buildNode(t, dir)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command("sleep", "300")
	parent.ExtraFiles = []*os.File{w} // the pipe's only writer
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	addrs := freeAddrs(t, 3)
	out, err := os.Create(filepath.Join(dir, "n1.log"))
	if err != nil {
		t.Fatal(err)
	}
	node := exec.Command(bin, "-exit-with-parent", "-name", "n1", "-listen", addrs[0], "-wal", filepath.Join(dir, "wal"),
		"-peer", "n2="+addrs[1], "-peer", "n3="+addrs[2])
	node.Stdin, node.Stdout, node.Stderr = r, out, out
	if err := node.Start(); err != nil {
		t.Fatal(err)
	}
	r.Close()
	w.Close()
	t.Cleanup(func() { _ = node.Process.Kill(); _ = parent.Process.Kill() })
	exited := make(chan error, 1)
	go func() { exited <- node.Wait() }()
	select {
	case err := <-exited:
		t.Fatalf("the node exited before its parent was killed: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	_ = parent.Process.Kill()
	_ = parent.Wait()
	select {
	case <-exited:
		b, _ := os.ReadFile(out.Name())
		if !bytes.Contains(b, []byte("the parent is gone")) {
			t.Errorf("the node exited, not on its parent's end:\n%s", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the node outlived its parent by 5s")
	}
}

// The real tier's node addresses are below the kernel's ephemeral range,
// where no ":0" listener (a toxiproxy proxy's) or outgoing connection
// takes them before the node binds: picked by listening on ":0", one was
// once a proxy's by the time n3 started.
func TestFreeAddrsBelowTheEphemeralRange(t *testing.T) {
	low := ephemeralLow()
	seen := map[string]bool{}
	for range 3 {
		for _, a := range freeAddrs(t, len(Nodes)) {
			_, port, err := net.SplitHostPort(a)
			if err != nil {
				t.Fatal(err)
			}
			if p, _ := strconv.Atoi(port); p >= low || p < 1024 {
				t.Errorf("%s: port %d is not below the ephemeral range (from %d)", a, p, low)
			}
			if seen[a] {
				t.Errorf("%s handed out twice", a)
			}
			seen[a] = true
		}
	}
}

// The nodes agree only if each answers at its own address as itself: CI's
// n3 never started (its port a proxy's), and its address, answering as
// n1 through the proxy, made the cluster look agreed.
func TestAgreementNeedsEachNodeAtItsAddress(t *testing.T) {
	st := func(node string) Status { return Status{Node: node, Up: true, Leader: "n1", Term: 2, Applied: 7} }
	if _, ok := agreement([]Status{st("n1"), st("n2"), st("n3")}); !ok {
		t.Fatal("three nodes agreeing: no agreement")
	}
	impostor := []Status{st("n1"), st("n2"), st("n1")}
	if _, ok := agreement(impostor); ok {
		t.Fatal("n3's address answering as n1: an agreement")
	}
	if got := impostors(impostor); !strings.Contains(got, `n3's address answered as "n1"`) {
		t.Fatalf("impostors: %q; want n3's address answering as n1", got)
	}
	behind := []Status{st("n1"), st("n2"), st("n3")}
	behind[2].Applied = 6
	if _, ok := agreement(behind); ok {
		t.Fatal("n3 behind: an agreement")
	}
}
