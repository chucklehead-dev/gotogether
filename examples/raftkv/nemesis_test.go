package raftkv

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qnemesis"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The real tier under a nemesis (qnemesis): three raftkv-node processes,
// each a qnemesis.Process, their peer links through toxiproxy, crashed,
// paused and cut off on a seeded schedule while clients write; the
// clients' history and the cluster's (the nodes' logs with the nemesis's
// steps) judged by the bindings the simulation uses.

// toxiproxyServer starts a toxiproxy-server for the test, or skips it,
// and returns its API's URL.
func toxiproxyServer(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("toxiproxy-server")
	if err != nil {
		t.Skip("toxiproxy-server not on PATH (go install github.com/Shopify/toxiproxy/v2/cmd/server@v2.12.0, renamed toxiproxy-server)")
	}
	port := freeAddrs(t, 1)[0]
	host, p, _ := splitHostPort(port)
	srv := &qnemesis.Process{Name: "toxiproxy", Cmd: func(int) *exec.Cmd { return exec.Command(bin, "-host", host, "-port", p) }}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	url := "http://" + port
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if resp, err := http.Get(url + "/version"); err == nil {
			resp.Body.Close()
			return url
		}
		if time.Now().After(deadline) {
			t.Fatal("toxiproxy-server did not start")
		}
	}
}

func splitHostPort(addr string) (host, port string, err error) {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], addr[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("no port in %q", addr)
}

// Think is a real client's pause between its operations.
const Think = 150 * time.Millisecond

// nemesisRun is one real run's shape.
type nemesisRun struct {
	Seed     int64
	Mutation Mutation
	// Mix is the node faults Random draws, Faults of them, one every
	// Interval, each undone after For.
	Mix      qnemesis.Mix
	Interval time.Duration
	For      time.Duration
	// ClientLoss loses the responses of this share of the clients'
	// connections (0: none), all through the run: a client that times out
	// retries, which is what exposes NoRequestID.
	ClientLoss float64
	// NoRecorders leaves the nodes' Recorders and Logs unnamed (an
	// ablation): a crash then declares no tail lost, and a kill between
	// an effect and its step's record could fail a run falsely.
	NoRecorders bool
}

// realVerdicts are a run's two checks, and where its logs are.
type realVerdicts struct {
	KV, Cluster *qtest.Run
	Dir         string
	Journal     *qnemesis.Journal
}

// runUnderNemesis runs the cluster under cfg's schedule in dir and judges
// it.
func runUnderNemesis(t *testing.T, bin, proxyURL, dir string, cfg nemesisRun) realVerdicts {
	t.Helper()
	ctx := context.Background()
	steps := filepath.Join(dir, "steps")
	if err := os.MkdirAll(steps, 0o755); err != nil {
		t.Fatal(err)
	}
	addrs := freeAddrs(t, len(Nodes))
	// the peer links, one proxy per direction: node i dials peers[i][j]
	// to reach j
	peers := &qnemesis.Toxiproxy{URL: proxyURL}
	t.Cleanup(func() { _ = peers.Close() })
	peerAddr := make([][]string, len(Nodes))
	for i, a := range Nodes {
		peerAddr[i] = make([]string, len(Nodes))
		for j, b := range Nodes {
			if i == j {
				continue
			}
			p, err := peers.Link(a, b, addrs[j])
			if err != nil {
				t.Fatal(err)
			}
			peerAddr[i][j] = p
		}
	}
	// the clients' links, a network of their own: an isolate cuts a
	// node's peer links only, as in the simulation, where clients are of
	// no process
	clientsNet := &qnemesis.Toxiproxy{URL: proxyURL}
	t.Cleanup(func() { _ = clientsNet.Close() })
	clientAddr := make([]string, len(Nodes))
	for j, b := range Nodes {
		p, err := clientsNet.Link("clients", b, addrs[j])
		if err != nil {
			t.Fatal(err)
		}
		clientAddr[j] = p
	}

	var pipes struct {
		sync.Mutex
		files []*os.File
	}
	t.Cleanup(func() {
		pipes.Lock()
		defer pipes.Unlock()
		for _, f := range pipes.files {
			f.Close()
		}
	})
	targets := map[string]qnemesis.Target{}
	var procs []*qnemesis.Process
	for i, name := range Nodes {
		logOf := func(inc int) string { return filepath.Join(steps, fmt.Sprintf("node-%s.%d.jsonl", name, inc)) }
		p := &qnemesis.Process{Name: name, PIDFile: filepath.Join(dir, name+".pgid"),
			Cmd: func(inc int) *exec.Cmd {
				args := []string{"-exit-with-parent", "-name", name, "-incarnation", strconv.Itoa(inc), "-listen", addrs[i],
					"-wal", filepath.Join(dir, "wal", name), "-steps", logOf(inc), "-mutation", mutationName(cfg.Mutation)}
				for j, n := range Nodes {
					if j != i {
						args = append(args, "-peer", n+"="+peerAddr[i][j])
					}
				}
				cmd := exec.Command(bin, args...)
				// the node exits when this process is gone (kill -9, a
				// -timeout): the pipe's write end is held here alone
				r, w, err := os.Pipe()
				if err != nil {
					t.Error(err)
					return nil
				}
				out, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s.%d.log", name, inc)))
				if err != nil {
					t.Error(err)
					return nil
				}
				cmd.Stdin, cmd.Stdout, cmd.Stderr = r, out, out
				pipes.Lock()
				pipes.files = append(pipes.files, r, w, out)
				pipes.Unlock()
				return cmd
			}}
		if !cfg.NoRecorders {
			// a kill may land between an effect and its step: the crash
			// declares the incarnation's tail lost, up to its reaping (a
			// settle after it stands)
			p.Recorders = func(inc int) []string { return []string{fmt.Sprintf("%s.%d", name, inc)} }
			p.Logs = func(inc int) []string { return []string{logOf(inc)} }
		}
		t.Cleanup(func() { _ = p.Close() })
		procs = append(procs, p)
		targets[name] = p
	}
	direct := HTTPNet{Addrs: addrs}
	for _, p := range procs {
		if err := p.Start(ctx); err != nil {
			t.Fatal(err)
		}
	}
	agreed(t, direct, 15*time.Second)

	// the nemesis records the faults the cluster's model has, in its
	// process: one actor's trace whose process does not change
	rec, nsink, err := qnemesis.NewRecorder(filepath.Join(steps, "nemesis.jsonl"), qobs.WithProcess("cluster"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := os.Create(filepath.Join(dir, "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	nem := &qnemesis.Nemesis{Targets: targets, Net: peers, Rec: rec, Journal: journal,
		Steps: qnemesis.StepsIn(filepath.Join(steps, "node-*.jsonl")),
		Record: qnemesis.Recording{
			"crash":   func(e qnemesis.Event) (string, []any) { return "stop", []any{"node", e.Names[0]} },
			"isolate": func(e qnemesis.Event) (string, []any) { return "isolate", []any{"node", e.Names[0]} },
			"heal":    func(qnemesis.Event) (string, []any) { return "heal", nil },
		}}
	if cfg.ClientLoss > 0 {
		for _, b := range Nodes {
			if err := clientsNet.Loss(ctx, "clients", b, cfg.ClientLoss, 200*time.Millisecond); err != nil {
				t.Fatal(err)
			}
		}
	}

	sink, err := qobs.FileSink(filepath.Join(steps, "clients.jsonl"), qobs.PerStep)
	if err != nil {
		t.Fatal(err)
	}
	cctx := qobs.WithRecorder(ctx, qobs.NewRecorder(sink, qobs.WithID("clients")))
	client := Client{Net: HTTPNet{Addrs: clientAddr}, Nodes: len(Nodes), Mutation: cfg.Mutation}
	var faulting atomic.Bool
	faulting.Store(true)
	var wg sync.WaitGroup
	for id := 1; id <= 3; id++ {
		wg.Go(func() {
			// at least six operations, and on until the faults end, a
			// think between them: a history short enough to linearize
			for k := 0; k < 6 || faulting.Load(); k++ {
				client.Do(cctx, id, k+1, Workload(id, k), -1)
				time.Sleep(Think)
			}
		})
	}
	sched := qnemesis.Random(cfg.Seed, cfg.Mix, cfg.Interval, cfg.For)
	runErr := nem.Run(ctx, sched)
	faulting.Store(false)
	if cfg.ClientLoss > 0 {
		for _, b := range Nodes {
			_ = clientsNet.Clear(ctx, "clients", b)
		}
	}
	if err := nem.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	journal.Close()
	if runErr != nil {
		t.Fatalf("the nemesis: %v", runErr)
	}
	nsink.Close()
	agreed(t, direct, 15*time.Second)

	// the probe and the settles, as TestRealCluster's
	for i := range Nodes {
		s, err := direct.State(ctx, i)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range Keys {
			qobs.Invoke(cctx, "observe", "node", s.Node, "key", k, "value", s.KV[k]).Done(nil)
		}
	}
	qobs.Settled(cctx)
	sink.Close()
	hsink, err := qobs.FileSink(filepath.Join(steps, "harness.jsonl"), qobs.PerStep)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // past RealTimeSlack after the nodes' last step
	qobs.NewRecorder(hsink, qobs.WithID("harness"), qobs.WithProcess("cluster")).Settled(ctx)
	hsink.Close()
	for _, p := range procs {
		_ = p.Close()
	}

	v := realVerdicts{Dir: dir}
	if f, err := os.Open(filepath.Join(dir, "journal.jsonl")); err == nil {
		v.Journal, _ = qnemesis.ReadJournal(f)
		f.Close()
	}
	v.KV = qtest.Validate(t, "raftkv.binding.yaml", readSteps(t, filepath.Join(steps, "clients.jsonl")),
		qtest.Linearize(), qtest.ResolvePending(), qtest.LinearizeBudget(50000))
	cluster := readSteps(t, filepath.Join(steps, "node-*.jsonl"))
	cluster = append(cluster, readSteps(t, filepath.Join(steps, "nemesis.jsonl"))...)
	cluster = append(cluster, readSteps(t, filepath.Join(steps, "harness.jsonl"))...)
	v.Cluster = qtest.Validate(t, "cluster.binding.yaml", cluster)
	return v
}

// noRequestIDCaught is how a run shows NoRequestID, as the simulation's
// TestPendingTransfers states it: a retried transfer applied twice, which
// a read, a cas, a transfer's answer or the probe after the settle
// contradicts.
var noRequestIDCaught = qtest.AnyOf(qtest.Mismatch("read"), qtest.Mismatch("cas"), qtest.Mismatch("transfer"),
	qtest.Violates(validate.EventuallyName("converged")))

// agreed waits until every node is up and they agree on the leader, the
// term and the applied index.
func agreed(t *testing.T, net HTTPNet, d time.Duration) {
	t.Helper()
	c := &realCluster{t: t, net: net}
	c.agreed(context.Background(), d)
}

func mutationName(m Mutation) string {
	for k, v := range Mutations {
		if v == m {
			return k
		}
	}
	return ""
}

// realMix is the node faults of a real run: a crash, a pause or an
// isolate of any node, three of them, one every 800ms, each undone after
// 300ms; and the responses of four client connections in five lost all
// through it (toxiproxy draws per connection: a client that times out
// retries on a new one).
var realMix = qnemesis.Mix{Kinds: append(append(qnemesis.Kinds("crash", Nodes...), qnemesis.Kinds("pause", Nodes...)...),
	qnemesis.Kinds("isolate", Nodes...)...), Faults: 3}

func realConfig(seed int64, m Mutation) nemesisRun {
	return nemesisRun{Seed: seed, Mutation: m, Mix: realMix, Interval: 800 * time.Millisecond, For: 300 * time.Millisecond, ClientLoss: 0.8}
}

// realRuns runs n real runs of mutation m under the nemesis, seeds 1 to
// n (after GOTOGETHER_SEED_OFFSET, the nightly's), each in a directory of
// its own (under RAFTKV_REAL_DIR if set, kept), and returns their
// verdicts; a faithful run that does not conform has its replay hint
// logged.
func realRuns(t *testing.T, n int, m Mutation) (kv, cluster []*qtest.Run) {
	t.Helper()
	if os.Getenv("RAFTKV_REAL") != "1" {
		t.Skip("RAFTKV_REAL=1 runs the real tier: three raftkv-node processes on localhost, toxiproxy-server on PATH")
	}
	qtest.RequireQuint(t)
	url := toxiproxyServer(t)
	dir := t.TempDir()
	if keep := os.Getenv("RAFTKV_REAL_DIR"); keep != "" {
		dir = filepath.Join(keep, t.Name())
	}
	bin := buildNode(t, dir)
	offset := int64(envInt("GOTOGETHER_SEED_OFFSET", 0))
	for seed := offset + 1; seed <= offset+int64(n); seed++ {
		start := time.Now()
		v := runUnderNemesis(t, bin, url, filepath.Join(dir, fmt.Sprintf("seed%d", seed)), realConfig(seed, m))
		t.Logf("seed %d (%v): the clients' history ok %v (NoRequestID's failure %v), the cluster's ok %v, settled %v", seed,
			time.Since(start).Round(100*time.Millisecond), v.KV.OK(), noRequestIDCaught.Holds(v.KV), v.Cluster.OK(), qtest.Settles().Holds(v.Cluster))
		if m == Faithful && (!v.KV.OK() || !v.Cluster.OK()) {
			logReplay(t, v)
		}
		kv, cluster = append(kv, v.KV), append(cluster, v.Cluster)
	}
	return kv, cluster
}

// logReplay says how to re-make a failed real run's faults in the
// simulation: its journal's Script, and what the script cannot make.
func logReplay(t *testing.T, v realVerdicts) {
	t.Helper()
	if v.Journal == nil {
		t.Logf("%s: no journal", v.Dir)
		return
	}
	sc := v.Journal.Script()
	t.Logf("%s: its faults in the simulation: RAFTKV_REAL_JOURNAL=%s go test -run TestRealFaultsInSimulation -v . (script %v, CutLength %v)",
		v.Dir, filepath.Join(v.Dir, "journal.jsonl"), sc.Faults, v.Journal.LongestCut())
	for _, u := range v.Journal.Unreproducible() {
		t.Logf("  not as it happened: %s", u)
	}
}

// envInt is the environment's number of runs, or def.
func envInt(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return n
	}
	return def
}

// Under crashes, pauses and isolated nodes, and clients that lose half
// their responses, the real cluster's clients see a linearizable store
// (with the calls a client gave up on placed, ResolvePending) that
// converges after the faults; the nodes' history with the nemesis's
// steps has one leader a term, a leader again after the settle and
// within 300ms of a heal, and a restarted node caught up within 1s.
// RAFTKV_REAL_RUNS runs (3 by default; CI's nightly 10). Measured: every
// faithful run conforms and settles, 33 of 33 (16 at this loss, 6 of
// them with -race; 13 at half of it; 4 without), 6 to 17s each; with the
// nodes' Recorders named, 8 of 8, the 4 with a crash among them (their
// losses bounded by the reaping, the settles after it stand).
func TestRealNemesis(t *testing.T) {
	kv, cluster := realRuns(t, envInt("RAFTKV_REAL_RUNS", 3), Faithful)
	qtest.Runs(kv...).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	qtest.Runs(cluster...).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
}

// A client that retries under a new request id (NoRequestID, the
// client's: a real client carries it through Client.Do) applies a
// transfer whose response was lost twice, which no placement of its
// calls explains: the simulation's claim (TestPendingTransfers), over
// real runs. Measured at this loss: 22 of 24 runs caught it (10 of 12;
// 12 of 12 with -race), the nodes' history conforming in all. At half
// of it 28 of 55 (19 of 30; 9 of 25 with -race, the slower nodes
// answering in time more often), and without lost responses 2 of 20,
// which is why the clients lose them. At a rate of 0.73 (the lower 95%
// bound of 22 of 24) three runs miss it with a chance of 2% and five of
// 0.14%: RAFTKV_REAL_MUTANT_RUNS is 3 by default, CI's nightly 5.
func TestRealNoRequestID(t *testing.T) {
	kv, cluster := realRuns(t, envInt("RAFTKV_REAL_MUTANT_RUNS", 3), NoRequestID)
	qtest.Runs(kv...).Expect(t, qtest.Some(noRequestIDCaught))
	qtest.Runs(cluster...).Expect(t, qtest.Conforms())
}
