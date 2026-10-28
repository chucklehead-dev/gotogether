package counters

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// readyPoll is how often the test's router asks each endpoint's /readyz:
// its view of the pods lags theirs by up to this, as kube-proxy's lags
// the pods' readiness.
const readyPoll = 200 * time.Millisecond

// A router is a load balancer in front of the pods, as a Kubernetes
// Service is: it sends each request to a pod whose /readyz answered 200
// at its last poll (every readyPoll), round robin; with none it answers
// 503, and a request it cannot deliver (the pod's listener closed) 502,
// neither with grist's body: what grist/client counts as a refused try.
type router struct {
	mu        sync.Mutex
	endpoints map[string]*pod // by pod name: its current start
	ready     []*url.URL      // as of the last poll
	next      atomic.Uint64
	stop      chan struct{}
	done      chan struct{}
}

func newRouter() *router {
	r := &router{endpoints: map[string]*pod{}, stop: make(chan struct{}), done: make(chan struct{})}
	go r.poll()
	return r
}

// set makes p the endpoint of pod name (its new start's address).
func (r *router) set(name string, p *pod) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endpoints[name] = p
}

// poll asks every endpoint's /readyz each readyPoll.
func (r *router) poll() {
	defer close(r.done)
	hc := &http.Client{Timeout: time.Second}
	for {
		r.mu.Lock()
		eps := make([]*pod, 0, len(r.endpoints))
		for _, p := range r.endpoints {
			eps = append(eps, p)
		}
		r.mu.Unlock()
		var ready []*url.URL
		for _, p := range eps {
			resp, err := hc.Get(p.health + "/readyz")
			if err != nil {
				continue
			}
			resp.Body.Close()
			if u, err := url.Parse(p.url); err == nil && resp.StatusCode == http.StatusOK {
				ready = append(ready, u)
			}
		}
		r.mu.Lock()
		r.ready = ready
		r.mu.Unlock()
		select {
		case <-r.stop:
			return
		case <-time.After(readyPoll):
		}
	}
}

// readyCount is how many endpoints the router's view has.
func (r *router) readyCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ready)
}

func (r *router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	ready := r.ready
	r.mu.Unlock()
	if len(ready) == 0 {
		http.Error(w, "no ready endpoint", http.StatusServiceUnavailable)
		return
	}
	target := ready[r.next.Add(1)%uint64(len(ready))]
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, req)
}

// waitReady waits until the router's view has n endpoints.
func (r *router) waitReady(t *testing.T, n int) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); r.readyCount() < n; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the router's view has %d ready endpoints after 30 s, want %d", r.readyCount(), n)
		}
	}
}

// lagSamples polls p's /metrics every 50 ms until it ends, recording when
// it reported draining with every processor's lag at 0: the drain's
// settle waits for the processors' work to be done.
func lagSamples(p *pod, end <-chan struct{}) <-chan []time.Time {
	out := make(chan []time.Time, 1)
	go func() {
		var at []time.Time
		hc := &http.Client{Timeout: time.Second}
		for {
			select {
			case <-end:
				out <- at
				return
			case <-time.After(50 * time.Millisecond):
			}
			resp, err := hc.Get(p.health + "/metrics")
			if err != nil {
				continue
			}
			draining, lagging, lags := false, false, 0
			sc := bufio.NewScanner(resp.Body)
			for sc.Scan() {
				l := sc.Text()
				switch {
				case l == "grist_draining 1":
					draining = true
				case strings.HasPrefix(l, "grist_processor_lag{"):
					lags++
					lagging = lagging || !strings.HasSuffix(l, " 0")
				}
			}
			resp.Body.Close()
			if draining && lags > 0 && !lagging {
				at = append(at, time.Now())
			}
		}
	}()
	return out
}

// rollingRestart runs the app as three pods of its binary on one SQLite
// file behind the router, each with its side port and its steps in a
// directory (a file per start), while three clients call through the
// router throughout (a call every 100 ms or so each, recorded where they
// see it): rounds times, each pod in turn is sent SIGTERM, drains (its
// /metrics watched), and is started again under its POD_NAME, the next
// once the router sees it ready. Then the clients stop and every pod is
// stopped. It returns every file's steps merged, and how many drains
// reported every processor's lag at 0 before their settle.
func rollingRestart(t *testing.T, rounds int) (steps []qtrace.Step, settledAfterLag int) {
	dir := t.TempDir()
	db := filepath.Join(dir, "counters.db")
	stepsDir := filepath.Join(dir, "steps")
	if err := os.Mkdir(stepsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{"counters-0", "counters-1", "counters-2"}
	r := newRouter()
	defer func() { close(r.stop); <-r.done }()
	pods := map[string]*pod{}
	files := map[*pod]string{} // each start's steps file
	startAt := func(n string) {
		before, _ := filepath.Glob(filepath.Join(stepsDir, "*.jsonl"))
		p := startPod(t, n, db, stepsDir, "-health-addr", "127.0.0.1:0")
		after, _ := filepath.Glob(filepath.Join(stepsDir, "*.jsonl"))
		for _, f := range after {
			if !slices.Contains(before, f) {
				files[p] = f
			}
		}
		pods[n] = p
		r.set(n, p)
	}
	for _, n := range names {
		startAt(n)
	}
	r.waitReady(t, len(names))
	front := httptest.NewServer(r)
	defer front.Close()
	clientsPath := filepath.Join(dir, "clients.jsonl")
	sink, err := qobs.FileSink(clientsPath, qobs.PerStep)
	if err != nil {
		t.Fatal(err)
	}
	clients := qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink, qobs.WithID("clients")))
	var stop atomic.Bool
	var wg sync.WaitGroup
	for c := 1; c <= 3; c++ {
		wg.Go(func() {
			ctx := qobs.Thread(clients) // one thread per goroutine
			// retries that never run out: a refused try is the claim's to
			// count, not a call's to lose
			cl := &client.Client{App: App, URL: front.URL, CallerHeader: "X-Caller", RetryDelay: 10 * time.Millisecond,
				MaxAttempts: 100, Budget: &client.Budget{Ratio: 100, Max: 1000}}
			rnd := rand.New(rand.NewPCG(11, uint64(c)))
			for op := 1; !stop.Load(); op++ {
				env := grist.Envelope{ID: fmt.Sprintf("c%d-%d", c, op), Tenant: "t1", Caller: fmt.Sprint("client", c)}
				counter := []string{"a", "b", "c"}[rnd.IntN(3)]
				var name, args string
				switch rnd.IntN(3) {
				case 0:
					name, args = "create-counter", fmt.Sprintf(`{"counter":%q,"name":%q}`, counter, counter)
				case 1:
					name, args = "increment-counter", fmt.Sprintf(`{"counter":%q,"by":%d}`, counter, 1+rnd.IntN(3))
				default:
					name, args = "show-counter", fmt.Sprintf(`{"counter":%q}`, counter)
				}
				if err := cl.Do(ctx, env, name, []byte(args)); err != nil {
					var rej *grist.Rejected
					if !errors.As(err, &rej) && !errors.Is(err, grist.ErrConflict) {
						t.Errorf("%s %s: %v", name, env.ID, err)
					}
				}
				time.Sleep(time.Duration(50+rnd.IntN(100)) * time.Millisecond)
			}
		})
	}
	type drained struct {
		file  string
		lagAt []time.Time // when its /metrics showed every lag at 0
	}
	var drains []drained
	for round := 0; round < rounds; round++ {
		for _, n := range names {
			time.Sleep(500 * time.Millisecond)
			p := pods[n]
			ended := make(chan struct{})
			samples := lagSamples(p, ended)
			p.stop(t)
			close(ended)
			drains = append(drains, drained{files[p], <-samples})
			startAt(n)
			r.waitReady(t, len(names))
		}
	}
	time.Sleep(500 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	var stopping sync.WaitGroup
	for _, n := range names {
		stopping.Go(func() { pods[n].stop(t) })
	}
	stopping.Wait()
	all, _ := filepath.Glob(filepath.Join(stepsDir, "*.jsonl"))
	if want := len(names) * (rounds + 1); len(all) != want {
		t.Errorf("%d step files, want one per start (%d)", len(all), want)
	}
	settles := map[string]time.Time{} // each start's settle, from its file
	for _, f := range append(all, clientsPath) {
		fs := readSteps(t, f)
		for _, s := range fs {
			if s.Settled {
				settles[f] = s.Time
			}
		}
		steps = append(steps, fs...)
	}
	// each drain of the rolling restart: its settle recorded after its
	// /metrics showed every processor's lag at 0
	for _, d := range drains {
		st, ok := settles[d.file]
		if ok && len(d.lagAt) > 0 && !d.lagAt[0].After(st) {
			settledAfterLag++
		} else {
			t.Logf("%s: settle %v (recorded %v), lag at 0 in %d samples", filepath.Base(d.file), st, ok, len(d.lagAt))
		}
	}
	return steps, settledAfterLag
}

// refusedCalls counts the clients' calls that had a try refused (their
// step's refused field), for the log.
func refusedCalls(steps []qtrace.Step) int {
	n := 0
	for _, s := range steps {
		if s.Args["refused"] != nil {
			n++
		}
	}
	return n
}

// GR10 slice 2's real half: a rolling restart of three pods of the app's
// binary on one SQLite file, behind a router that learns of each pod's
// readiness up to 200 ms late, three recorded clients calling through it
// throughout. The merged files reconstruct with no duplicate (a file per
// start), conform to the store's binding and settle; the clients' calls
// linearize, and none had a try refused (NoClientErrors): each pod keeps
// serving for its drain's delay (Drain: 5 s), long past the router's
// lag, before its listener closes. Each drain's /metrics showed every
// processor's lag at 0 before its settle was recorded. The mutant
// noDrainDelay, its listener closed at the signal, gets refused tries in
// the router's lag (TestMutant). Full mode: it takes about half a
// minute of real time.
func TestRealRollingRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("full mode: three pods' rolling restart takes about half a minute")
	}
	if qtest.Mutant() != "" {
		t.Skip("a mutant's binary: its drain is the mutant's (TestMutant)")
	}
	qtest.RequireQuint(t)
	start := time.Now()
	steps, settled := rollingRestart(t, 1)
	t.Logf("three pods restarted in turn in %v, %d steps, %d drains settled after their lag reached 0, %d calls refused a try",
		time.Since(start).Round(time.Millisecond), len(steps), settled, refusedCalls(steps))
	if d := duplicates(steps); len(d) > 0 {
		t.Fatalf("the pods' files merged: %d duplicate errors, the first: %s", len(d), d[0])
	}
	if settled != 3 {
		t.Errorf("%d of 3 drains recorded their settle after their /metrics showed every processor's lag at 0", settled)
	}
	qtest.Validate(t, commits, steps).Expect(t, qtest.Conforms(), qtest.Settles())
	qtest.Validate(t, calls, steps, qtest.Linearize()).Expect(t, qtest.Conforms(), gristtest.NoClientErrors())
}
