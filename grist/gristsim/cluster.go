package gristsim

import (
	"context"
	"fmt"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/drainhook"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// A Cluster runs the nodes as an orchestrator does (docs/grist-platform.md
// §4): every node serves the API, behind a router whose view of them
// lags, and each may be terminated gracefully, draining as grist.Main
// does (the same schedule: grist.Drain). Its faults and choices:
//   - nemesis.terminate.<node> (family terminate, qenv.Sim.Terminations):
//     the node is sent SIGTERM; it stops being ready at once, keeps
//     serving for its drain's Delay, shuts its API down (a request then is
//     refused: the client retries it), quiets, stops its work, flushes its
//     relay and exits, to be restarted as its next incarnation; one still
//     draining at Grace is killed (a crash);
//   - route.api, the router's choice of a node's endpoint, and
//     route.api.lag (option stale): the router learns of a change of a
//     node's readiness EndpointLag late, so a terminating node is still
//     sent requests, and a restarted one not yet;
//   - gristsim.rollout, when Rolling: the point where the rolling
//     restart begins.
//
// Each node's API is a qenv.Service of its own, api.<node> (its faults
// rpc.api.<node>.request, .delay, .response). A client's try that no
// server takes (the router's endpoint gone, none in its view, a server
// shut down) is retried and counted on its call's step:
// gristtest.NoClientErrors judges them.
type Cluster struct {
	// Grace is each node's termination grace (default DefaultGrace, what
	// the generated manifests give): its drain is budgeted into it, and a
	// node still draining at it is killed.
	Grace time.Duration
	// Drain is the node's drain schedule, the app's own (what its binary
	// gives grist.WithDrain): its zero fields are grist's defaults on an
	// orchestrator (grist.DrainDelay, then 10 s each), its Grace Grace
	// unless set.
	Drain grist.Drain
	// EndpointLag is how late the router may learn of a node's change of
	// readiness (route.api.lag's stale option); 0: never late.
	EndpointLag time.Duration
	// Disruptions, if set, lets at most this many nodes terminate at once
	// (a PodDisruptionBudget's maxUnavailable); crashes are not
	// budgeted.
	Disruptions int
	// Rolling restarts every node in turn while the clients call (a
	// rollout's restart: each node terminated once the previous one is
	// back and in the router's view), from a point gristsim.rollout chooses (a quarter of Pace
	// apart, over the clients' phase).
	Rolling bool
	// Pace is how long each client waits between its operations (default
	// 1 s), so a run's calls span its drains.
	Pace time.Duration
}

// DefaultGrace is a Cluster's default Grace: the 45 s the generated
// manifests give a pod (docs/grist-platform.md §10.2), more than the 35 s
// the drain's defaults need.
const DefaultGrace = 45 * time.Second

// cluster is a run's Cluster: its nodes, their APIs and the router.
type cluster struct {
	c      Cluster
	drain  grist.Drain
	nodes  []*qenv.Process
	apis   map[*qenv.Process]*qenv.Service[request, response]
	state  map[qenv.Endpoint]*apiState // each incarnation's API
	router *qenv.Router
}

// apiState is a node incarnation's API: closed once its drain shuts it
// down (its listener closed: a request sent after is refused), and the
// requests sent to it before and not yet answered, which its shutdown
// waits for (connections a server's graceful shutdown lets finish).
type apiState struct {
	closed   bool
	inflight int
	idle     qenv.Signal
}

// startCluster starts cfg.Nodes nodes under cfg.Cluster: each a crashable
// process starting the App, serving its API and draining at its
// termination.
func startCluster(s *qenv.Sim, app *grist.App, st grist.Store, relay *grist.Relay, deps func(context.Context) context.Context, cfg Config) *cluster {
	c := *cfg.Cluster
	if c.Grace <= 0 {
		c.Grace = DefaultGrace
	}
	if c.Pace <= 0 {
		c.Pace = time.Second
	}
	cl := &cluster{c: c, drain: c.Drain, apis: map[*qenv.Process]*qenv.Service[request, response]{}, state: map[qenv.Endpoint]*apiState{}}
	if cl.drain.Grace == 0 {
		cl.drain.Grace = c.Grace
	}
	start := func(ctx context.Context) {
		p := qenv.ProcessOf(ctx)
		as := cl.api(p)
		// the node's work: it stops after the drain's settle (stop), not at
		// the signal; its effects in flight finish
		nodeCtx, stop := context.WithCancel(ctx)
		var drainer drainhook.Node
		hook := func(n drainhook.Node) { drainer = n }
		if err := app.Start(drainhook.With(deps(nodeCtx), hook), st, grist.WithRelay(relay)); err != nil {
			panic(fmt.Sprintf("gristsim: %s could not start: %v", p.Name, err))
		}
		qenv.Go(ctx, "drain", func(ctx context.Context) {
			term := qenv.Terminating(ctx)
			qenv.WaitIdle(ctx, func() bool { return term.Err() != nil })
			if err := drainer.Drain(ctx, cl.drain, apiServer{as}, stop); err != nil && s.Log != nil {
				s.Log(fmt.Sprintf("%v gristsim: %s's drain: %v", s.Elapsed(), p.ID(), err))
			}
			p.Exit()
		})
	}
	for i := 1; i <= cfg.Nodes; i++ {
		name := "node"
		if i > 1 {
			name = fmt.Sprintf("node%d", i)
		}
		p := s.Process(name, true, start)
		cl.nodes = append(cl.nodes, p)
		cl.apis[p] = qenv.NewService(s, "api."+name, p, func(ctx context.Context, req request) (response, error) {
			return serve(deps(ctx), app, st, req), nil
		})
	}
	s.Terminations(qenv.Termination{Grace: c.Grace, Budget: c.Disruptions}, cl.nodes...)
	cl.router = qenv.NewRouter(s, "api", c.EndpointLag, cl.nodes...)
	if c.Rolling {
		s.Spawn("rollout", func(ctx context.Context) {
			// a quarter of the pace apart: not only where the clients send
			qenv.Sleep(ctx, time.Duration(s.Choose("gristsim.rollout", 4*cfg.Ops))*c.Pace/4)
			for _, p := range cl.nodes {
				qenv.Wait(ctx, func() bool { return cl.allUp() })
				inc := p.Incarnation
				p.Terminate()
				qenv.Wait(ctx, func() bool { return p.Incarnation > inc && !p.Down() })
				// and routed to again before the next goes (a Deployment's
				// minReadySeconds): with two nodes, the next one's removal
				// before this one's addition reached the view left it empty
				for !cl.routed(p) {
					qenv.Sleep(ctx, RetryDelay)
				}
			}
		})
	}
	return cl
}

// api is the API state of p's current incarnation.
func (cl *cluster) api(p *qenv.Process) *apiState {
	return cl.apiOf(qenv.Endpoint{Process: p, Incarnation: p.Incarnation})
}

// apiOf is the API state of endpoint e's incarnation.
func (cl *cluster) apiOf(e qenv.Endpoint) *apiState {
	as := cl.state[e]
	if as == nil {
		as = &apiState{}
		cl.state[e] = as
	}
	return as
}

// routed reports whether the router's view has p's current incarnation.
func (cl *cluster) routed(p *qenv.Process) bool {
	for _, e := range cl.router.View() {
		if e.Process == p && e.Incarnation == p.Incarnation {
			return true
		}
	}
	return false
}

// allUp reports whether every node is up and none terminating.
func (cl *cluster) allUp() bool {
	for _, p := range cl.nodes {
		if p.Down() || p.Terminating() {
			return false
		}
	}
	return true
}

// send sends req until a node answers it (at most MaxAttempts tries, the
// same id each time), each through the router: a try no server takes
// (the endpoint gone, none in the view, the node's API shut down when it
// was sent) is refused, counted and retried, as a lost or late answer is
// retried. One sent before the shutdown is served: the shutdown waits
// for it.
func (cl *cluster) send(ctx context.Context, req request) response {
	refused := 0
	for try := 1; try <= MaxAttempts; try++ {
		ep, err := cl.router.Pick(ctx)
		if err != nil || ep.Gone() || cl.apiOf(ep).closed {
			refused++ // no endpoint, or none listening at its address
		} else {
			as := cl.apiOf(ep)
			as.inflight++
			resp, err := cl.apis[ep.Process].Call(ctx, req, CallTimeout)
			as.inflight--
			as.idle.Notify()
			if err == nil && !resp.Retry {
				resp.refused = refused
				return resp
			}
		}
		qenv.Sleep(ctx, RetryDelay)
	}
	return response{Err: "unanswered", refused: refused}
}

// apiServer is a node incarnation's API as its drain's server.
type apiServer struct{ as *apiState }

func (a apiServer) Keep(ctx context.Context, until time.Time) error {
	if d := until.Sub(qenv.Now(ctx)); d > 0 {
		qenv.Sleep(ctx, d)
	}
	return nil
}

func (a apiServer) Shutdown(ctx context.Context, until time.Time) error {
	a.as.closed = true
	for a.as.inflight > 0 {
		left := until.Sub(qenv.Now(ctx))
		if left <= 0 {
			return fmt.Errorf("%d requests in flight", a.as.inflight)
		}
		a.as.idle.Wait(ctx, left)
	}
	return nil
}
