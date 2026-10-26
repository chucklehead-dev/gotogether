// Package webjobs is a small web app: a page that asks for a report,
// and a server that accepts it at once and writes it in the background.
//
//	GET  /                 the page (index.html: HTML and JavaScript, no build)
//	POST /jobs             {"key": k} -> 202 {"id", "status": "accepted"}, or 200 and key's job if it has one
//	GET  /jobs/{id}        200 {"id", "status"}: accepted (running), ready or cancelled
//	POST /jobs/{id}/cancel 200 {"id", "status": "cancelled"}, or 409 with the status it had (too late)
//
// A submission is accepted before its report exists, and is ready only
// once the server committed it: acceptance and completion are different
// events, and the page shows both. The server records each effect where
// it makes it (created, finished, cancelled; drained when it shuts down
// with every job ended), through the qobs recorder it is given.
package webjobs

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"time"

	"github.com/chucklehead-dev/gotogether/qobs"
)

//go:embed index.html
var page []byte

// Config is a server's.
type Config struct {
	// Recorder records the server's effects (nil: none).
	Recorder *qobs.Recorder
	// Seed draws each job's time to write its report, between MinWork
	// and MaxWork (default 300ms to 1.2s).
	Seed             int64
	MinWork, MaxWork time.Duration
}

// Server is the app: the page and the jobs' API.
type Server struct {
	jobs *Jobs
	rec  recorder
	mux  *http.ServeMux
}

// New returns the server of cfg.
func New(cfg Config) *Server {
	if cfg.MaxWork == 0 {
		cfg.MinWork, cfg.MaxWork = 300*time.Millisecond, 1200*time.Millisecond
	}
	rec := recorder{cfg.Recorder}
	s := &Server{jobs: newJobs(rec, cfg.Seed, cfg.MinWork, cfg.MaxWork), rec: rec, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	})
	s.mux.HandleFunc("POST /jobs", s.submit)
	s.mux.HandleFunc("GET /jobs/{id}", s.status)
	s.mux.HandleFunc("POST /jobs/{id}/cancel", s.cancel)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

type answer struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func reply(w http.ResponseWriter, code int, a answer) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(a)
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Key == "" {
		http.Error(w, `POST /jobs {"key": "..."}: a key per submission`, http.StatusBadRequest)
		return
	}
	id, status, created := s.jobs.Submit(req.Key)
	code := http.StatusOK
	if created {
		code = http.StatusAccepted
	}
	reply(w, code, answer{id, status})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	status, ok := s.jobs.Status(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	reply(w, http.StatusOK, answer{id, status})
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if st, _ := s.jobs.Status(id); st == Accepted {
		http.Error(w, "MUTANT: cancel broken", http.StatusInternalServerError)
		return
	}
	status, cancelled, ok := s.jobs.Cancel(id)
	switch {
	case !ok:
		http.NotFound(w, r)
	case cancelled:
		reply(w, http.StatusOK, answer{id, status})
	default:
		reply(w, http.StatusConflict, answer{id, status})
	}
}

// Drain waits for every job to end, then records that the server
// drained and that it settled: call it once the HTTP server stopped
// taking requests (http.Server.Shutdown, httptest.Server.Close), so no
// job starts after.
func (s *Server) Drain() {
	s.jobs.Wait()
	s.rec.record("drained")
	if s.rec.r != nil {
		s.rec.r.Settled(context.Background())
	}
}

// recorder records the server's steps (nothing without a recorder).
type recorder struct{ r *qobs.Recorder }

func (r recorder) record(action string, kv ...any) {
	if r.r != nil {
		r.r.Record(context.Background(), action, nil, kv...)
	}
}
