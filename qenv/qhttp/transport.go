package qhttp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// DefaultTimeout is how long a Transport waits for a response when its
// Timeout is unset.
const DefaultTimeout = 10 * time.Second

// A Transport is an http.RoundTripper to the Servers of a simulation,
// routed by the request URL's host (a Server's name). Use it as an
// http.Client's Transport, and send from simulation tasks.
type Transport struct {
	// Timeout is how long a request waits for its response, in virtual
	// time (0: DefaultTimeout). A request context's deadline (Client.Timeout's
	// too) shortens it: converted to virtual time the first time the
	// simulation sees it, as if it had been set then, rounded up to a grid
	// of round timeouts, and used up in virtual time from there (see
	// virtualDeadline; WithTimeout makes it exact). A request sent after
	// it passed fails at once with a TimeoutError.
	Timeout time.Duration
	// Refuse lets the network refuse a connection to a server that
	// listens: the fault kind "http.<name>.conn", option "refused".
	// Nothing was sent, so a retry is safe; off, a connection is refused
	// only when the server is down or has not called Handle.
	Refuse bool

	servers map[string]*Server
}

// NewTransport returns a transport to servers.
func NewTransport(servers ...*Server) *Transport {
	t := &Transport{servers: map[string]*Server{}}
	for _, s := range servers {
		t.servers[s.Name] = s
	}
	return t
}

// A TimeoutError is a request whose response did not arrive in time: the
// server may or may not have processed it. It is a net.Error whose
// Timeout is true (so is the *url.Error's an http.Client returns), and it
// is context.DeadlineExceeded for errors.Is.
type TimeoutError struct {
	Host string
	// After is how long the request waited, in virtual time.
	After time.Duration
	// Deadline is set when the request context's deadline, not the
	// Transport's Timeout, bounded the wait.
	Deadline bool
}

func (e *TimeoutError) Error() string {
	by := "the transport's timeout"
	if e.Deadline {
		by = "the request context's deadline"
	}
	return fmt.Sprintf("qhttp: no response from %s within %v of virtual time (%s; outcome unknown)", e.Host, e.After, by)
}

// Timeout implements net.Error.
func (e *TimeoutError) Timeout() bool { return true }

// Temporary implements net.Error.
func (e *TimeoutError) Temporary() bool { return true }

// Is makes a TimeoutError context.DeadlineExceeded, as a real client's
// timeout is.
func (e *TimeoutError) Is(target error) bool { return target == context.DeadlineExceeded }

// addr is a server's address in a *net.OpError.
type addr string

func (a addr) Network() string { return "tcp" }
func (a addr) String() string  { return string(a) }

// refused is a refused connection, as net's dialer reports it: nothing
// was sent.
func refused(host string) error {
	return &net.OpError{Op: "dial", Net: "tcp", Addr: addr(host), Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
}

// reset is a request sent on a connection the server dropped: as net
// reports it, and the outcome is unknown.
func reset(host string) error {
	return &net.OpError{Op: "read", Net: "tcp", Addr: addr(host), Err: os.NewSyscallError("read", syscall.ECONNRESET)}
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	host := req.URL.Host
	srv := t.servers[host]
	if srv == nil {
		srv = t.servers[req.URL.Hostname()]
	}
	if srv == nil {
		closeBody(req)
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: req.URL.Hostname(), IsNotFound: true}}
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		closeBody(req)
		return nil, fmt.Errorf("qhttp: unsupported protocol scheme %q for %s: use %s", req.URL.Scheme, host, srv.URL())
	}
	s := srv.sim
	if qenv.From(ctx) != s {
		closeBody(req)
		return nil, fmt.Errorf("qhttp: %s %s sent outside a task of %s's simulation: send it from a task (Sim.Spawn, Process.Spawn, qenv.Go), "+
			"where the network and virtual time are", req.Method, req.URL, srv.Name)
	}
	dl, hasDeadline := ctx.Deadline()
	// a deadline the wall clock passed means nothing here (the virtual one
	// below applies): a test process stalled between setting it and
	// sending made the run depend on the stall
	if err := ctx.Err(); err != nil && !(hasDeadline && errors.Is(err, context.DeadlineExceeded)) {
		closeBody(req)
		return nil, err
	}
	timeout, byDeadline := t.Timeout, false
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if hasDeadline {
		left := virtualDeadline(s, dl).Sub(s.Now())
		if left <= 0 { // passed in virtual time: nothing is sent, as for a done context
			closeBody(req)
			return nil, &TimeoutError{Host: host, Deadline: true}
		}
		if left < timeout {
			timeout, byDeadline = left, true
		}
	}
	wire, err := writeRequest(req)
	if err != nil {
		return nil, err
	}
	qenv.Yield(ctx) // the dial: a scheduling point, where the server may crash
	if !srv.listening() {
		// said to the run (hooks.Notes), whose report would otherwise show
		// a client that never got a step recorded as a deadlock
		hooks.NotesOf(ctx).Note("a request to "+srv.Name+" refused, nothing listening", fmt.Sprintf("%s %s at %v: %s", req.Method, req.URL.Path, s.Now().Sub(qenv.Epoch), srv.notListening()))
		return nil, refused(host)
	}
	if t.Refuse && s.FaultOf(srv.conn) == "refused" {
		return nil, refused(host)
	}
	from := "client:0"
	if p := qenv.ProcessOf(ctx); p != nil {
		from = p.Name + ":0"
	}
	in := append(append([]byte(from), 0), wire...)
	resp, err := srv.Service.Call(ctx, in, timeout)
	switch {
	case errors.Is(err, qenv.ErrTimeout):
		return nil, &TimeoutError{Host: host, After: timeout, Deadline: byDeadline}
	case errors.Is(err, qenv.ErrOverloaded):
		w := newResponseWriter(req)
		w.WriteHeader(http.StatusServiceUnavailable)
		resp = w.wire(ctx)
	case errors.Is(err, errNotListening):
		// sent, and it found no one listening: a reset, not a refusal,
		// for another copy of it may have been processed
		return nil, reset(host)
	case err != nil:
		return nil, err
	}
	return http.ReadResponse(bufio.NewReader(bytes.NewReader(resp)), req)
}

// writeRequest is the request's bytes on the wire. Its body is read here,
// whole, and closed (a RoundTripper closes it, even on errors). A body of
// unknown length is sent chunked, or, for a method that usually lacks a
// body and an empty one, not at all: what net/http decides by reading a
// byte of it on a goroutine raced against a real 200 ms timer
// (transferWriter.probeRequestBody), here decided without either.
func writeRequest(req *http.Request) ([]byte, error) {
	r := *req // RoundTrip must not modify the request
	if req.Body != nil && req.Body != http.NoBody {
		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("qhttp: reading the body of %s %s: %w", req.Method, req.URL, err)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if req.ContentLength <= 0 && len(req.TransferEncoding) == 0 {
			if len(body) == 0 && lacksBody(req.Method) {
				r.Body, r.ContentLength = nil, 0
			} else {
				r.TransferEncoding, r.ContentLength = []string{"chunked"}, -1
			}
		}
	}
	var b bytes.Buffer
	if err := r.Write(&b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// lacksBody is net/http's requestMethodUsuallyLacksBody.
func lacksBody(method string) bool {
	switch method {
	case "GET", "HEAD", "DELETE", "OPTIONS", "PROPFIND", "SEARCH":
		return true
	}
	return false
}

func closeBody(req *http.Request) {
	if req.Body != nil {
		req.Body.Close()
	}
}
