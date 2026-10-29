package gristsim

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
)

// peerGraph validates the whole topology before allocating any peer's
// services. Traversing declarations in registration order makes the run
// independent of Config.Peers' order; shared peers are visited once.
// Cycles are deliberately outside this first composition: in particular
// a call back to the root would need the root's workload API as a peer.
func peerGraph(app *grist.App, cfg Config) ([]*grist.App, error) {
	if len(cfg.Peers) > 0 && (cfg.From != nil || cfg.Cluster != nil) {
		return nil, errors.New("gristsim: Config.Peers with Config.From or Config.Cluster: a composition's rollouts and clusters are not built yet (GR4)")
	}
	given := map[*grist.App]bool{}
	names := map[string]*grist.App{app.Name: app}
	for _, p := range cfg.Peers {
		switch {
		case p == nil || p == app:
			return nil, errors.New("gristsim: Config.Peers holds nil or the App itself: give the Apps it calls (grist.NewPeer)")
		case given[p]:
			return nil, fmt.Errorf("gristsim: Config.Peers has %s twice", p.Name)
		case names[p.Name] != nil:
			return nil, fmt.Errorf("gristsim: Config.Peers has two Apps named %s: give each service a unique App name", p.Name)
		}
		if err := p.Verify(); err != nil {
			return nil, fmt.Errorf("gristsim: Config.Peers: %w", err)
		}
		given[p], names[p.Name] = true, p
	}
	state := map[*grist.App]int{} // 1 visiting, 2 visited
	var order []*grist.App
	var path []string
	var visit func(*grist.App) error
	visit = func(a *grist.App) error {
		if state[a] == 1 {
			return fmt.Errorf("gristsim: peer graph has cycle %s: this composition supports acyclic peer calls", strings.Join(append(path, a.Name), " -> "))
		}
		if state[a] == 2 {
			return nil
		}
		state[a] = 1
		path = append(path, a.Name)
		if a != app {
			order = append(order, a)
		}
		for _, d := range a.Dependencies() {
			p := d.Peer()
			if p == nil {
				continue
			}
			if p != app && !given[p] {
				if names[p.Name] != nil {
					return fmt.Errorf("gristsim: %s calls peer %s, but Config.Peers holds another App of that name: give the App declared by grist.NewPeer", a.Name, p.Name)
				}
				return fmt.Errorf("gristsim: %s calls peer %s (grist.NewPeer), which the run does not run: add its App to Config.Peers", a.Name, p.Name)
			}
			if err := visit(p); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[a] = 2
		return nil
	}
	if err := visit(app); err != nil {
		return nil, err
	}
	for _, p := range cfg.Peers {
		if state[p] == 0 {
			return nil, fmt.Errorf("gristsim: Config.Peers has %s, which %s does not call, directly or through another peer: declare it (grist.NewPeer) or leave it out", p.Name, app.Name)
		}
		// The root's nodes keep their established names (node, node2, ...).
		for i := 1; i <= cfg.Nodes; i++ {
			n := fmt.Sprintf("node%d", i)
			if i == 1 {
				n = "node"
			}
			if p.Name == n {
				return nil, fmt.Errorf("gristsim: peer %s has the name of the root's node process: rename its App so crash faults name one process", n)
			}
		}
	}
	// qhttp APIs and dependency doubles use the same rpc.<name>.*
	// namespace. Reject collisions rather than panic in qenv's registry,
	// or accidentally combine two services' fault rates and identities.
	owners := map[string]string{}
	all := append([]*grist.App{app}, order...)
	for _, a := range all {
		owners[a.Name] = "App " + a.Name
	}
	for _, a := range all {
		for _, d := range a.Dependencies() {
			if d.Peer() != nil {
				continue
			}
			owner := "dependency " + d.Name() + " of " + a.Name
			if prev := owners[d.Name()]; prev != "" {
				return nil, fmt.Errorf("gristsim: %s and %s share rpc.%s.*: rename the dependency so each simulated endpoint has its own name", prev, owner, d.Name())
			}
			owners[d.Name()] = owner
		}
	}
	return order, nil
}

// startPeers wires every API before attaching dependencies, so an App
// may call a peer allocated later and several Apps reach one shared
// store/node/API. No task runs until Run starts the simulation.
func startPeers(s *qenv.Sim, app *grist.App, cfg Config) (map[*grist.App]grist.Transport, []*grist.Relay, map[*qenv.Process]bool, error) {
	apps, err := peerGraph(app, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	type peer struct {
		app    *grist.App
		served func(context.Context) context.Context
	}
	trs := map[*grist.App]grist.Transport{}
	var relays []*grist.Relay
	procs := map[*qenv.Process]bool{}
	var peers []*peer
	for _, a := range apps {
		db := qenv.NewStore(s, a.Name, cfg.Isolation)
		st := simstore.New(db)
		relay, err := grist.NewRelay(st)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, t := range cfg.Tenants {
			if err := st.CreateTenant(s.Context(), t); err != nil {
				return nil, nil, nil, err
			}
		}
		db.CommitFaults = cfg.CommitFaults
		h, err := grist.NewHandler(a, st, grist.CallerFromHeader(CallerHeader))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("gristsim: peer %s: %w", a.Name, err)
		}
		p := &peer{app: a}
		var srv *qhttp.Server
		node := s.Process(a.Name, true, func(ctx context.Context) {
			if err := a.Start(relay.With(p.served(ctx)), st, grist.WithRelay(relay)); err != nil {
				panic(fmt.Sprintf("gristsim: peer %s could not start: %v", a.Name, err))
			}
			srv.Handle(ctx, h)
		})
		srv = qhttp.NewServer(s, a.Name, node)
		tr := qhttp.NewTransport(srv)
		tr.Timeout = DependencyTimeout
		trs[a] = client.Peer(&client.Client{App: a, URL: srv.URL(), HTTP: &http.Client{Transport: tr}, CallerHeader: CallerHeader})
		peers = append(peers, p)
		relays = append(relays, relay)
		procs[node] = true
	}
	for _, p := range peers {
		p.served, err = serveDependencies(s, p.app, nil, nil, trs)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("gristsim: peer %s: %w", p.app.Name, err)
		}
	}
	return trs, relays, procs, nil
}
