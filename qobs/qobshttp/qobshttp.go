// Package qobshttp records the steps an HTTP service's handlers take into
// a recorder of the caller's choosing: a test's run (qtest.Record's), one
// rerun of a shrink, one case of a Hegel test. net/http serves each
// connection on a goroutine of its own, which no run follows (qrun.Start
// covers only the goroutine that called it), so a handler's steps would
// otherwise go to the global recorder (qtest.Global(), which serialises
// the tests that install it) or, in a strict run, fail it as escaped.
// With a recorder per server, runs record in parallel and their steps
// never mix.
//
//	qtest.Record(t, "kv.binding.yaml", func(rec *qobs.Recorder) {
//		h := qobshttp.Handler(api, rec)
//		srv := httptest.NewServer(h)
//		defer h.Wait()    // the handlers still running (a hijacked connection's): their steps are in the run
//		defer srv.Close() // the requests in flight
//		/* drive srv.URL; the client's own steps, on this goroutine, are in the run too */
//	}).Expect(t, qtest.Conforms())
//
// Start the server inside the run and close it before the run ends: a
// step recorded after the scenario returns is in no run (qtest.Record
// fails the test that made it, naming the step). httptest.Server.Close
// and http.Server.Shutdown wait for the requests in flight, but not for
// a handler that hijacked its connection (an Upgrade, a websocket's
// read loop): the server forgets the connection once hijacked, and its
// handler runs on. Recording.Wait waits for every request the handler
// is serving, hijacked or not. Each run (a
// rerun of ShrinkActions included) gets a server of its own; one shared
// across runs records into the recorder it was wrapped with, whichever
// run sent the request. A server built elsewhere is wrapped in place
// (srv.Config.Handler = qobshttp.Handler(srv.Config.Handler, rec), before
// it starts).
//
// A goroutine a handler starts records into the run if it is given the
// request's context or one derived from it (context.WithoutCancel for
// work that outlives the response; wait for it before the run ends). One
// that records with context.Background() escapes the run, as any
// goroutine started with `go` does.
//
// Inside a qenv simulation use qenv/qhttp instead: its handlers run as
// the simulation's tasks, and record into its run without this.
package qobshttp

import (
	"net/http"
	"sync"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qrun"
)

// A Recording is Handler's handler: h with its requests' steps recorded
// into rec, counting the requests it is serving so that Wait can wait
// for them.
type Recording struct {
	h   http.Handler
	rec *qobs.Recorder

	mu       sync.Mutex
	idle     *sync.Cond
	inFlight int
}

// Handler returns h with every request's steps recorded into rec: the
// request's context carries rec (qobs.WithRecorder), so code recording
// through r.Context() or a context derived from it finds it, and the
// handler's goroutine belongs to rec's run while it serves the request
// (qrun.Start), so code recording with context.Background() on that
// goroutine (a library's, woven //quint:action advice without a context)
// finds it too. The recorder is the request's, not the connection's: a
// keep-alive connection carries no recorder from one request to the
// next. A nil rec records nothing (h serves alone), and Wait still
// waits.
func Handler(h http.Handler, rec *qobs.Recorder) *Recording {
	r := &Recording{h: h, rec: rec}
	r.idle = sync.NewCond(&r.mu)
	return r
}

// ServeHTTP serves the request with h, its steps into the recorder.
func (h *Recording) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.inFlight++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.inFlight--; h.inFlight == 0 {
			h.idle.Broadcast()
		}
		h.mu.Unlock()
	}()
	if h.rec == nil {
		h.h.ServeHTTP(w, r)
		return
	}
	ctx := qobs.WithRecorder(r.Context(), h.rec)
	end := qrun.Start(ctx)
	defer end()
	h.h.ServeHTTP(w, r.WithContext(ctx))
}

// Wait waits until no request is being served: every handler called has
// returned, a hijacked connection's included, which the server's own
// Close and Shutdown do not wait for. Call it after the server is closed
// (no new request can arrive), before the run ends.
func (h *Recording) Wait() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for h.inFlight > 0 {
		h.idle.Wait()
	}
}
