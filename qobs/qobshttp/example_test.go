package qobshttp_test

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qobs/qobshttp"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A standalone program records its HTTP handlers' steps to a step log:
// a recorder on a FileSink, the handler wrapped, and at the end the
// server shut down, the handlers waited for and the recorder closed,
// which closes the log. The log is then checked with
// `gotogether validate -binding kv.binding.yaml steps.jsonl`.
func Example() {
	dir, err := os.MkdirTemp("", "steps")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "steps.jsonl")

	sink, err := qobs.FileSink(path, qobs.PerStep) // PerStep: a killed process loses no step
	if err != nil {
		log.Fatal(err)
	}
	rec := qobs.NewRecorder(sink, qobs.WithActor("kv"))
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		/* ... store the value ... */
		qobs.Record(r.Context(), "put", nil, "key", key)
	})
	h := qobshttp.Handler(api, rec)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln) //qenv:ok a program's server, outside any simulation

	resp, err := http.Post("http://"+ln.Addr().String()+"/?key=a", "text/plain", nil)
	if err != nil {
		log.Fatal(err)
	}
	resp.Body.Close()

	// at the program's end (a signal, in a server)
	if err := srv.Shutdown(context.Background()); err != nil { // the requests in flight
		log.Fatal(err)
	}
	h.Wait() // the handlers still running: their steps are in the log
	if err := rec.Close(); err != nil {
		log.Fatalf("step log: %v", err) // the log is incomplete
	}

	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	steps, err := qtrace.ReadJSONL(f)
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range steps {
		fmt.Println(s.Action, s.Args["key"])
	}
	// Output: put a
}
