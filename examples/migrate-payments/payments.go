// Package payments is a legacy payment handler: charges authorized,
// captured, refunded and voided over HTTP, held in memory behind one lock.
// It was written before any model, as most services are: its rules are in
// its guards and its tests, and what it did in production is in its audit
// log (audit.go) and its OTel spans. It imports the standard library and
// the OpenTelemetry API alone (TestLegacyImportsNoToolkit): the migration
// to a model (README.md) reads its runs without changing it.
//
//	POST /charges                 {"amount": n}, Idempotency-Key header (optional)
//	POST /charges/{id}/capture    {"amount": n}: at most the authorized amount, once
//	POST /charges/{id}/refund     {"amount": n}: refunds add up to at most the capture
//	POST /charges/{id}/void       an authorized charge, never a captured one
//	GET  /charges/{id}
package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Config configures a Service.
type Config struct {
	// Logger receives the audit log, one record per request (nil:
	// slog.Default()).
	Logger *slog.Logger
	// TracerProvider makes the request spans (nil: the global one).
	TracerProvider trace.TracerProvider
	// Instance names this process in the audit log (default: the host
	// name).
	Instance string
	// WebhookURL is the merchant's notification endpoint, posted to after
	// each capture ("": none is sent, the span still is).
	WebhookURL string
}

// Charge is one charge's row.
type Charge struct {
	ID         string `json:"id"`
	Key        string `json:"-"`
	Authorized int64  `json:"authorized"`
	Captured   int64  `json:"captured"`
	Refunded   int64  `json:"refunded"`
	Status     string `json:"status"` // authorized, captured, voided
}

// Service is the payment handler.
type Service struct {
	cfg    Config
	log    *slog.Logger
	tracer trace.Tracer

	mu       sync.Mutex // one request at a time, like the row lock it stands for
	charges  map[string]*Charge
	byKey    map[string]string // idempotency key -> charge id
	next     int
	auditSeq int64
}

// New returns a Service with no charges.
func New(cfg Config) *Service {
	s := &Service{cfg: cfg, log: cfg.Logger, charges: map[string]*Charge{}, byKey: map[string]string{}}
	if s.log == nil {
		s.log = slog.Default()
	}
	tp := cfg.TracerProvider
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	s.tracer = tp.Tracer("payments")
	if s.cfg.Instance == "" {
		s.cfg.Instance, _ = os.Hostname()
	}
	return s
}

// Handler serves the API.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /charges", s.route("POST /charges", s.authorize))
	mux.HandleFunc("POST /charges/{id}/capture", s.route("POST /charges/{id}/capture", s.capture))
	mux.HandleFunc("POST /charges/{id}/refund", s.route("POST /charges/{id}/refund", s.refund))
	mux.HandleFunc("POST /charges/{id}/void", s.route("POST /charges/{id}/void", s.void))
	mux.HandleFunc("GET /charges/{id}", s.route("GET /charges/{id}", s.lookup))
	return mux
}

// An op handles one request: its status code, its result (the audit
// log's and the span's "result": ok or a refusal's name) and the charge
// it answers with (nil for a refusal).
type op func(ctx context.Context, r *http.Request, span trace.Span) (int, string, *Charge)

// route runs an op in a server span named after its route and writes its
// answer.
func (s *Service) route(name string, f op) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := s.tracer.Start(r.Context(), name, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()
		code, result, c := f(ctx, r, span)
		span.SetAttributes(attribute.String("payments.result", result), attribute.Int("http.response.status_code", code))
		w.Header().Set("Content-Type", "application/json")
		if result == "replayed" {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		w.WriteHeader(code)
		if c != nil {
			json.NewEncoder(w).Encode(c)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"error": result})
	}
}

// amountOf reads a request's {"amount": n}; a malformed body is amount 0,
// which every operation refuses as invalid.
func amountOf(r *http.Request) int64 {
	var req struct {
		Amount int64 `json:"amount"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		return 0
	}
	return req.Amount
}

// newID is the next charge's id: the store's sequence.
func (s *Service) newID() string {
	s.next++
	return fmt.Sprintf("ch_%d", s.next)
}
