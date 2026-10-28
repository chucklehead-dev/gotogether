// Package qhttp runs net/http code inside a qenv simulation: an
// unmodified http.Handler behind a Server, and an unmodified http.Client
// whose Transport sends its requests across the simulated network. Every
// fault of the network is a qenv choice, so seeds explore them and Hegel
// shrinks them:
//   - "http.<name>.conn": "refused" (with Transport.Refuse), the
//     connection refused before anything is sent: a retry is safe;
//   - "rpc.<name>.request": "drop" (never processed) or "duplicate"
//     (processed twice, as a retransmission can);
//   - "rpc.<name>.delay": a request that arrives late (qenv.Service);
//   - "rpc.<name>.response": "lost" after the handler ran;
//   - a crash of the server's process, which kills handlers mid-flight
//     (by default also after a handler's effect and before its response:
//     the Service's crash window) and refuses connections while it is
//     down.
//
// The client sees each as a real one would: a refused connection is a
// *net.OpError wrapping ECONNREFUSED (only before anything was sent), a
// request that reaches a restarted server before its Handle one wrapping
// ECONNRESET (outcome unknown), a missing response a *TimeoutError
// after Transport.Timeout of virtual time (the *url.Error's Timeout() is
// true, and the outcome is unknown), a full queue (Service.Capacity) a 503.
//
// Requests and responses travel as the bytes a connection would carry
// (http.Request.Write and http.ReadRequest; the response written as
// net/http's server writes it, read with http.ReadResponse), so the handler
// and the client share no memory, and headers, Content-Length, chunking and
// bodies are what a wire gives. What is not simulated: TLS (an https URL
// reaches the same server, r.TLS is nil), HTTP/2, trailers, 100-continue,
// interim (1xx) responses, Hijack, and connection reuse (every request is
// on a connection of its own).
//
// # Time and goroutines
//
// http.Client.Do calls RoundTrip on the caller's goroutine (net/http's
// send), so a client in a simulation task stays in it. Client.Timeout,
// though, is enforced by net/http with a real timer: for a RoundTripper
// other than its own Transport, setRequestCancel starts a goroutine that
// waits on time.NewTimer. It measures real time, which a simulated run
// barely spends, so it approximately never fires. Its deadline does reach
// the request's context, and the Transport converts a context's deadline
// to virtual time (Sim.ToVirtual) once, the first time the simulation sees
// it, its remaining time rounded up to a grid of round timeouts (150 ms,
// 1 s; 700 ms becomes 800 ms) so the test process's own jitter does not
// reach the run, and uses it up in virtual time from then (WithTimeout
// gives the Transport a timeout exactly): the deadline that applies
// to a request is the virtual one, Client.Timeout's included. The context
// itself still ends on its wall timer: ctx.Err() and ctx.Done() of a
// service's own deadlines are wall time in a simulation, and approximately
// never fire in a fast run. A loop that retries until ctx.Err() spins at
// one virtual instant once the deadline has passed; stop it on the
// request's error (errors.Is(err, context.DeadlineExceeded), as a real
// one would see) or bound its attempts.
//
// A handler runs as a qenv task in the server's process with the task's
// context as r.Context(), so qenv's seams (Sleep, Store, Service calls) and
// step recording work inside it. A handler that starts goroutines (go
// statements, http.TimeoutHandler, which runs the handler on a goroutine of
// its own and times it with a real timer) escapes the simulation: weave it
// (qenv/weave) or run the program under gosim (qgosim).
package qhttp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A Server serves an http.Handler in a process of a simulation. Its
// handler is installed by Handle, from the process's start function, at
// every incarnation: before that, and while the process is down, a
// connection to it is refused, as a closed port refuses one.
type Server struct {
	Name string
	Proc *qenv.Process
	// Service carries the requests: set its NoCrashWindow or Capacity
	// (a queue whose overflow answers 503) before the run, read its Calls.
	// A request is the caller's address, a NUL, and the request's bytes as
	// written on the wire; a response is the response's bytes.
	Service *qenv.Service[[]byte, []byte]

	sim     *qenv.Sim
	conn    qenv.FaultKind
	handler http.Handler
	inc     int // the incarnation of Proc whose start installed handler
}

// errNotListening is a request arriving (late) at a server whose process
// restarted and has not called Handle again: no one listens on the port.
// The request was sent, and a copy of it may have been processed, so the
// client sees a reset (outcome unknown), never a refusal.
var errNotListening = errors.New("qhttp: no handler installed")

// NewServer creates a server named name (its host in URLs) in process p of
// s (nil: a process of its own, which never crashes; call Handle from the
// test then).
func NewServer(s *qenv.Sim, name string, p *qenv.Process) *Server {
	srv := &Server{Name: name, sim: s}
	srv.Service = qenv.NewService(s, name, p, srv.serve)
	srv.Proc = srv.Service.Proc
	srv.conn = qenv.DeclareFaults("http."+name+".conn", "refused").At(srv.Proc)
	return srv
}

// Handle installs h as the server's handler for the process's current
// incarnation, and returns (unlike http.Serve, it does not block: the
// start function may go on or return). Call it from the process's start
// function, which runs again at every restart; called from outside any
// task, it installs h for the current incarnation only.
func (srv *Server) Handle(ctx context.Context, h http.Handler) {
	if p := qenv.ProcessOf(ctx); p != nil && p != srv.Proc {
		panic(fmt.Sprintf("qhttp: %s.Handle called from process %s: call it from %s's start function (Sim.Process), "+
			"so every incarnation installs its handler", srv.Name, p.Name, srv.Proc.Name))
	}
	srv.handler, srv.inc = h, srv.Proc.Incarnation
}

// URL is the server's base URL, "http://<name>".
func (srv *Server) URL() string { return "http://" + srv.Name }

// notListening says why the server does not listen now.
func (srv *Server) notListening() string {
	switch {
	case srv.Proc.Down():
		return "its process " + srv.Proc.Name + " was down"
	case srv.handler == nil:
		return "its process had not called Handle yet: start the server's process before its clients, or have them retry a refused connection"
	}
	return "its process restarted and had not called Handle again"
}

// listening reports whether a connection to the server is accepted now.
func (srv *Server) listening() bool {
	return srv.handler != nil && !srv.Proc.Down() && srv.inc == srv.Proc.Incarnation
}

// serve is the Service's handler: one request, read from its bytes, served
// by the handler, its response written as bytes.
func (srv *Server) serve(ctx context.Context, in []byte) ([]byte, error) {
	if !srv.listening() {
		return nil, errNotListening
	}
	from, wire, _ := bytes.Cut(in, []byte{0})
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(wire)))
	if err != nil {
		// cannot happen for a request the Transport wrote; a server
		// answers a malformed one so
		w := newResponseWriter(&http.Request{Method: http.MethodGet})
		http.Error(w, "400 Bad Request: "+err.Error(), http.StatusBadRequest)
		return w.wire(ctx), nil
	}
	req.RemoteAddr = string(from)
	req = req.WithContext(ctx)
	w := newResponseWriter(req)
	// a panic is not recovered: net/http's server would recover it and
	// close the connection, but here it fails the run (Sim.Run re-raises
	// it), naming the handler's bug instead of hiding it in a timeout
	srv.handler.ServeHTTP(w, req)
	return w.wire(ctx), nil
}
