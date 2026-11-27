package raftkv

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qnemesis"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
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
		qtest.Optional(t, "toxiproxy-server not on PATH (go install github.com/Shopify/toxiproxy/v2/cmd/server@v2.12.0, renamed toxiproxy-server)")
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
	// Recorders names each incarnation's recorder and log
	// (qnemesis.Process's Recorders and Logs), so a crash declares its
	// tail lost up to the reaping: an ablation. That protection is for a
	// program that records a step on return, after its effect may have
	// been seen (a reply sent, then killed before the record), where a
	// step another recorder saw would be rejected falsely. A raftkv node
	// has no such window: it records each cluster step under inc.mu
	// before anything of it is visible (a lead or a campaign before any
	// message of its term is sent, an apply before a handler waiting on
	// it can answer), and the step is in the file when Record returns.
	// Declared, the loss would make every failure after a killed
	// incarnation's last step no verdict: a second leader of a term after
	// a crash, which is what NoPersistVote makes, could not be judged.
	Recorders bool
	// Cut, if set, cuts every node off from the others for Cut, Cuts
	// times, once the schedule is done, each when the nodes agree,
	// recorded as the simulation's TestElectionAfterHeal records it
	// (isolate each, then heal): the leader steps down, no one can be
	// elected, and after the heal one must be within the binding's bound.
	// It is what tells SlowElection from the faithful timeout; each cut
	// is another chance.
	Cut  time.Duration
	Cuts int
}

// realVerdicts are a run's two checks, and where its logs are.
type realVerdicts struct {
	KV, Cluster *qtest.Run
	Dir         string
	Journal     *qnemesis.Journal
	// Host is what the harness measured of the host while the run ran
	// (watchHost): its stalls excuse a bound they could have broken
	Host qnemesis.HostReport
}

// stallThreshold is the shortest host stall the real runs record: a
// fifth of cluster.real.binding.yaml's tighter bound (a leader within
// 500ms of a heal), and over half the headroom the faithful cluster's
// slowest measured election (320ms with -race) leaves under it. A
// shorter stall cannot make a faithful run late by itself, and the
// probes' own delays stay under it (on a loaded four-core machine with
// -race, ticks up to 78ms late and fsyncs up to 93ms in runs without a
// stall: docs/HISTORY.md, 2026-11-25). A
// stall excuses a late response only if it covers its lateness
// (validate.Report.ExcuseStalls), so SlowElection's, 300ms to 600ms late,
// is not excused by a stall of this length.
const stallThreshold = 100 * time.Millisecond

// watchHost starts measuring the host's stalls from the harness, not the
// nodes: a 10ms ticker and an fsync in dir, on the disk the nodes' logs
// are on, every 20ms. CI's one faithful failure (seed 5, 2026-11-25) was
// a leader 1.15s after a heal: a stall of every node's fsync, which only
// a probe of the disk sees.
func watchHost(t *testing.T, dir string) *qnemesis.HostMonitor {
	t.Helper()
	m, err := qnemesis.WatchHost(qnemesis.HostWatch{Every: 10 * time.Millisecond, Threshold: stallThreshold, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop() })
	return m
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
	host := watchHost(t, dir)
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
		if cfg.Recorders {
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
	agreed(t, direct, dir, 15*time.Second)

	// the nemesis records the faults the cluster's model has, in its
	// process: one actor's trace whose process does not change
	rec, err := qnemesis.NewRecorder(filepath.Join(steps, "nemesis.jsonl"), qobs.WithProcess("cluster"))
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
	// a new connection per call: toxiproxy draws a loss per connection,
	// and a kept-alive one would lose every response or none for the run
	noKeepAlive := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	client := Client{Net: HTTPNet{Addrs: clientAddr, Client: noKeepAlive}, Nodes: len(Nodes), Mutation: cfg.Mutation}
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
	downByItself(t, dir)
	for range cfg.Cuts {
		agreed(t, direct, dir, 15*time.Second)
		for i, a := range Nodes {
			for _, b := range Nodes[i+1:] {
				if err := peers.Cut(ctx, a, b); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, n := range Nodes { // in effect: recorded after the cut, as qnemesis records one
			rec.Record(ctx, "isolate", nil, "node", n)
		}
		time.Sleep(cfg.Cut)
		rec.Record(ctx, "heal", nil) // before the heal, as qnemesis records one
		for i, a := range Nodes {
			for _, b := range Nodes[i+1:] {
				if err := peers.Heal(ctx, a, b); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	rec.Close()
	agreed(t, direct, dir, 15*time.Second)

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
	// every node stopped (SIGSTOP) before the settle: a late step (an
	// election timer firing) between the settle and the kill would void it
	// Dispatch every stop before waiting for group/thread confirmations. A
	// sequential Pause can let followers start an election while the leader's
	// stopped-state scan runs, changing the history being captured.
	if err := qnemesis.PauseProcesses(ctx, procs...); err != nil {
		t.Fatal(err)
	}
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

	v := realVerdicts{Dir: dir, Host: host.Stop()}
	if f, err := os.Open(filepath.Join(dir, "journal.jsonl")); err == nil {
		v.Journal, _ = qnemesis.ReadJournal(f)
		f.Close()
	}
	v.KV = qtest.Validate(t, "raftkv.binding.yaml", readSteps(t, filepath.Join(steps, "clients.jsonl")),
		qtest.Linearize(), qtest.ResolvePending(), qtest.LinearizeBudget(50000), qtest.HostStalls(v.Host.Stalls...))
	cluster := readSteps(t, filepath.Join(steps, "node-*.jsonl"))
	cluster = append(cluster, readSteps(t, filepath.Join(steps, "nemesis.jsonl"))...)
	cluster = append(cluster, readSteps(t, filepath.Join(steps, "harness.jsonl"))...)
	v.Cluster = qtest.Validate(t, "cluster.real.binding.yaml", cluster, qtest.HostStalls(v.Host.Stalls...))
	return v
}

// downByItself fails the run if a node went down by itself during it,
// as its journal says (Stop's start of a target no fault had stopped):
// only the nemesis stops a node here, and a run with one missing (n3 once
// could not bind its address, its port another process's) is not the
// run its schedule describes.
func downByItself(t *testing.T, dir string) {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	j, err := qnemesis.ReadJournal(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range j.Entries {
		if e.Index < 0 {
			name := strings.TrimPrefix(e.Kind, "nemesis.restart.")
			t.Fatalf("%s went down by itself during the run (Stop started its incarnation %d): its logs %s say why",
				name, e.Incarnation, filepath.Join(dir, name+".*.log"))
		}
	}
}

// noRequestIDCaught is how a run shows NoRequestID, as the simulation's
// TestPendingTransfers states it: a retried transfer applied twice, which
// a read, a cas, a transfer's answer or the probe after the settle
// contradicts.
var noRequestIDCaught = qtest.AnyOf(qtest.Mismatch("read"), qtest.Mismatch("cas"), qtest.Mismatch("transfer"),
	qtest.Violates(validate.EventuallyName("converged")))

// agreed waits until every node is up and they agree on the leader, the
// term and the applied index.
func agreed(t *testing.T, net HTTPNet, dir string, d time.Duration) {
	t.Helper()
	c := &realCluster{t: t, net: net, dir: dir}
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
	return nemesisRun{Seed: seed, Mutation: m, Mix: realMix, Interval: 800 * time.Millisecond, For: 300 * time.Millisecond, ClientLoss: 0.8,
		Cut: 2500 * time.Millisecond, Cuts: 6}
}

// realRuns runs the real cluster under the nemesis at seeds (seedsOf),
// cfgOf making each run's shape, each in a directory of its own (under
// RAFTKV_REAL_DIR if set, kept), and returns each seed's runs: a seed
// whose run has no verdict for a host stall alone is run again
// (untilJudged); a faithful run that does not conform has its replay
// hint logged.
func realRuns(t *testing.T, seeds []int64, cfgOf func(seed int64) nemesisRun) []seedRuns {
	t.Helper()
	if os.Getenv("RAFTKV_REAL") != "1" {
		t.Skip("RAFTKV_REAL=1 runs the real tier: three raftkv-node processes on localhost, toxiproxy-server on PATH")
	}
	qtest.RequireQuint(t)
	dir := realDir(t)
	url := toxiproxyServer(t)
	bin := buildNode(t, dir)
	var out []seedRuns
	for _, seed := range seeds {
		cfg := cfgOf(seed)
		out = append(out, untilJudged(t, dir, fmt.Sprintf("seed%d", seed), func(name string) realVerdicts {
			start := time.Now()
			v := runUnderNemesis(t, bin, url, filepath.Join(dir, name), cfg)
			t.Logf("%s (%v): the clients' history ok %v (NoRequestID's failure %v), the cluster's ok %v, settled %v", name,
				time.Since(start).Round(100*time.Millisecond), v.KV.OK(), noRequestIDCaught.Holds(v.KV), v.Cluster.OK(), qtest.Settles().Holds(v.Cluster))
			t.Logf("  the host: %s", v.Host)
			if !v.Cluster.OK() && !v.Cluster.Stalled() {
				t.Logf("  the cluster's: %s", strings.TrimSpace(v.Cluster.Report.Summary()))
			}
			if cfg.Mutation == Faithful && (!v.KV.OK() && !v.KV.Stalled() || !v.Cluster.OK() && !v.Cluster.Stalled()) {
				logReplay(t, v)
			}
			return v
		}))
	}
	return out
}

// stallRetries is how many times a seed is run again while a history of
// its runs has no verdict for a host stall alone (qtest.Run.Stalled: a
// bound broken while the harness measured a stall that could have broken
// it): "run it again on a quiet host", automated. CI's hosted runners
// stall their disk's fsync for over 100ms several times a run, and one
// run in three once had a stall excuse a late leader (PR #284's seed 10:
// 14ms late, the host stalled 512ms of the 514ms before it). A stall long
// enough to excuse a bound is the rare case: two more runs make three in
// a row, which says the host cannot judge the tier, and the claims fail
// on it.
const stallRetries = 2

// seedRuns are one seed's real runs, in order: the first, then each run
// again after one that had a history with no verdict (host stall).
type seedRuns struct {
	Attempts []realVerdicts
}

// kv is the run whose clients' history judges the seed, cluster the one
// whose cluster history does: for each history, the first run with a
// verdict for it, else the last (no verdict, which the claims fail on).
// A run passed over had no verdict for that history (Stalled: nothing in
// it FAILs), so no verdict is dropped, a pass or a FAIL: a later run's
// FAIL counts as its pass would. Each history is a claim of its own, so
// a run's other history keeps its verdict (a NoRequestID run whose
// clients' history caught the mutant while its cluster's stalled keeps
// the catch).
func (s seedRuns) kv() realVerdicts { return s.first(func(v realVerdicts) *qtest.Run { return v.KV }) }

func (s seedRuns) cluster() realVerdicts {
	return s.first(func(v realVerdicts) *qtest.Run { return v.Cluster })
}

func (s seedRuns) first(of func(realVerdicts) *qtest.Run) realVerdicts {
	for _, v := range s.Attempts {
		if !of(v).Stalled() {
			return v
		}
	}
	return s.Attempts[len(s.Attempts)-1]
}

// stalled reports whether a history of the seed has no verdict yet for a
// host stall alone.
func (s seedRuns) stalled() bool { return s.kv().KV.Stalled() || s.cluster().Cluster.Stalled() }

// untilJudged runs a seed (run, given the run's directory name: name,
// then name.again1, ...) until each of its histories has a verdict, at
// most 1+stallRetries times. Each run without one is said: logged, and a
// line in inconclusive.txt in dir, which CI turns into a warning (and
// keeps the runs as an artifact). Leaving such a seed out of the claims
// instead (judged, before) left a claim over fewer seeds than its test
// names, and the mutants' claims (Some, measured per seed) lost a seed's
// chance: TestRealNoRequestID's cluster Conforms failed on the stalled
// run it kept. A run again is the seed's schedule again on another
// moment of the host, which is what the stall's message asks for.
func untilJudged(t *testing.T, dir, name string, run func(name string) realVerdicts) seedRuns {
	t.Helper()
	var s seedRuns
	for k := 0; k <= stallRetries; k++ {
		n := name
		if k > 0 {
			n = fmt.Sprintf("%s.again%d", name, k)
		}
		s.Attempts = append(s.Attempts, run(n))
		if !s.stalled() {
			break
		}
		v := s.Attempts[len(s.Attempts)-1]
		var which []string // the seed's histories still without a verdict
		for _, h := range []struct {
			name string
			r    *qtest.Run
		}{{"the clients'", s.kv().KV}, {"the cluster's", s.cluster().Cluster}} {
			if h.r.Stalled() {
				which = append(which, h.name)
			}
		}
		line := fmt.Sprintf("%s: no verdict (host stall) of %s history: %s", n, strings.Join(which, " and "), v.Host)
		if k < stallRetries {
			line += "; run again"
		} else {
			line += fmt.Sprintf("; %d runs, none with a verdict: the claims fail on it", k+1)
		}
		t.Logf("inconclusive: %s", line)
		inconclusive(t, dir, line)
	}
	return s
}

// inconclusive adds line to dir's inconclusive.txt.
func inconclusive(t *testing.T, dir, line string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, "inconclusive.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		_, err = f.WriteString(line + "\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		t.Error(err)
	}
}

// realDir is where a test's real runs write: a new temporary directory,
// or RAFTKV_REAL_DIR/<test>, which must not hold an earlier run's (its
// nodes would refuse their step logs, and its logs would be mixed).
func realDir(t *testing.T) string {
	t.Helper()
	keep := os.Getenv("RAFTKV_REAL_DIR")
	if keep == "" {
		return t.TempDir()
	}
	dir := filepath.Join(keep, t.Name())
	if es, err := os.ReadDir(dir); err == nil && len(es) > 0 {
		t.Fatalf("RAFTKV_REAL_DIR: %s holds an earlier run's logs: remove it, or give RAFTKV_REAL_DIR another directory", dir)
	}
	return dir
}

// seedsOf are the seeds a test runs: RAFTKV_REAL_SEEDS (a comma-separated
// list), else, with GOTOGETHER_SEED_OFFSET (the nightly's), the next
// $runs seeds after it ($runs the environment's number, len(def) if
// unset), else def, the per-PR seeds.
func seedsOf(t *testing.T, runs string, def []int64) []int64 {
	t.Helper()
	if list := os.Getenv("RAFTKV_REAL_SEEDS"); list != "" {
		var out []int64
		for _, f := range strings.Split(list, ",") {
			n, err := strconv.ParseInt(strings.TrimSpace(f), 10, 64)
			if err != nil {
				t.Fatalf("RAFTKV_REAL_SEEDS=%q: want numbers separated by commas", list)
			}
			out = append(out, n)
		}
		return out
	}
	if off := envInt("GOTOGETHER_SEED_OFFSET", 0); off > 0 {
		var out []int64
		for i := range envInt(runs, len(def)) {
			out = append(out, int64(off+i+1))
		}
		return out
	}
	return def
}

// kvOf are the seeds' clients' histories, each judged by its first run
// with a verdict (seedRuns.kv).
func kvOf(ss []seedRuns) (out []*qtest.Run) {
	for _, s := range ss {
		out = append(out, s.kv().KV)
	}
	return out
}

// clusterOf are the seeds' cluster histories, each judged by its first
// run with a verdict (seedRuns.cluster).
func clusterOf(ss []seedRuns) (out []*qtest.Run) {
	for _, s := range ss {
		out = append(out, s.cluster().Cluster)
	}
	return out
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
		v.Dir, filepath.Join(v.Dir, "journal.jsonl"), sc.Faults, replayConfig(v.Journal).CutLength)
	if es := v.Journal.Entries; len(es) > 0 {
		// the cuts of every node after the schedule are the harness's, not
		// the nemesis's: a failure after them (CI's seed 5, a leader 1.1s
		// after the first cut's heal) is not re-made by the journal
		t.Logf("  the journal's faults end at %s: the cuts after them (nemesisRun.Cuts) are not in it", es[len(es)-1].Time.Format(time.RFC3339Nano))
	}
	for _, u := range replayNotes(v.Journal) {
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

// prSeeds are the per-PR runs' seeds: schedules with a crash in each
// (5: a pause, a crash, an isolate; 9: a crash, a pause, a crash; 10: a
// pause, a crash, a crash), each catching both mutants below in most
// runs measured.
var prSeeds = []int64{5, 9, 10}

// Under crashes, pauses and isolated nodes, every node cut off six
// times, and clients that lose four responses in five, the real
// cluster's clients see a linearizable store (with the calls a client
// gave up on placed, ResolvePending) that converges after the faults;
// the nodes' history with the nemesis's steps has one leader a term, a
// leader again after the settle and within 500ms of each heal, and a
// restarted node caught up within 1s. The per-PR seeds (prSeeds); the
// nightly's RAFTKV_REAL_RUNS fresh ones. Measured with this shape: 24 of
// 24 runs conform and settle on both histories (18 with -race), 14 to
// 22s each. A second leader of a term added to one run's logs after its
// crashes is a violation of electionSafety: the nodes declare no lost
// tails (nemesisRun.Recorders), so a crash leaves the steps after it
// judged. A run whose bound broke while the host stalled long enough to
// break it has no verdict: its seed is run again, said (untilJudged),
// and a seed with none in three runs fails the claims.
func TestRealNemesis(t *testing.T) {
	ss := realRuns(t, seedsOf(t, "RAFTKV_REAL_RUNS", prSeeds), func(seed int64) nemesisRun { return realConfig(seed, Faithful) })
	qtest.Runs(kvOf(ss)...).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	qtest.Runs(clusterOf(ss)...).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	secondLeader(t, ss[0].cluster()).Expect(t, qtest.Violates("electionSafety"))
}

// secondLeader is v's cluster history with a node's lead of a term
// another node led added at its end, as NoPersistVote makes one, judged.
func secondLeader(t *testing.T, v realVerdicts) *qtest.Run {
	t.Helper()
	dir := filepath.Join(v.Dir, "steps")
	nodes := readSteps(t, filepath.Join(dir, "node-*.jsonl"))
	var lead *qtrace.Step
	for i := range nodes {
		if s := &nodes[i]; s.Action == "lead" && (lead == nil || s.Time.After(lead.Time)) {
			lead = s
		}
	}
	if lead == nil {
		t.Fatalf("%s: no node led", dir)
	}
	other := Nodes[(slices.Index(Nodes, lead.Args["node"].(string))+1)%len(Nodes)]
	var last qtrace.Step // other's last step: the added one follows it in its recorder
	for _, s := range nodes {
		if s.Args["node"] == other && (last.Recorder == "" || s.Time.After(last.Time)) {
			last = s
		}
	}
	at := last.Time
	if lead.Time.After(at) {
		at = lead.Time
	}
	fake := qtrace.Step{Action: "lead", Recorder: last.Recorder, Seq: last.Seq + 1, Actor: last.Actor, Process: last.Process, Outcome: "ok",
		Time: at.Add(time.Microsecond), Args: map[string]any{"node": other, "term": lead.Args["term"]}}
	steps := append(nodes, fake)
	steps = append(steps, readSteps(t, filepath.Join(dir, "nemesis.jsonl"))...)
	steps = append(steps, readSteps(t, filepath.Join(dir, "harness.jsonl"))...)
	return qtest.Validate(t, "cluster.real.binding.yaml", steps)
}

// A client that retries under a new request id (NoRequestID, the
// client's: a real client carries it through Client.Do) applies a
// transfer whose response was lost twice, which no placement of its
// calls explains: the simulation's claim (TestPendingTransfers), over
// real runs. Each call is a new connection, so toxiproxy draws its loss
// per call. Measured with this shape and -race, per seed: 5 caught it in
// 3 of 3 runs, 9 in 2 of 3, 10 in 3 of 3 (and 18 of 18 runs of seeds
// 4-10 without the cuts and -race): at each seed's worst measured miss
// rate (1 in 3) the three miss together 3.7% of the time. The nightly
// runs RAFTKV_REAL_MUTANT_RUNS fresh seeds. A seed whose cluster history
// has no verdict (host stall) is run again (untilJudged): its Conforms
// once failed on such a run (PR #284), and a clients' history that caught
// the mutant keeps its catch.
func TestRealNoRequestID(t *testing.T) {
	ss := realRuns(t, seedsOf(t, "RAFTKV_REAL_MUTANT_RUNS", prSeeds), func(seed int64) nemesisRun { return realConfig(seed, NoRequestID) })
	qtest.Runs(kvOf(ss)...).Expect(t, qtest.Some(noRequestIDCaught))
	qtest.Runs(clusterOf(ss)...).Expect(t, qtest.Conforms())
}

// An election timeout ten times as long (SlowElection, the node's) still
// elects, too late: after every node was cut off, past the 500ms bound
// after the heal (cluster.real.binding.yaml), as in the simulation's
// TestElectionAfterHeal. Not after every heal: raft draws each node's
// timeout afresh and a node cut off pre-campaigns at each, so at the
// heal it is part-way through its cycle, and the first leader can come
// early (the faithful cluster's 10 to 170ms after a heal, SlowElection's
// 317ms to 1.1s). Each cut is a chance, so the real runs cut six times:
// with three, CI once saw all three seeds elect in time after every
// heal (main after #203), and locally 3 of 18 runs missed (seed 10 3 of
// 6). With six, measured with -race: 14 of 15 runs caught it, per seed
// 5: 5 of 5, 9: 5 of 5, 10: 4 of 5; the three missed together in none
// of five tries, which needs 5 and 9 to miss, never seen in ten runs.
// A seed with no verdict (host stall) is run again (untilJudged), so
// each seed is a chance, as measured: a stalled run is no witness, and
// None(...) has no verdict of it.
func TestRealSlowElection(t *testing.T) {
	ss := realRuns(t, seedsOf(t, "RAFTKV_REAL_MUTANT_RUNS", prSeeds), func(seed int64) nemesisRun { return realConfig(seed, SlowElection) })
	qtest.Runs(clusterOf(ss)...).Expect(t, qtest.Some(qtest.Violates("within 500ms after healed: leaderOrSplit")),
		qtest.None(qtest.Violates("electionSafety")))
	qtest.Runs(kvOf(ss)...).Expect(t, qtest.Conforms())
}
