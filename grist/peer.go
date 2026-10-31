package grist

import "fmt"

// NewPeer declares peer, another grist App, a dependency of app: its
// calls are peer's commands (NewPeerCall), sent to peer's API by
// grist/client (client.Peer: in a simulation over qhttp,
// gristsim.Config.Peers; for real over HTTP), each under the effect's key
// as its command id (EffectKey: <app>/<processor>/<tenant>/<position>@<event-id>), in
// the caller's tenant, as the caller PeerCaller(app.Name). So an
// at-least-once effect's tries are one command at the peer, answered from
// its marker after the first (docs/grist-instrumentation.md §3.7): the
// peer's own steps record what it did (its commits, under
// <peer>/<tenant>), the caller's what it asked (the call's step, under
// <app>/<tenant>), and gristtest.Linked relates them by key. The
// dependency is named after peer; no site records a peer's call (its
// commit is the site), so a peer call has no Handlers and no served
// records in the model.
func NewPeer(app, peer *App) *Dependency {
	if peer == nil {
		app.problems = append(app.problems, "NewPeer: a nil peer: give the peer's App (its package's App)")
		return &Dependency{app: app, key: &depKey{name: "a nil peer"}}
	}
	if peer == app {
		app.problems = append(app.problems, fmt.Sprintf("NewPeer: app %s declared its own peer: an App's effects call its own commands by appending events (a processor's Emit), not over its API", app.Name))
	}
	d := NewDependency(app, peer.Name)
	d.peer = peer
	return d
}

// Peer is the App whose commands d's calls are (NewPeer), or nil for a
// dependency that is no peer.
func (d *Dependency) Peer() *App { return d.peer }

// PeerCaller is the caller a peer's commands see for app's calls
// (service:<app>): what the peer's policy admits (Authorize(Callers(
// PeerCaller("orders")))), GR4's service identity.
func PeerCaller(app string) string { return "service:" + app }

// NewPeerCall declares command cmd of d's peer (NewPeer) a call of d: its
// request cmd's arguments, its response none (the command's receipt is
// the peer's: positions in its log), Keyed (the peer's marker applies
// each command id once), its refusals cmd's (Refuses). Any other answer
// (a conflict, a denial, a key reused, no answer) leaves the call's
// outcome unknown to the caller: the peer's own steps say what happened.
func NewPeerCall[C any](d *Dependency, cmd *Command[C]) *Call[C, struct{}] {
	a := d.app
	opts := []Option{Keyed()}
	switch {
	case d.peer == nil:
		a.problems = append(a.problems, fmt.Sprintf("NewPeerCall: dependency %s is no peer: declare it with grist.NewPeer, or its calls with grist.NewCall", d.name))
	case cmd == nil || cmd.app != d.peer:
		a.problems = append(a.problems, fmt.Sprintf("NewPeerCall: a command of another App than %s's peer %s: give one of %s's commands", a.Name, d.name, d.name))
	case cmd.info.opts.internal:
		a.problems = append(a.problems, fmt.Sprintf("NewPeerCall: command %s of %s is Internal, which its API serves to no one: give it an Authorize admitting grist.PeerCaller(%q)", cmd.info.name, d.name, a.Name))
	}
	name := ""
	if cmd != nil {
		name = cmd.info.name
		if r := cmd.info.opts.refuses; len(r) > 0 {
			rs := make([]error, len(r))
			for i, n := range r {
				rs[i] = Rejection(n)
			}
			opts = append(opts, Refuses(rs...))
		}
	}
	c := NewCall[C, struct{}](d, name, opts...)
	c.info.peer = true
	return c
}

// peerCalls reports, for Main and the harnesses, a problem with serving
// d as a dependency of handlers (Handlers, ServeCalls, a double): a
// peer's calls are its commands, served by its own API.
func (d *Dependency) peerCalls(what string) error {
	if d.peer == nil {
		return nil
	}
	return fmt.Errorf("grist: %s: dependency %s is a peer (grist.NewPeer), whose calls are its commands, served by its own API: attach client.Peer(...) for it (Dependency.With; under Main, Dependencies)", what, d.name)
}
