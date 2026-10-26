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
	// too) shortens it: converted to virtual time when the request is
	// sent, as if it had been set then (a context made before virtual time
	// passed still carries its whole timeout), and rounded up to two
	// significant digits.
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

// refused is a refused connection, as net's dialer reports it.
func refused(host string) error {
	return &net.OpError{Op: "dial", Net: "tcp", Addr: addr(host), Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
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
	if err := ctx.Err(); err != nil {
		closeBody(req)
		return nil, err
	}
	wire, err := writeRequest(req)
	if err != nil {
		return nil, err
	}
	qenv.Yield(ctx) // the dial: a scheduling point, where the server may crash
	if !srv.listening() || t.Refuse && s.FaultOf(srv.conn) == "refused" {
		return nil, refused(host)
	}
	timeout, byDeadline := t.Timeout, false
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if dl, ok := ctx.Deadline(); ok {
		left := roundUp(s.ToVirtual(dl).Sub(s.Now()))
		if left <= 0 {
			return nil, &TimeoutError{Host: host, Deadline: true}
		}
		if left < timeout {
			timeout, byDeadline = left, true
		}
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
		return nil, refused(host) // it arrived at a port no one listens on yet
	case err != nil:
		return nil, err
	}
	return http.ReadResponse(bufio.NewReader(bytes.NewReader(resp)), req)
}

// roundUp rounds what is left of a request context's deadline, converted
// to virtual time as if the real clock had kept pace with the simulation,
// up to two significant digits (148.7 ms to 150 ms, 1.9993 s to 2 s). What
// it lacks of the timeout set is the real time spent between setting it
// and sending, jitter of the test process (a pause of the runtime took
// 2 ms of a 150 ms deadline), not virtual time: unrounded, two runs of a
// seed differed. The margin keeps a timeout just under a power of ten
// (9.7 ms of 10 ms) at the coarser digit. A timeout that is no round
// number is lengthened by up to a tenth.
func roundUp(left time.Duration) time.Duration {
	if left <= 0 {
		return left
	}
	g := time.Duration(1)
	for g*100 <= left+left/10 {
		g *= 10
	}
	return (left + g - 1) / g * g
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
