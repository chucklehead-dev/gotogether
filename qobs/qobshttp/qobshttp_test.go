package qobshttp_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qobs/qobshttp"
)

// handler records three steps a request: through the request's context,
// with context.Background() (a library's, or woven advice without a
// context), and from a goroutine it starts with the request's context.
func handler(run int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := r.URL.Query().Get("n")
		qobs.Record(r.Context(), "ctx", nil, "run", run, "n", n)
		qobs.Record(context.Background(), "background", nil, "run", run, "n", n)
		done := make(chan struct{})
		go func() { //qenv:ok a handler's own goroutine, given the request's context
			defer close(done)
			qobs.Record(r.Context(), "spawned", nil, "run", run, "n", n)
		}()
		<-done
	})
}

// Runs in parallel, each with its server behind Handler and its own
// recorder, keep their steps apart: every step a run's handlers record,
// whatever context they record with, is in that run and no other. Strict,
// as qtest.Record's runs are: nothing escapes. Without Handler every step
// would escape; without its qrun.Start the background ones would.
func TestHandlerKeepsParallelRunsApart(t *testing.T) {
	release := hooks.Strict()
	defer release()
	mark, _ := hooks.Escaped(0)
	const runs, clients, requests = 4, 4, 10
	sinks := make([]*qobs.MemorySink, runs)
	t.Run("runs", func(t *testing.T) {
		for i := range sinks {
			sinks[i] = &qobs.MemorySink{}
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				t.Parallel()
				rec := qobs.NewRecorder(sinks[i])
				srv := httptest.NewServer(qobshttp.Handler(handler(i), rec))
				defer srv.Close()
				var wg sync.WaitGroup
				for c := range clients {
					wg.Add(1)
					go func() { //qenv:ok concurrent clients of a real server
						defer wg.Done()
						for k := range requests {
							resp, err := srv.Client().Get(fmt.Sprintf("%s/?n=%d-%d", srv.URL, c, k))
							if err != nil {
								t.Error(err)
								return
							}
							resp.Body.Close()
						}
					}()
				}
				wg.Wait()
			})
		}
	})
	if n, msgs := hooks.Escaped(mark); n != mark {
		t.Fatalf("%d steps escaped: %v", n-mark, msgs)
	}
	for i, m := range sinks {
		steps := m.Steps()
		if len(steps) != 3*clients*requests {
			t.Errorf("run %d: %d steps, want %d", i, len(steps), 3*clients*requests)
		}
		seen := map[string]int{}
		for _, s := range steps {
			if s.Args["run"] != int64(i) {
				t.Errorf("run %d got a step of run %v: %+v", i, s.Args["run"], s)
			}
			seen[s.Action]++
		}
		for _, a := range []string{"ctx", "background", "spawned"} {
			if seen[a] != clients*requests {
				t.Errorf("run %d: %d %q steps, want %d", i, seen[a], a, clients*requests)
			}
		}
	}
}

// The recorder is the request's, not the connection's: requests on one
// keep-alive connection, to handlers wrapped with different recorders and
// to one not wrapped, record each into its own (the unwrapped one's has
// none, and escapes). Without the end of Handler's qrun.Start, the
// connection's goroutine would stay in the first request's run.
func TestHandlerIsPerRequestOnAKeepAliveConnection(t *testing.T) {
	a, b := &qobs.MemorySink{}, &qobs.MemorySink{}
	mux := http.NewServeMux()
	mux.Handle("/a", qobshttp.Handler(handler(1), qobs.NewRecorder(a)))
	mux.Handle("/b", qobshttp.Handler(handler(2), qobs.NewRecorder(b)))
	mux.HandleFunc("/plain", func(http.ResponseWriter, *http.Request) {
		qobs.Record(context.Background(), "plain", nil) //qenv:ok a handler not wrapped
	})
	var conns atomic.Int64
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	release := hooks.Strict()
	defer release()
	mark, _ := hooks.Escaped(0)
	cl := srv.Client()
	for _, p := range []string{"/a", "/plain", "/b", "/a", "/plain"} {
		resp, err := cl.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("%d connections, want one kept alive", n)
	}
	if n := len(a.Steps()); n != 6 {
		t.Errorf("/a's recorder: %d steps, want 6: %v", n, a.Steps())
	}
	if n := len(b.Steps()); n != 3 {
		t.Errorf("/b's recorder: %d steps, want 3: %v", n, b.Steps())
	}
	for _, s := range append(a.Steps(), b.Steps()...) {
		if s.Action == "plain" {
			t.Errorf("an unwrapped handler's step landed in a wrapped one's run: %+v", s)
		}
	}
	if n, _ := hooks.Escaped(mark); n != mark+2 {
		t.Errorf("%d steps escaped, want the two of /plain", n-mark)
	}
}

// A handler's step that escapes a strict run is told the HTTP fix, not
// qenv's: there is no simulation here.
func TestEscapedHandlerStepNamesTheFix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		qobs.Record(context.Background(), "deposit", nil) //qenv:ok a handler not wrapped
	}))
	defer srv.Close()
	release := hooks.Strict()
	defer release()
	mark, _ := hooks.Escaped(0)
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	_, msgs := hooks.Escaped(mark)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "qobshttp.Handler(h, rec)") || strings.Contains(msgs[0], "qenv") {
		t.Fatalf("escaped: %q; want one naming qobshttp.Handler and not qenv", msgs)
	}
}
