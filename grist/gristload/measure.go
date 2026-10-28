// Package gristload sizes a grist application's deployment from its own
// recorded steps (docs/grist-platform.md §8): the service times of its
// commands, queries and declared calls measured from a run's steps, a
// chain (qmeta) per hop ranking the settings, a load simulation (qload,
// grist/client's retry decision in the loop) confirming them, and the
// arithmetic rows (pool, grace, CPU, memory). Report gives every value
// with its formula, tool, margin, caveat and evidence, never a bare
// number; Write puts it in sizing.yaml beside the evidence's files.
//
// It is never in an application's binary: Main is the entry point of the
// app's sizing binary of its own, cmd/<app>-sizing (`func main() {
// gristload.Main(app) }`), which the app's `<app> sizing` runs by name
// (grist.Main), so qload, qmeta and gristsim stay out of what an operator
// deploys.
package gristload

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qmeta"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Kind is what a measured block is.
type Kind string

// The kinds of a measured block.
const (
	Command Kind = "command"
	Query   Kind = "query"
	// Call is a declared dependency's call, measured at its caller (a
	// processor's effect): the dependency's latency as the app sees it.
	Call Kind = "call"
)

// A Block is one command's, query's or declared call's measured
// service.
type Block struct {
	Name  string // the block's name ("increment-counter", "mailer.send")
	Quint string // its steps' action
	Kind  Kind
	// Service is its calls' durations, invocation to answer, as the
	// caller saw them (qmeta.Measure).
	Service qmeta.Service
	// Growth is a command's service against the log's length at its
	// commit: Slope seconds per event, a least-squares line over its
	// calls (0 with too few to fit, or a query or call).
	Growth Growth
}

// Growth is a block's service time as a line in the log's length:
// Intercept + Slope × events.
type Growth struct {
	Intercept, Slope float64 // seconds, seconds per event
	// From and To are the log lengths the calls spanned.
	From, To int64
	Calls    int
}

// At is the line's service at a log of n events (seconds).
func (g Growth) At(n int64) float64 { return g.Intercept + g.Slope*float64(n) }

// A Processor is a processor's measured load: its trigger events per
// client call, on the busiest tenant, and the declared calls its effect
// makes.
type Processor struct {
	Name string
	On   string // its trigger event type
	// PerCall is the trigger events committed per client call (all
	// tenants): the processor's arrival rate is the load's rate times it.
	PerCall float64
	// Uses are the measured blocks of the calls its effect makes (none
	// measured: its service is unknown, and the hop is not sized).
	Uses []string
	// EffectTries is how many times a failed effect is tried.
	EffectTries int
}

// Measured is what Measure finds in an app's steps.
type Measured struct {
	App    string
	Blocks map[string]*Block // by name
	// Calls is the client calls completed, all tenants; Tenants each
	// tenant's.
	Calls   int
	Tenants map[string]int
	// Hot is the busiest tenant and HotShare its share of the calls.
	Hot      string
	HotShare float64
	// InFlight is the most client calls the recording had in flight at
	// once: above 1, a call's duration includes its wait behind others,
	// and the service times are upper bounds.
	InFlight   int
	Processors []Processor
	Steps      int
}

// Order is the blocks' names, commands first, then queries, then calls,
// each sorted.
func (m *Measured) Order() []string {
	rank := map[Kind]int{Command: 0, Query: 1, Call: 2}
	names := make([]string, 0, len(m.Blocks))
	for n := range m.Blocks {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := m.Blocks[names[i]], m.Blocks[names[j]]
		if rank[a.Kind] != rank[b.Kind] {
			return rank[a.Kind] < rank[b.Kind]
		}
		return a.Name < b.Name
	})
	return names
}

// Of is the service of all blocks of a kind together (their samples
// pooled): the API's mixture of commands and queries, say.
func (m *Measured) Of(kinds ...Kind) qmeta.Service {
	var all []time.Duration
	for _, n := range m.Order() {
		if b := m.Blocks[n]; slices.Contains(kinds, b.Kind) {
			all = append(all, b.Service.Samples...)
		}
	}
	return qmeta.Measure(all)
}

// Measure reads an app's service times from the steps of a run of it
// (a real tier's step files, `<app> serve -steps`, merged; or a
// gristtest.Tier's Steps): each client call's and each declared call's
// duration, its invocation record to its answer (qobs.Invoke,
// Call.Done), by block; a command's against the log's length at its
// commit (the store's step of the same id); the tenants' shares of the
// calls; and each processor's trigger events per call. A recording
// with one call at a time gives service times; one with calls in flight
// together (InFlight above 1) gives each call's wait too.
func Measure(app *grist.App, steps []qtrace.Step) (*Measured, error) {
	cat := app.Catalog()
	kind := map[string]*Block{} // by Quint action
	m := &Measured{App: app.Name, Blocks: map[string]*Block{}, Tenants: map[string]int{}, Steps: len(steps)}
	add := func(name, quint string, k Kind) {
		b := &Block{Name: name, Quint: quint, Kind: k}
		kind[quint] = b
	}
	for _, c := range cat.Commands {
		add(c.Name, c.Quint, Command)
	}
	for _, q := range cat.Queries {
		add(q.Name, q.Quint, Query)
	}
	for _, d := range cat.Dependencies {
		for _, c := range d.Calls {
			add(d.Name+"."+c.Name, c.Quint, Call)
		}
	}
	type key struct {
		rec string
		seq uint64
	}
	invoked := map[key]time.Time{}
	for _, s := range steps {
		if s.Invocation {
			invoked[key{s.Recorder, s.Seq}] = s.Time
		}
	}
	samples := map[string][]time.Duration{}
	type point struct {
		id  string
		dur time.Duration
	}
	byID := map[string][]point{}
	head := map[string]int64{} // a command id's log length at its commit
	triggers := map[string]int{}
	type span struct{ from, to time.Time }
	var clientSpans []span
	for _, s := range steps {
		if s.Invocation {
			continue
		}
		if s.Recorder != "" && strings.HasPrefix(s.Recorder, "grist/") && s.Outcome == "ok" {
			// a commit, from the store's step log: its id's log length, its events
			if id, ok := str(s.Args["id"]); ok {
				if h, ok := integer(s.Obs["head"]); ok {
					head[id] = h
				}
			}
			for _, e := range events(s.Obs["events"]) {
				triggers[e]++
			}
		}
		b, ok := kind[s.Action]
		if !ok || s.Call == 0 {
			continue
		}
		start := s.Invoked
		if start.IsZero() {
			start = invoked[key{s.Recorder, s.Call}]
		}
		if start.IsZero() || s.Time.Before(start) {
			continue
		}
		d := s.Time.Sub(start)
		samples[b.Name] = append(samples[b.Name], d)
		if b.Kind != Call {
			m.Calls++
			if tenant, _, ok := strings.Cut(s.Actor, "/"); ok {
				m.Tenants[tenant]++
			}
			clientSpans = append(clientSpans, span{start, s.Time})
		}
		if b.Kind == Command {
			if id, ok := str(s.Args["id"]); ok {
				byID[b.Name] = append(byID[b.Name], point{id, d})
			}
		}
	}
	if m.Calls == 0 {
		return nil, fmt.Errorf("gristload: no client calls in %d steps of %s: record a run with its clients' steps (grist/client, gristtest.Tier's Clients), each call's invocation and answer", len(steps), app.Name)
	}
	for _, b := range kind {
		if len(samples[b.Name]) == 0 {
			continue
		}
		b.Service = qmeta.Measure(samples[b.Name])
		if b.Kind == Command {
			var xs, ys []float64
			for _, p := range byID[b.Name] {
				if h, ok := head[p.id]; ok {
					xs, ys = append(xs, float64(h)), append(ys, p.dur.Seconds())
				}
			}
			b.Growth = fit(xs, ys)
		}
		m.Blocks[b.Name] = b
	}
	for t, n := range m.Tenants {
		if n > m.Tenants[m.Hot] || n == m.Tenants[m.Hot] && t < m.Hot {
			m.Hot = t
		}
	}
	if m.Hot != "" {
		m.HotShare = float64(m.Tenants[m.Hot]) / float64(m.Calls)
	}
	m.InFlight = maxOverlap(clientSpans, func(s span) (time.Time, time.Time) { return s.from, s.to })
	quintOf := map[string]string{}
	for _, d := range cat.Dependencies {
		for _, c := range d.Calls {
			quintOf[c.Quint] = d.Name + "." + c.Name
		}
	}
	for _, p := range cat.Processors {
		pr := Processor{Name: p.Name, On: p.On, PerCall: float64(triggers[p.On]) / float64(m.Calls), EffectTries: p.EffectTries}
		for _, u := range p.Uses {
			if n, ok := quintOf[u]; ok && m.Blocks[n] != nil {
				pr.Uses = append(pr.Uses, n)
			}
		}
		m.Processors = append(m.Processors, pr)
	}
	return m, nil
}

// fit is the least-squares line through (xs, ys), zero with fewer than
// ten points or no spread in xs.
func fit(xs, ys []float64) Growth {
	n := float64(len(xs))
	if len(xs) < 10 {
		return Growth{Calls: len(xs)}
	}
	var sx, sy, sxx, sxy float64
	lo, hi := math.Inf(1), math.Inf(-1)
	for i := range xs {
		sx, sy, sxx, sxy = sx+xs[i], sy+ys[i], sxx+xs[i]*xs[i], sxy+xs[i]*ys[i]
		lo, hi = math.Min(lo, xs[i]), math.Max(hi, xs[i])
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return Growth{Intercept: sy / n, Calls: len(xs)}
	}
	slope := (n*sxy - sx*sy) / den
	return Growth{Intercept: (sy - slope*sx) / n, Slope: slope, From: int64(lo), To: int64(hi), Calls: len(xs)}
}

// maxOverlap is the most intervals open at once.
func maxOverlap[T any](xs []T, at func(T) (time.Time, time.Time)) int {
	type edge struct {
		t     time.Time
		delta int
	}
	var es []edge
	for _, x := range xs {
		a, b := at(x)
		es = append(es, edge{a, 1}, edge{b, -1})
	}
	sort.Slice(es, func(i, j int) bool {
		if !es[i].t.Equal(es[j].t) {
			return es[i].t.Before(es[j].t)
		}
		return es[i].delta < es[j].delta // an end before a start at one instant
	})
	n, most := 0, 0
	for _, e := range es {
		n += e.delta
		most = max(most, n)
	}
	return most
}

func str(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func integer(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	case float64:
		return int64(x), true
	case uint64:
		return int64(x), true
	}
	return 0, false
}

// events is a commit's events, its obs.events (a JSON list of names, as
// recorded).
func events(v any) []string {
	switch x := v.(type) {
	case string:
		x = strings.Trim(x, "[]")
		if x == "" {
			return nil
		}
		var out []string
		for _, e := range strings.Split(x, ",") {
			out = append(out, strings.Trim(strings.TrimSpace(e), `"`))
		}
		return out
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return x
	}
	return nil
}
