package grist

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/store"
)

// liveBound is how long a poller may work on one trigger of its batch
// before /livez fails: far longer than any handler or effect takes (an
// effect's calls time out in callTimeout, and are tried EffectTries
// times), so only a deadlock or a stuck goroutine reaches it. The clock
// restarts at each trigger and after each backoff from a store error: a
// batch of slow effects, or a commit retried through a database outage,
// is not stuck.
//
// Shortened by tests (export_test.go); read-only otherwise.
var liveBound = 5 * time.Minute

// health is Main's side port: /livez, /readyz and /metrics.
type health struct {
	app      *App
	st       atomic.Pointer[storeRef]
	relay    atomic.Pointer[Relay]
	node     atomic.Pointer[node]
	ready    atomic.Bool  // the store answered and the node started
	draining atomic.Bool  // SIGTERM came
	requests atomic.Int64 // the API's requests in flight
	red      red          // the API's requests by block and answer: rate, errors, latency
}

// red is the API's request counters (rate, errors, duration): per kind
// (command, query, other) and block, a count per status code and a
// latency histogram. A block's name is the App's (an unknown name is
// "other", so a client cannot grow the series).
type red struct {
	mu   sync.Mutex
	by   map[[2]string]*redRow
	keys [][2]string // in first-seen order, sorted when written
}

// redRow is one block's counters.
type redRow struct {
	codes   map[int]int64
	buckets []int64 // per latencyBuckets bound, cumulative when written
	count   int64
	sum     float64 // seconds
}

// latencyBuckets are the request duration histogram's upper bounds, in
// seconds: a query's milliseconds to a command's transient retries.
// Read-only.
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// observe counts one request of block (kind, name) answered code after d.
func (r *red) observe(kind, name string, code int, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.by == nil {
		r.by = map[[2]string]*redRow{}
	}
	k := [2]string{kind, name}
	row := r.by[k]
	if row == nil {
		row = &redRow{codes: map[int]int64{}, buckets: make([]int64, len(latencyBuckets))}
		r.by[k] = row
		r.keys = append(r.keys, k)
	}
	row.codes[code]++
	row.count++
	row.sum += d.Seconds()
	for i, b := range latencyBuckets {
		if d.Seconds() <= b {
			row.buckets[i]++
			break
		}
	}
}

// blockOf is a request's kind and block, by the API's routes (NewHandler):
// POST /t/{tenant}/commands/{name}, GET /t/{tenant}/queries/{name}; a
// name the App does not register, and every other path, is "other".
func blockOf(app *App, path string) (string, string) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 4 && parts[0] == "t" {
		switch {
		case parts[2] == "commands" && app.commands[parts[3]] != nil:
			return "command", parts[3]
		case parts[2] == "queries" && slices.Contains(app.Queries(), parts[3]):
			return "query", parts[3]
		}
	}
	return "other", "other"
}

// statusWriter records a response's status code.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap is the ResponseWriter wrapped (http.ResponseController's).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// storeRef boxes a Store for an atomic.Pointer.
type storeRef struct{ Store }

// started marks the node started on st: the store has answered (App.Start
// read its tenants) and the node runs, so the node is ready.
func (h *health) started(st Store, r *Relay, n *node) {
	h.st.Store(&storeRef{st})
	h.relay.Store(r)
	if n != nil {
		h.node.Store(n)
	}
	h.ready.Store(true)
}

// count wraps the API's handler, counting its requests in flight.
func (h *health) count(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.requests.Add(1)
		defer h.requests.Add(-1)
		start := time.Now() //qenv:ok the real tier's request latency, outside any simulation
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			code := sw.code
			if code == 0 {
				code = http.StatusOK // nothing written: net/http answers 200
			}
			kind, name := blockOf(h.app, r.URL.Path)
			h.red.observe(kind, name, code, time.Since(start)) //qenv:ok the real tier's request latency
		}()
		next.ServeHTTP(sw, r)
	})
}

func (h *health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/livez":
		// never the store: a database blip must not restart every pod
		if n := h.node.Load(); n != nil {
			if why := n.stuck(time.Now().UnixNano(), liveBound); why != "" {
				http.Error(w, why, http.StatusServiceUnavailable)
				return
			}
		}
		fmt.Fprintln(w, "ok")
	case "/readyz":
		// never a dependency, never processor lag: either would take every
		// pod out of the Service at once and turn a slow dependency into an
		// outage
		switch {
		case h.draining.Load():
			http.Error(w, "draining", http.StatusServiceUnavailable)
		case !h.ready.Load():
			http.Error(w, "starting: the store has not answered, or the node has not started", http.StatusServiceUnavailable)
		default:
			fmt.Fprintln(w, "ok")
		}
	case "/metrics":
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		w.Write([]byte(h.metrics(ctx, time.Now())))
	default:
		http.NotFound(w, r)
	}
}

// metrics is grist's gauges in Prometheus' text format: the node's state,
// the API's requests and the effects in flight, the relay's backlog, and
// per processor and tenant the lag (the tenant's head minus the
// processor's checkpoint while a trigger of its waits, else 0) and the
// age of the oldest trigger not yet handled. A store that does not answer leaves its gauges out, and
// grist_store_up 0.
func (h *health) metrics(ctx context.Context, now time.Time) string {
	var b strings.Builder
	gauge := func(name, help string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	}
	value := func(name string, v any, labels ...string) {
		b.WriteString(name)
		if len(labels) > 0 {
			b.WriteByte('{')
			for i := 0; i < len(labels); i += 2 {
				if i > 0 {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, "%s=\"%s\"", labels[i], labelEscaper.Replace(labels[i+1]))
			}
			b.WriteByte('}')
		}
		fmt.Fprintf(&b, " %v\n", v)
	}
	bit := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	gauge("grist_ready", "1 when /readyz answers ok.")
	value("grist_ready", bit(h.ready.Load() && !h.draining.Load()))
	gauge("grist_draining", "1 once SIGTERM came.")
	value("grist_draining", bit(h.draining.Load()))
	gauge("grist_api_requests_in_flight", "The API's requests being served.")
	value("grist_api_requests_in_flight", h.requests.Load())
	counter := func(name, help string, v int64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
	}
	h.redMetrics(&b)
	runtimeMetrics(&b)
	rs := h.app.ReadStats()
	counter("grist_fold_events_total", "Events read models folded: what reads cost.", rs.Folded)
	counter("grist_memo_hits_total", "Reads served from the node's memo of read models' states (grist.Memo).", rs.MemoHits)
	counter("grist_memo_stale_total", "Memoized states whose last event the log no longer holds (a rewind): refolded.", rs.MemoStale)
	counter("grist_snapshot_hits_total", "Snapshot lookups that found a sound snapshot.", rs.SnapshotHits)
	counter("grist_snapshot_misses_total", "Snapshot lookups that found none, or one torn, another fold's, past the read, or whose last event is gone.", rs.SnapshotMisses)
	counter("grist_snapshot_put_failures_total", "Snapshot puts the store failed (a read-only role, a full disk): reads go on folding the log.", rs.SnapshotPutFailures)
	counter("grist_snapshots_pruned_total", "Other fingerprints' snapshots deleted after a put (a retired release's).", rs.SnapshotsPruned)
	counter("grist_snapshot_audits_total", "Reads from a snapshot or the memo refolded from the start to check them (grist.AuditSnapshots).", rs.Audits)
	counter("grist_snapshot_audit_mismatches_total", "Audited states that were not the refold's: a fold changed without a new fingerprint, or a store's snapshot not what was put.", rs.AuditMismatches)
	n, ref, relay := h.node.Load(), h.st.Load(), h.relay.Load()
	if n != nil {
		gauge("grist_effects_in_flight", "Processors' effects running.")
		value("grist_effects_in_flight", n.effects.Load())
	}
	if ref == nil {
		return b.String()
	}
	st := ref.Store
	up := true
	if relay != nil {
		if behind, err := relay.Behind(ctx); err == nil {
			gauge("grist_relay_backlog", "Committed steps the relay has not yet recorded.")
			value("grist_relay_backlog", behind)
		} else {
			up = false
		}
	}
	tenants, err := st.Tenants(ctx)
	up = up && err == nil
	slices.Sort(tenants)
	type row struct {
		p, t string
		lag  int64
		ageS float64
	}
	var rows []row
	for _, t := range tenants {
		for _, p := range h.app.processors {
			cp, err := st.Checkpoint(ctx, t, p.name)
			if err != nil {
				up = false
				continue
			}
			evs, head, err := st.Read(ctx, t, store.Selector{Types: []string{p.on}, After: cp, Limit: 1}, 0)
			if err != nil {
				up = false
				continue
			}
			// lag only while a trigger waits: a processor's checkpoint stays
			// at the last trigger it handled, and the head moves on with
			// every other event, so head minus checkpoint never came back
			// to 0 for a processor that had caught up
			r := row{p: p.name, t: t}
			if len(evs) > 0 {
				r.lag = int64(head - cp)
				r.ageS = max(0, float64(now.UnixMilli()-evs[0].Time)/1000)
			}
			rows = append(rows, r)
		}
	}
	if len(rows) > 0 {
		gauge("grist_processor_lag", "Positions between the tenant's head and the processor's checkpoint while a trigger waits (0 for none).")
		for _, r := range rows {
			value("grist_processor_lag", r.lag, "processor", r.p, "tenant", r.t)
		}
		gauge("grist_processor_oldest_trigger_age_seconds", "Age of the oldest trigger the processor has not handled (0 for none).")
		for _, r := range rows {
			value("grist_processor_oldest_trigger_age_seconds", r.ageS, "processor", r.p, "tenant", r.t)
		}
	}
	gauge("grist_store_up", "1 when every read of this scrape answered.")
	value("grist_store_up", bit(up))
	if relay != nil {
		gauge("grist_rewound", "1 for a tenant whose log went backwards under this node (grist.ErrRewound): its processors are stopped until `<app> resume`.")
		rewound := map[string]bool{}
		for _, e := range relay.Rewound() {
			rewound[e.Tenant] = true
		}
		for _, t := range tenants {
			value("grist_rewound", bit(rewound[t]), "tenant", t)
		}
	}
	return b.String()
}

// redMetrics writes the API's request counters: grist_requests_total by
// kind, block and status code (rate, and errors: a code of 4xx or 5xx),
// grist_request_duration_seconds by kind and block.
func (h *health) redMetrics(b *strings.Builder) {
	h.red.mu.Lock()
	defer h.red.mu.Unlock()
	keys := slices.Clone(h.red.keys)
	slices.SortFunc(keys, func(x, y [2]string) int { return strings.Compare(x[0]+"/"+x[1], y[0]+"/"+y[1]) })
	b.WriteString("# HELP grist_requests_total The API's requests answered, by kind (command, query, other), block and status code.\n# TYPE grist_requests_total counter\n")
	for _, k := range keys {
		row := h.red.by[k]
		codes := slices.Sorted(maps.Keys(row.codes))
		for _, c := range codes {
			fmt.Fprintf(b, "grist_requests_total{kind=\"%s\",block=\"%s\",code=\"%d\"} %d\n", k[0], labelEscaper.Replace(k[1]), c, row.codes[c])
		}
	}
	b.WriteString("# HELP grist_request_duration_seconds The API's request durations, by kind and block.\n# TYPE grist_request_duration_seconds histogram\n")
	for _, k := range keys {
		row := h.red.by[k]
		labels := fmt.Sprintf("kind=\"%s\",block=\"%s\"", k[0], labelEscaper.Replace(k[1]))
		var cum int64
		for i, bound := range latencyBuckets {
			cum += row.buckets[i]
			fmt.Fprintf(b, "grist_request_duration_seconds_bucket{%s,le=\"%s\"} %d\n", labels, strconv.FormatFloat(bound, 'g', -1, 64), cum)
		}
		fmt.Fprintf(b, "grist_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", labels, row.count)
		fmt.Fprintf(b, "grist_request_duration_seconds_sum{%s} %g\n", labels, row.sum)
		fmt.Fprintf(b, "grist_request_duration_seconds_count{%s} %d\n", labels, row.count)
	}
}

// runtimeMetrics writes the Go runtime's gauges and the build's info:
// goroutines, the heap, GC cycles, GOMAXPROCS and GOMEMLIMIT, and
// grist_build_info (the main module's version and path, Go's).
func runtimeMetrics(b *strings.Builder) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	for _, g := range []struct {
		name, help string
		v          any
	}{
		{"go_goroutines", "Goroutines that exist.", runtime.NumGoroutine()},
		{"go_memstats_heap_alloc_bytes", "Bytes of allocated heap objects.", ms.HeapAlloc},
		{"go_memstats_heap_sys_bytes", "Bytes of heap obtained from the OS.", ms.HeapSys},
		{"go_gc_cycles_total", "Completed GC cycles.", ms.NumGC},
		{"go_gomaxprocs", "GOMAXPROCS.", runtime.GOMAXPROCS(0)},
		{"go_gomemlimit_bytes", "The runtime's soft memory limit (GOMEMLIMIT).", debug.SetMemoryLimit(-1)},
	} {
		typ := "gauge"
		if strings.HasSuffix(g.name, "_total") {
			typ = "counter"
		}
		fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n%s %v\n", g.name, g.help, g.name, typ, g.name, g.v)
	}
	version, path := "(unknown)", "(unknown)"
	if bi, ok := debug.ReadBuildInfo(); ok {
		version, path = bi.Main.Version, bi.Main.Path
	}
	fmt.Fprintf(b, "# HELP grist_build_info The binary's build: its main module's path and version, and Go's.\n# TYPE grist_build_info gauge\ngrist_build_info{path=\"%s\",version=\"%s\",goversion=\"%s\"} 1\n",
		labelEscaper.Replace(path), labelEscaper.Replace(version), runtime.Version())
}

// labelEscaper escapes a label value as Prometheus' text format does:
// a backslash, a double quote and a newline, nothing else (a tenant's
// name is any UTF-8; strconv.Quote's \t or \u200b is no escape there,
// and a scraper refused the whole page). Read-only.
var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// setMemoryLimit sets the Go runtime's soft memory limit to 90% of the
// cgroup's memory.max (read from path) when GOMEMLIMIT is unset, so the
// collector works harder before the container is OOM-killed (SIGKILL: no
// drain at all). It never touches GOMAXPROCS (container-aware since Go
// 1.25) or GOGC. It returns what it did, for the startup line.
func setMemoryLimit(path string) string {
	if v := os.Getenv("GOMEMLIMIT"); v != "" {
		return "GOMEMLIMIT " + v + " (set)"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "no memory limit (no cgroup memory.max)"
	}
	v := strings.TrimSpace(string(b))
	limit, err := strconv.ParseInt(v, 10, 64)
	if err != nil || limit <= 0 {
		return "no memory limit (memory.max " + v + ")"
	}
	soft := limit / 10 * 9
	debug.SetMemoryLimit(soft)
	return fmt.Sprintf("GOMEMLIMIT %d (90%% of the cgroup's memory.max, %d)", soft, limit)
}

// cgroupMemoryMax is cgroup v2's memory limit of the process's cgroup, as
// a container sees it.
const cgroupMemoryMax = "/sys/fs/cgroup/memory.max"
