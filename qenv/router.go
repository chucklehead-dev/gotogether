package qenv

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// An Endpoint is one incarnation of a backend process, as a router's
// view names it: a pod's address, which its restart replaces.
type Endpoint struct {
	Process     *Process
	Incarnation int
}

// Gone reports whether the endpoint's incarnation has ended (the process
// is down or restarted since): a request sent to it is refused, as a
// connection to a dead pod's address is.
func (e Endpoint) Gone() bool { return e.Process.down || e.Process.Incarnation != e.Incarnation }

func (e Endpoint) String() string { return fmt.Sprintf("%s#%d", e.Process.Name, e.Incarnation) }

// ErrNoEndpoint is Router.Pick's error when its view has no endpoint:
// what a load balancer answers with a 503 and no backend behind it.
var ErrNoEndpoint = errors.New("qenv: the router has no ready endpoint in its view")

// A Router is a load balancer in front of backend processes (kube-proxy,
// a Gateway, a mesh's sidecar): each request goes to one endpoint of its
// view, a schedule choice "route.<name>" among them (option 0 the first
// in the backends' order). A backend is ready while it is up and not
// terminating (Process.Terminate); its view follows each change of a
// backend's readiness, at once (nominal), or Lag later under the fault
// "route.<name>.lag", option "stale": a terminating backend still
// chosen, a restarted one not yet. Each backend's changes reach the view
// in order (a later one never before an earlier one). With Lag zero the
// view is never stale, and the fault kind is not declared.
type Router struct {
	Name     string
	sim      *Sim
	lag      time.Duration
	lagKind  FaultKind
	backends []*Process
	slot     map[*Process]int
	view     []routeEntry // per backend, in its order: the endpoint the view has
	want     []routeEntry // per backend: what its readiness says
	queue    [][]routeUpdate
}

// routeEntry is a backend's endpoint in a view, or none.
type routeEntry struct {
	inc     int
	present bool
}

// routeUpdate is a change of a backend's endpoint that reaches the view at
// at.
type routeUpdate struct {
	at time.Time
	e  routeEntry
}

// NewRouter registers a router named name in front of backends, its view
// lagging by lag under its fault. Two routers of one name in a
// simulation panic, as two doubles of one kind do.
func NewRouter(s *Sim, name string, lag time.Duration, backends ...*Process) *Router {
	s.claim("route."+name, "Router", name)
	r := &Router{Name: name, sim: s, lag: lag, backends: append([]*Process(nil), backends...), slot: map[*Process]int{}}
	if lag > 0 {
		r.lagKind = DeclareFaults("route."+name+".lag", "stale")
	}
	for i, p := range backends {
		if _, ok := r.slot[p]; ok {
			panic(fmt.Sprintf("qenv: router %s: backend %s given twice", name, p.Name))
		}
		r.slot[p] = i
		e := readiness(p)
		r.view, r.want = append(r.view, e), append(r.want, e)
	}
	r.queue = make([][]routeUpdate, len(backends))
	s.onChange(r.changed)
	return r
}

// readiness is p's endpoint as its readiness says: present while it is
// up and not terminating.
func readiness(p *Process) routeEntry {
	return routeEntry{inc: p.Incarnation, present: !p.down && !p.terminating}
}

// changed follows a backend's change of readiness into the view: at once,
// or a lag later.
func (r *Router) changed(p *Process) {
	i, ok := r.slot[p]
	if !ok {
		return
	}
	e := readiness(p)
	if !e.present {
		e.inc = r.want[i].inc // no endpoint: which incarnation it was is moot
	}
	if e == r.want[i] {
		return
	}
	r.want[i] = e
	s := r.sim
	at := s.Now()
	if r.lag > 0 && s.FaultOf(r.lagKind) == "stale" {
		at = at.Add(r.lag)
	}
	r.queue[i] = append(r.queue[i], routeUpdate{at: at, e: e}) // applied in order: never before an earlier change
	r.apply()
}

// apply moves into the view every change whose time has come, each
// backend's in order: one waits for the changes before it.
func (r *Router) apply() {
	now := r.sim.Now()
	for i, q := range r.queue {
		k := 0
		for k < len(q) && !q[k].at.After(now) {
			r.view[i] = q[k].e
			k++
		}
		r.queue[i] = q[k:]
	}
}

// View is the router's view now: the endpoints a request may go to, in
// the backends' order.
func (r *Router) View() []Endpoint {
	r.apply()
	var out []Endpoint
	for i, e := range r.view {
		if e.present {
			out = append(out, Endpoint{Process: r.backends[i], Incarnation: e.inc})
		}
	}
	return out
}

// Pick chooses the endpoint of a request: one of the view's, by the
// schedule choice route.<name>, or ErrNoEndpoint when the view is empty.
// The endpoint may be Gone (the view is stale): the caller's request to
// it is refused.
func (r *Router) Pick(ctx context.Context) (Endpoint, error) {
	v := r.View()
	if len(v) == 0 {
		return Endpoint{}, ErrNoEndpoint
	}
	return v[r.sim.Choose("route."+r.Name, len(v))], nil
}
