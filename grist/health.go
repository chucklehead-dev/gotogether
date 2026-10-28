package grist

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/store"
)

// LiveBound is how long a poller may work on one trigger of its batch
// before /livez fails: far longer than any handler or effect takes (an
// effect's calls time out in CallTimeout, and are tried EffectTries
// times), so only a deadlock or a stuck goroutine reaches it. The clock
// restarts at each trigger and after each backoff from a store error: a
// batch of slow effects, or a commit retried through a database outage,
// is not stuck.
const LiveBound = 5 * time.Minute

// liveBound is LiveBound, shortened by tests (export_test.go); read-only
// otherwise.
var liveBound = LiveBound

// health is Main's side port: /livez, /readyz and /metrics.
type health struct {
	app      *App
	st       atomic.Pointer[storeRef]
	relay    atomic.Pointer[Relay]
	node     atomic.Pointer[node]
	ready    atomic.Bool  // the store answered and the node started
	draining atomic.Bool  // SIGTERM came
	requests atomic.Int64 // the API's requests in flight
}

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
		next.ServeHTTP(w, r)
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
// processor's checkpoint) and the age of the oldest trigger not yet
// handled. A store that does not answer leaves its gauges out, and
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
			r := row{p: p.name, t: t, lag: int64(head - cp)}
			if len(evs) > 0 {
				r.ageS = max(0, float64(now.UnixMilli()-evs[0].Time)/1000)
			}
			rows = append(rows, r)
		}
	}
	if len(rows) > 0 {
		gauge("grist_processor_lag", "Positions between the tenant's head and the processor's checkpoint.")
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
	return b.String()
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
