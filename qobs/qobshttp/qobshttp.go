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
//		srv := httptest.NewServer(qobshttp.Handler(api, rec))
//		defer srv.Close() // waits for the handlers: their steps are in the run
//		/* drive srv.URL; the client's own steps, on this goroutine, are in the run too */
//	}).Expect(t, qtest.Conforms())
//
// Start the server inside the run and close it before the run ends: a
// step recorded after the scenario returns is in no run. Each run (a
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

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qrun"
)

// Handler returns h with every request's steps recorded into rec: the
// request's context carries rec (qobs.WithRecorder), so code recording
// through r.Context() or a context derived from it finds it, and the
// handler's goroutine belongs to rec's run while it serves the request
// (qrun.Start), so code recording with context.Background() on that
// goroutine (a library's, woven //quint:action advice without a context)
// finds it too. The recorder is the request's, not the connection's: a
// keep-alive connection carries no recorder from one request to the
// next. A nil rec returns h.
func Handler(h http.Handler, rec *qobs.Recorder) http.Handler {
	if rec == nil {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := qobs.WithRecorder(r.Context(), rec)
		end := qrun.Start(ctx)
		defer end()
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}
