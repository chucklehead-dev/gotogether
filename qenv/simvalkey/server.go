// Package simvalkey runs valkey-go commands and Lua scripts in qenv.
// It models one primary and one asynchronous replica at the client seam;
// it does not run a socket server or Valkey's cluster protocol.
package simvalkey

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"weak"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/simvalkey/internal/engine"
)

// ErrUnavailable refuses a call before its command executes.
var ErrUnavailable = errors.New("simvalkey: server unavailable")

// ErrUnknownOutcome means the caller cannot know whether a dispatched command applied.
var ErrUnknownOutcome = errors.New("simvalkey: command outcome unknown")

// Server is one primary and an explicitly represented asynchronous replica.
// TIME and expiry read Sim.Now, independently of calling processes' clocks.
// Its process may be partitioned or crashed with qenv's ordinary controls.
type Server struct {
	// Proc is the primary's simulated process and network endpoint.
	Proc                                     *qenv.Process
	sim                                      *qenv.Sim
	name                                     string
	db, replica                              *engine.DB
	revision, dataRevision, replicaRevision  uint64
	request, response, replication, failover qenv.FaultKind
}

var registry = struct {
	sync.Mutex
	names map[weak.Pointer[qenv.Sim]]map[string]bool
}{names: map[weak.Pointer[qenv.Sim]]map[string]bool{}}

// New registers a uniquely named server. It starts empty; nominal writes
// replicate before their response, while the replication lag fault leaves
// them vulnerable to Failover. All methods run within s's cooperative tasks.
func New(s *qenv.Sim, name string) *Server {
	if s == nil || name == "" {
		panic("simvalkey: New needs a simulation and a nonempty name")
	}
	key := weak.Make(s)
	registry.Lock()
	names := registry.names[key]
	if names == nil {
		names = map[string]bool{}
		registry.names[key] = names
		runtime.AddCleanup(s, func(k weak.Pointer[qenv.Sim]) { registry.Lock(); delete(registry.names, k); registry.Unlock() }, key)
	}
	if names[name] {
		registry.Unlock()
		panic("simvalkey: duplicate server name " + name)
	}
	names[name] = true
	registry.Unlock()
	v := &Server{sim: s, name: name, db: engine.New(), replica: engine.New()}
	v.Proc = s.Process("valkey."+name, false, func(context.Context) {})
	v.request = qenv.DeclareFaults("valkey."+name+".request", "outage", "hang").At(v.Proc)
	v.response = qenv.DeclareFaults("valkey."+name+".response", "lost").At(v.Proc)
	v.replication = qenv.DeclareFaults("valkey."+name+".replication", "lag").At(v.Proc)
	v.failover = qenv.DeclareFaults("valkey."+name+".failover", "lose").At(v.Proc)
	s.OnCrash(func(p *qenv.Process) {
		if p == v.Proc {
			v.db = v.replica.Clone()
			v.db.ClearScripts()
			v.dataRevision = v.replicaRevision
			v.check()

		}
	})
	return v
}

// Client returns an independent logical connection implementing valkey.Client.
func (s *Server) Client() *Client { return newClient(s) }

// Replicate copies the primary's data and absolute expiration deadlines to
// its one replica. WAIT acknowledges only data actually copied here.
func (s *Server) Replicate() {
	s.replica = s.db.Clone()
	s.replicaRevision = s.dataRevision
	s.check()

}

// Failover loses the unreplicated write suffix, clears the script cache and
// changes the server incarnation. It never rewinds the server clock.
func (s *Server) Failover() {
	if !s.Proc.Down() {
		s.Proc.Crash()
	}
	s.Proc.Restart()
}

func (s *Server) check() {
	if s.sim.Check {
		if err := s.db.Check(s.sim.Now()); err != nil {
			panic(fmt.Sprintf("simvalkey: primary contract: %v", err))
		}
		if err := s.replica.Check(s.sim.Now()); err != nil {
			panic(fmt.Sprintf("simvalkey: replica contract: %v", err))
		}
		if s.replicaRevision == s.dataRevision && !s.db.SameData(s.replica, s.sim.Now()) {
			panic("simvalkey: acknowledged replica differs from primary")
		}
		if s.replicaRevision > s.dataRevision || s.dataRevision > s.revision {
			panic("simvalkey: replication revision contract")
		}
	}
}
func (s *Server) advanceReplica() {
	if s.replicaRevision < s.dataRevision && s.sim.FaultOf(s.replication) != "lag" {
		s.Replicate()
	}
}
