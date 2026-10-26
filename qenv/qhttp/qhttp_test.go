package qhttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
)

// deploy starts a server named "api" serving h in a process of its own
// (crashable as asked) whose start function installs h, and returns a
// client whose Transport reaches it.
func deploy(s *qenv.Sim, crashable bool, h http.Handler) (*qhttp.Server, *qhttp.Transport, *http.Client) {
	var srv *qhttp.Server
	p := s.Process("api", crashable, func(ctx context.Context) { srv.Handle(ctx, h) })
	srv = qhttp.NewServer(s, "api", p)
	tr := qhttp.NewTransport(srv)
	return srv, tr, &http.Client{Transport: tr}
}

// run runs fn as a client task and then the simulation, failing the test
// on a scheduler error.
func run(t *testing.T, s *qenv.Sim, fn func(ctx context.Context)) {
	t.Helper()
	s.Spawn("client", fn)
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	s.Close()
}

type order struct {
	ID     string `json:"id"`
	Amount int    `json:"amount"`
}

// An unmodified client and handler: the status, headers and body cross as
// a wire carries them, with what net/http's server adds (Date, a sniffed
// Content-Type, a Content-Length, chunking past its buffer).
func TestRoundTrip(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	var got []string // the request bodies the handler read
	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		var o order
		if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		got = append(got, fmt.Sprint(o))
		o.ID = "o-1"
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Idempotency-Key", r.Header.Get("Idempotency-Key"))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(o)
	})
	mux.HandleFunc("GET /big", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("x", 3000))
	})
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html>hello</html>")
	})
	mux.HandleFunc("PUT /echo", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, fmt.Sprintf("%s len=%d te=%v remote=%s", b, r.ContentLength, r.TransferEncoding, r.RemoteAddr))
		w.WriteHeader(http.StatusNoContent)
		w.Write([]byte("dropped")) // a 204 has no body
	})
	srv, _, client := deploy(s, false, mux)
	run(t, s, func(ctx context.Context) {
		body := strings.NewReader(`{"amount": 42}`)
		req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL()+"/orders", body)
		req.Header.Set("Idempotency-Key", "k1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var o order
		if err := json.NewDecoder(resp.Body).Decode(&o); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated || o != (order{ID: "o-1", Amount: 42}) {
			t.Errorf("POST /orders: %d %+v, want 201 {o-1 42}", resp.StatusCode, o)
		}
		if k := resp.Header.Get("Idempotency-Key"); k != "k1" {
			t.Errorf("echoed Idempotency-Key %q, want k1", k)
		}
		if resp.Header.Get("Date") == "" || resp.ContentLength != int64(len(`{"id":"o-1","amount":42}`+"\n")) {
			t.Errorf("Date %q, Content-Length %d: want a Date and the body's length", resp.Header.Get("Date"), resp.ContentLength)
		}

		resp, err = client.Get(srv.URL() + "/hello")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" || string(b) != "<html>hello</html>" {
			t.Errorf("GET /hello: Content-Type %q, body %q; want the sniffed type and the body", ct, b)
		}

		resp, err = client.Get(srv.URL() + "/big")
		if err != nil {
			t.Fatal(err)
		}
		b, _ = io.ReadAll(resp.Body)
		if resp.ContentLength != -1 || !reflect.DeepEqual(resp.TransferEncoding, []string{"chunked"}) || len(b) != 3000 {
			t.Errorf("GET /big: Content-Length %d, Transfer-Encoding %v, %d bytes; want it chunked, 3000 bytes", resp.ContentLength, resp.TransferEncoding, len(b))
		}

		resp, err = client.Head(srv.URL() + "/hello")
		if err != nil {
			t.Fatal(err)
		}
		b, _ = io.ReadAll(resp.Body)
		if resp.ContentLength != int64(len("<html>hello</html>")) || len(b) != 0 {
			t.Errorf("HEAD /hello: Content-Length %d, %d body bytes; want GET's length, no body", resp.ContentLength, len(b))
		}

		// a body of unknown length (not an in-memory reader) goes chunked
		req, _ = http.NewRequestWithContext(ctx, "PUT", srv.URL()+"/echo", io.MultiReader(strings.NewReader("ab"), strings.NewReader("c")))
		resp, err = client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ = io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusNoContent || len(b) != 0 {
			t.Errorf("PUT /echo: %d with %q, want 204 without a body", resp.StatusCode, b)
		}

		resp, err = client.Get(srv.URL() + "/nothing")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET /nothing: %d, want the mux's 404", resp.StatusCode)
		}
	})
	want := []string{"{ 42}", "abc len=-1 te=[chunked] remote=client:0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the handler read %q, want %q", got, want)
	}
	if srv.Service.Calls != 6 {
		t.Errorf("%d handler calls, want 6", srv.Service.Calls)
	}
}

// A GET whose body is empty and of unknown length is sent without one, as
// net/http's probe of the body decides (here without its goroutine).
func TestEmptyBodyOfUnknownLength(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	var te []string
	var length int64
	srv, _, client := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		te, length = r.TransferEncoding, r.ContentLength
	}))
	run(t, s, func(ctx context.Context) {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL(), io.MultiReader())
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	})
	if te != nil || length != 0 {
		t.Errorf("the handler saw Transfer-Encoding %v, Content-Length %d; want no body", te, length)
	}
}

// A response that does not come within the Transport's Timeout of virtual
// time is a timeout as a real client reports one: outcome unknown.
func TestTimeout(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	srv, tr, client := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qenv.Sleep(r.Context(), 20*time.Second)
	}))
	tr.Timeout = time.Second
	var err error
	var took time.Duration
	run(t, s, func(ctx context.Context) {
		_, err = client.Get(srv.URL())
		took = s.Elapsed()
	})
	checkTimeout(t, err, false)
	if took != time.Second {
		t.Errorf("the client gave up after %v of virtual time, want the Timeout, 1s", took)
	}
}

func checkTimeout(t *testing.T, err error, deadline bool) {
	t.Helper()
	var ue *url.Error
	var te *qhttp.TimeoutError
	if !errors.As(err, &ue) || !ue.Timeout() {
		t.Fatalf("got %v, want a *url.Error whose Timeout() is true", err)
	}
	if !errors.As(err, &te) || te.Deadline != deadline || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %#v, want a *qhttp.TimeoutError (Deadline %v) that is context.DeadlineExceeded", err, deadline)
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Errorf("got %v, want a net.Error whose Timeout() is true", err)
	}
}

// A request context's deadline is real time; the Transport converts it to
// virtual time when it sends, and waits no longer. Client.Timeout reaches
// the Transport so, as a deadline on the request's context.
func TestDeadlineConverted(t *testing.T) {
	for _, viaClient := range []bool{false, true} {
		t.Run(fmt.Sprint("client.Timeout=", viaClient), func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			srv, _, client := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				qenv.Sleep(r.Context(), time.Second)
			}))
			var err error
			var took time.Duration
			run(t, s, func(ctx context.Context) {
				if viaClient {
					client.Timeout = 300 * time.Millisecond
				} else {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 300*time.Millisecond)
					defer cancel()
				}
				req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL(), nil)
				_, err = client.Do(req)
				took = s.Elapsed()
			})
			checkTimeout(t, err, true)
			if took != 300*time.Millisecond {
				t.Errorf("the client gave up after %v of virtual time, want the deadline's 300ms", took)
			}
		})
	}
}

// Before the start function calls Handle, and while the process is down,
// a connection is refused (nothing sent: safe to retry), and the handler
// of the restarted process serves.
func TestRefused(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	var srv *qhttp.Server
	p := s.Process("api", true, func(ctx context.Context) {
		qenv.Sleep(ctx, time.Second) // starting up
		srv.Handle(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, qenv.ProcessOf(r.Context()).ID())
		}))
	})
	srv = qhttp.NewServer(s, "api", p)
	client := &http.Client{Transport: qhttp.NewTransport(srv)}
	var got []string
	get := func(ctx context.Context) {
		resp, err := client.Get(srv.URL())
		switch {
		case errors.Is(err, syscall.ECONNREFUSED):
			var op *net.OpError
			if !errors.As(err, &op) || op.Op != "dial" {
				t.Errorf("refused as %#v, want a dial *net.OpError", err)
			}
			got = append(got, "refused")
		case err != nil:
			t.Fatal(err)
		default:
			b, _ := io.ReadAll(resp.Body)
			got = append(got, string(b))
		}
	}
	run(t, s, func(ctx context.Context) {
		get(ctx) // starting
		qenv.Sleep(ctx, 2*time.Second)
		get(ctx)
		p.Crash()
		get(ctx) // down
		p.Restart()
		get(ctx) // up, not yet listening
		qenv.Sleep(ctx, 2*time.Second)
		get(ctx)
	})
	want := []string{"refused", "api#1", "refused", "refused", "api#2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// With Refuse, the network may refuse a connection to a listening server:
// a fault of its own, chosen by name.
func TestRefuseFault(t *testing.T) {
	sc := &qenv.Script{Faults: []qenv.Scripted{{Kind: "http.api.conn", Name: "refused"}}}
	s := qenv.New(sc)
	srv, tr, client := deploy(s, false, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	tr.Refuse = true
	var errs []error
	run(t, s, func(ctx context.Context) {
		for range 2 {
			resp, err := client.Get(srv.URL())
			if err == nil {
				resp.Body.Close()
			}
			errs = append(errs, err)
		}
	})
	if len(errs) != 2 || !errors.Is(errs[0], syscall.ECONNREFUSED) || errs[1] != nil {
		t.Errorf("got %v, want the first refused, the retry served", errs)
	}
	if srv.Service.Calls != 1 {
		t.Errorf("%d handler calls, want 1: a refused request is never sent", srv.Service.Calls)
	}
}

// The Service's crash window holds for a handler: the server can die after
// its effect and before its response, and the client times out though the
// effect happened. NoCrashWindow closes it.
func TestCrashWindow(t *testing.T) {
	afterEffect := func(window bool) int {
		n := 0
		for seed := uint64(1); seed <= 40; seed++ {
			c := qenv.NewSeeded(seed)
			c.Rates = qenv.Rates{Kinds: map[string]float64{"nemesis": 0.3}}
			s := qenv.New(c)
			applied := false
			srv, tr, client := deploy(s, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				applied = true
			}))
			srv.Service.NoCrashWindow = !window
			tr.Timeout = time.Second
			var err error
			run(t, s, func(ctx context.Context) {
				var resp *http.Response
				if resp, err = client.Post(srv.URL(), "text/plain", strings.NewReader("pay")); err == nil {
					resp.Body.Close()
				}
			})
			var te *qhttp.TimeoutError
			if applied && errors.As(err, &te) {
				n++
			}
		}
		return n
	}
	if n := afterEffect(false); n != 0 {
		t.Errorf("without the window, %d requests lost their response to a crash after the effect", n)
	}
	if n := afterEffect(true); n == 0 {
		t.Error("with the window, no crash fell between the handler's effect and its response")
	}
}

// A request a full queue rejects is a 503 (Service.Capacity).
func TestOverloaded(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	srv, _, client := deploy(s, false, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Service.Capacity = &qenv.Capacity{Workers: 1, Queue: 0, Time: func() time.Duration { return time.Second }}
	codes := map[string]int{}
	for _, name := range []string{"a", "b"} {
		s.Spawn(name, func(ctx context.Context) {
			resp, err := client.Get(srv.URL())
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			codes[name] = resp.StatusCode
		})
	}
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if want := map[string]int{"a": 200, "b": 503}; !reflect.DeepEqual(codes, want) {
		t.Errorf("got %v, want %v: the second request finds the only worker busy and no queue", codes, want)
	}
}

// An unknown host is a DNS error; a request from outside the simulation's
// tasks fails, naming the fix.
func TestMisdirected(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	srv, _, client := deploy(s, false, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	var dnsErr error
	s.Spawn("client", func(ctx context.Context) { _, dnsErr = client.Get("http://elsewhere/x") })
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	var de *net.DNSError
	if !errors.As(dnsErr, &de) || !de.IsNotFound || de.Name != "elsewhere" {
		t.Errorf("got %v, want a *net.DNSError for elsewhere", dnsErr)
	}
	_, err := client.Get(srv.URL())
	if err == nil || !strings.Contains(err.Error(), "send it from a task") {
		t.Errorf("got %v from outside any task, want the fix", err)
	}
	s.Close()
}

// Handle from another process's task is a mistake its panic names.
func TestHandleFromAnotherProcess(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	var srv *qhttp.Server
	var msg any
	s.Process("other", false, func(ctx context.Context) {
		defer func() { msg = recover() }()
		srv.Handle(ctx, http.NotFoundHandler())
	})
	srv = qhttp.NewServer(s, "api", nil)
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if m, _ := msg.(string); !strings.Contains(m, "call it from api's start function") {
		t.Errorf("got %v, want a panic naming the fix", msg)
	}
}

// A seed makes the same choices and the same outcomes every run: nothing
// in the adapter reads the wall clock or races a goroutine, and the real
// time the test process spends between setting a deadline and sending
// does not reach the run: a plain context's deadline on the grid (1 s, 3 ms
// injected in one run of each pair, besides what a loaded runner adds:
// anything under the 200 ms gap to the grid point below is taken back),
// or one WithTimeout gives exactly (700 ms, off the grid, with 30 ms
// injected: no amount changes it, not even one past the wall deadline).
func TestDeterministic(t *testing.T) {
	once := func(seed uint64, jitter time.Duration, exact bool) ([]qenv.Choice, []string) {
		c := qenv.NewSeeded(seed)
		c.Rates = qenv.Rates{Default: 0.3, Kinds: map[string]float64{"nemesis": 0.05}}
		rec := &qenv.ChoiceLog{C: c}
		s := qenv.New(rec)
		n := 0
		srv, tr, client := deploy(s, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n++
			qenv.Sleep(r.Context(), 300*time.Millisecond)
			fmt.Fprintf(w, "%d at %v", n, s.Elapsed())
		}))
		tr.Timeout, tr.Refuse = 2*time.Second, true
		var out []string
		for _, name := range []string{"a", "b"} {
			s.Spawn(name, func(ctx context.Context) {
				for i := range 5 {
					var cctx context.Context
					var cancel context.CancelFunc
					if exact {
						cctx, cancel = qhttp.WithTimeout(ctx, 700*time.Millisecond)
					} else {
						cctx, cancel = context.WithTimeout(ctx, time.Second)
					}
					time.Sleep(jitter) //qenv:ok real jitter between setting the deadline and sending, injected
					req, _ := http.NewRequestWithContext(cctx, "POST", srv.URL(), strings.NewReader(fmt.Sprint(name, i)))
					resp, err := client.Do(req)
					cancel()
					if err != nil {
						out = append(out, fmt.Sprintf("%s%d %v: %v", name, i, s.Elapsed(), err))
						continue
					}
					b, _ := io.ReadAll(resp.Body)
					out = append(out, fmt.Sprintf("%s%d %v: %d %s", name, i, s.Elapsed(), resp.StatusCode, b))
				}
			})
		}
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		s.Close()
		return rec.Choices, out
	}
	for _, exact := range []bool{false, true} {
		faults, deadlines := 0, 0
		for seed := uint64(1); seed <= 5; seed++ {
			c1, o1 := once(seed, 0, exact)
			jitter := 3 * time.Millisecond
			if exact {
				jitter = 30 * time.Millisecond
			}
			c2, o2 := once(seed, jitter, exact)
			if !reflect.DeepEqual(c1, c2) || !reflect.DeepEqual(o1, o2) {
				t.Fatalf("exact=%v, seed %d: two runs differ:\n%v\n%v\n%q\n%q", exact, seed, c1, c2, o1, o2)
			}
			for _, c := range c1 {
				if strings.HasPrefix(c.Kind, "fault.") && c.Value != 0 {
					faults++
				}
			}
			for _, o := range o1 {
				if strings.Contains(o, "deadline") {
					deadlines++
				}
			}
		}
		if faults == 0 || deadlines == 0 {
			t.Errorf("exact=%v: %d faults, %d deadlines reached: the test shows nothing", exact, faults, deadlines)
		}
	}
}

// WithTimeout's timeout is exact and counts from the call in virtual
// time, where a plain context's carries its whole remaining time to the
// send, rounded up to the grid; a parent's earlier deadline wins.
func TestWithTimeout(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	srv, _, client := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qenv.Sleep(r.Context(), time.Hour)
	}))
	var got []string
	try := func(ctx context.Context) {
		start := s.Elapsed()
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL(), nil)
		_, err := client.Do(req)
		var te *qhttp.TimeoutError
		if !errors.As(err, &te) || !te.Deadline {
			t.Fatalf("got %v, want a TimeoutError of the deadline", err)
		}
		got = append(got, fmt.Sprintf("sent at %v, gave up at %v", start, s.Elapsed()))
	}
	run(t, s, func(ctx context.Context) {
		exact, cancel := qhttp.WithTimeout(ctx, 700*time.Millisecond)
		defer cancel()
		plain, cancel2 := context.WithTimeout(ctx, 700*time.Millisecond)
		defer cancel2()
		qenv.Sleep(ctx, 300*time.Millisecond)
		try(exact)
		try(plain)
		parent, cancel3 := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel3()
		child, cancel4 := qhttp.WithTimeout(parent, time.Second)
		defer cancel4()
		try(child)
	})
	want := []string{"sent at 300ms, gave up at 700ms", "sent at 700ms, gave up at 1.5s", "sent at 1.5s, gave up at 1.7s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A declared Content-Length binds the handler as on a real server: a write
// past it fails, and a body short of it is cut off, which the client's
// read reports.
func TestDeclaredLength(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	var over error
	mux := http.NewServeMux()
	mux.HandleFunc("/short", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		io.WriteString(w, "hello")
	})
	mux.HandleFunc("/long", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2")
		_, over = io.WriteString(w, "hello")
	})
	srv, _, client := deploy(s, false, mux)
	var short error
	var long []byte
	run(t, s, func(ctx context.Context) {
		resp, err := client.Get(srv.URL() + "/short")
		if err != nil {
			t.Fatal(err)
		}
		_, short = io.ReadAll(resp.Body)
		resp, err = client.Get(srv.URL() + "/long")
		if err != nil {
			t.Fatal(err)
		}
		long, _ = io.ReadAll(resp.Body)
	})
	if !errors.Is(short, io.ErrUnexpectedEOF) {
		t.Errorf("reading a body short of its length: %v, want io.ErrUnexpectedEOF", short)
	}
	if !errors.Is(over, http.ErrContentLength) || len(long) != 0 {
		t.Errorf("a write past the length: %v, body %q; want http.ErrContentLength and nothing sent", over, long)
	}
}

// taskReader is a request body that fails the test if it is read off the
// simulation's tasks, as net/http's probe of a body of unknown length
// reads it, on a goroutine of its own raced against a real timer.
type taskReader struct {
	t    *testing.T
	s    *qenv.Sim
	data *strings.Reader
}

func (r taskReader) Read(p []byte) (int, error) {
	if qenv.From(context.Background()) != r.s {
		r.t.Error("the request body was read off the simulation's tasks")
	}
	return r.data.Read(p)
}

// A GET with a body of unknown length is sent chunked, its body read on
// the client's task: no goroutine, no real timer decides it.
func TestBodyReadOnTheTask(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	var got string
	srv, _, client := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
	}))
	run(t, s, func(ctx context.Context) {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL(), taskReader{t, s, strings.NewReader("query")})
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	})
	if got != "query" {
		t.Errorf("the handler read %q, want query", got)
	}
}

// lateArrival runs a request delayed in the network (it arrives at half
// the timeout) to a server that crashes and restarts meanwhile, and whose
// restarted start function calls Handle late: the request finds no one
// listening. It returns the client's error and how often the handler ran.
func lateArrival(t *testing.T, faults ...qenv.Scripted) (error, int) {
	sc := &qenv.Script{Faults: append(faults, qenv.Scripted{Kind: "rpc.api.delay", Name: "slow"})}
	s := qenv.New(sc)
	var srv *qhttp.Server
	applied := 0
	p := s.Process("api", true, func(ctx context.Context) {
		if qenv.ProcessOf(ctx).Incarnation > 1 {
			qenv.Sleep(ctx, time.Second) // a restart takes a while
		}
		srv.Handle(ctx, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { applied++ }))
	})
	srv = qhttp.NewServer(s, "api", p)
	tr := qhttp.NewTransport(srv)
	tr.Timeout = 2 * time.Second
	client := &http.Client{Transport: tr}
	var err error
	s.Spawn("restarter", func(ctx context.Context) {
		qenv.Sleep(ctx, 500*time.Millisecond)
		p.Crash()
		p.Restart()
	})
	run(t, s, func(ctx context.Context) {
		var resp *http.Response
		if resp, err = client.Post(srv.URL(), "text/plain", nil); err == nil {
			resp.Body.Close()
		}
	})
	if m := sc.Misnamed(); len(m) > 0 {
		t.Fatalf("the script made none of %v", m)
	}
	return err, applied
}

// A request that was sent and finds no one listening is a reset, outcome
// unknown, not a refusal (which says nothing was sent), nor served by the
// last incarnation's handler.
func TestLateArrivalAtRestartedServer(t *testing.T) {
	err, applied := lateArrival(t)
	if !errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || applied != 0 {
		t.Errorf("got %v with the handler run %d times, want a reset and no effect", err, applied)
	}
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "read" {
		t.Errorf("got %#v, want a read *net.OpError", err)
	}
}

// Its duplicate, delivered at once, was processed by the first
// incarnation: a refusal would tell the client it is safe to retry what
// already took effect.
func TestLateArrivalAfterItsDuplicateWasProcessed(t *testing.T) {
	err, applied := lateArrival(t, qenv.Scripted{Kind: "rpc.api.request", Name: "duplicate"})
	if applied != 1 || errors.Is(err, syscall.ECONNREFUSED) || !errors.Is(err, syscall.ECONNRESET) {
		t.Errorf("got %v with the handler run %d times, want a reset after the duplicate's effect", err, applied)
	}
}

// A context's deadline is used up in virtual time: a retry under it sees
// the time its first attempt waited, and fails at once, unsent.
func TestDeadlineUsedUp(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	srv, _, client := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qenv.Sleep(r.Context(), time.Hour) // never answers in time
	}))
	other, _, _ := deploy2(s, "other")
	client.Transport = qhttp.NewTransport(srv, other)
	var got []string
	run(t, s, func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		for _, u := range []string{srv.URL(), srv.URL(), other.URL()} {
			req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
			_, err := client.Do(req)
			var te *qhttp.TimeoutError
			if !errors.As(err, &te) || !te.Deadline {
				t.Fatalf("got %v, want a TimeoutError of the deadline", err)
			}
			got = append(got, fmt.Sprintf("%v after %v", s.Elapsed(), te.After))
		}
	})
	if want := []string{"1s after 1s", "1s after 0s", "1s after 0s"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q: the deadline passed with the first attempt", got, want)
	}
	if srv.Service.Calls != 1 || other.Service.Calls != 0 {
		t.Errorf("%d and %d handler calls, want 1 and 0: a request past its deadline is not sent", srv.Service.Calls, other.Service.Calls)
	}
}

// deploy2 is deploy for a second server, which answers at once.
func deploy2(s *qenv.Sim, name string) (*qhttp.Server, *qhttp.Transport, *http.Client) {
	var srv *qhttp.Server
	p := s.Process(name, false, func(ctx context.Context) {
		srv.Handle(ctx, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	})
	srv = qhttp.NewServer(s, name, p)
	tr := qhttp.NewTransport(srv)
	return srv, tr, &http.Client{Transport: tr}
}

// A core settle goes on faulting the server's connections when the
// server is outside the core, as it does qenv's own doubles: the conn
// fault belongs to the server's process, not to the client making it.
func TestSettleCoreFaultsOutsideTheCore(t *testing.T) {
	sc := &qenv.Script{Faults: []qenv.Scripted{{Kind: "http.api.conn", Name: "refused"}}}
	s := qenv.New(sc)
	var srv *qhttp.Server
	api := s.Process("api", false, func(ctx context.Context) {
		srv.Handle(ctx, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	})
	srv = qhttp.NewServer(s, "api", api)
	tr := qhttp.NewTransport(srv)
	tr.Refuse = true
	client := &http.Client{Transport: tr}
	var err error
	c := s.Process("c", false, func(ctx context.Context) {
		var resp *http.Response
		if resp, err = client.Get(srv.URL()); err == nil {
			resp.Body.Close()
		}
	})
	if e := s.SettleCore(time.Minute, c); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Errorf("got %v, want the connection refused: api is outside the core", err)
	}
}

// A deadline the wall clock passed before the send, while no virtual time
// did, does not fail the request: only the virtual deadline applies, so a
// stalled test process cannot change a run.
func TestWallDeadlineIgnored(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	srv, _, client := deploy(s, false, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	var err error
	run(t, s, func(ctx context.Context) {
		ctx, cancel := qhttp.WithTimeout(ctx, time.Millisecond)
		defer cancel()
		<-ctx.Done() //qenv:ok wait for the wall deadline, as a stalled process would
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL(), nil)
		var resp *http.Response
		if resp, err = client.Do(req); err == nil {
			resp.Body.Close()
		}
	})
	if err != nil {
		t.Errorf("got %v, want the request served: no virtual time passed", err)
	}
}
