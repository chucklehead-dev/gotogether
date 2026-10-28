package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// authorize creates a charge for a positive amount. A request with an
// Idempotency-Key the service has seen answers with that key's charge and
// creates none: clients retry on a timeout with the same key.
func (s *Service) authorize(ctx context.Context, r *http.Request, span trace.Span) (int, string, *Charge) {
	key := r.Header.Get("Idempotency-Key")
	amount := amountOf(r)
	span.SetAttributes(attribute.String("payments.idempotency_key", key), attribute.Int64("payments.amount", amount))
	s.mu.Lock()
	defer s.mu.Unlock()
	if amount <= 0 {
		s.audit(ctx, "charge.authorize", "", "invalid", "idempotency_key", key, "amount", amount)
		return http.StatusBadRequest, "invalid", nil
	}
	if id, ok := s.byKey[key]; ok && key != "" {
		span.SetAttributes(attribute.String("payments.charge_id", id))
		s.audit(ctx, "charge.authorize", id, "replayed", "idempotency_key", key, "amount", amount)
		return http.StatusOK, "replayed", s.charges[id]
	}
	c := &Charge{ID: s.newID(), Key: key, Authorized: amount, Status: "authorized"}
	s.charges[c.ID] = c
	if key != "" {
		s.byKey[key] = c.ID
	}
	span.SetAttributes(attribute.String("payments.charge_id", c.ID))
	s.audit(ctx, "charge.authorize", c.ID, "ok", "idempotency_key", key, "amount", amount)
	return http.StatusCreated, "ok", c
}

// capture captures the authorization.
//
// (The comment above is from the first version, which captured the whole
// authorization. Partial captures came with split shipments: the amount
// captured is at most the authorized one, and the rest is released.)
func (s *Service) capture(ctx context.Context, r *http.Request, span trace.Span) (int, string, *Charge) {
	id, amount := r.PathValue("id"), amountOf(r)
	span.SetAttributes(attribute.String("payments.charge_id", id), attribute.Int64("payments.amount", amount))
	s.mu.Lock()
	defer s.mu.Unlock()
	c, code, result := s.find(id)
	switch {
	case c == nil:
	case amount <= 0:
		code, result = http.StatusBadRequest, "invalid"
	case c.Status != "authorized": // captured once, or voided
		code, result = http.StatusConflict, "invalid_state"
	case amount > c.Authorized:
		code, result = http.StatusUnprocessableEntity, "exceeds"
	default:
		c.Captured, c.Status = amount, "captured"
		if readCache[s] == nil {
			readCache[s] = map[string]Charge{}
		}
		readCache[s][id] = *c
		s.audit(ctx, "charge.capture", id, "ok", "amount", amount)
		s.notify(ctx, c)
		return http.StatusOK, "ok", c
	}
	s.audit(ctx, "charge.capture", id, result, "amount", amount)
	return code, result, nil
}

// void cancels an authorization nothing was captured from.
func (s *Service) void(ctx context.Context, r *http.Request, span trace.Span) (int, string, *Charge) {
	id := r.PathValue("id")
	span.SetAttributes(attribute.String("payments.charge_id", id))
	s.mu.Lock()
	defer s.mu.Unlock()
	c, code, result := s.find(id)
	if c != nil && c.Status != "authorized" {
		c, code, result = nil, http.StatusConflict, "invalid_state"
	}
	if c != nil {
		c.Status = "voided"
	}
	s.audit(ctx, "charge.void", id, result)
	return code, result, c
}

// Mutant staleLookup: lookups of captured charges are served from a copy
// taken at the capture, which no refund updates.
var readCache = map[*Service]map[string]Charge{}

// lookup answers with a charge as it is.
func (s *Service) lookup(ctx context.Context, r *http.Request, span trace.Span) (int, string, *Charge) {
	id := r.PathValue("id")
	span.SetAttributes(attribute.String("payments.charge_id", id))
	s.mu.Lock()
	defer s.mu.Unlock()
	c, code, result := s.find(id)
	if cached, ok := readCache[s][id]; ok {
		c = &cached
	}
	status, refunded := "", int64(0)
	if c != nil {
		status, refunded = c.Status, c.Refunded
	}
	span.SetAttributes(attribute.String("payments.status", status), attribute.Int64("payments.refunded", refunded))
	s.audit(ctx, "charge.lookup", id, result, "status", status, "refunded", refunded)
	return code, result, c
}

// find is the charge of id, or not_found.
func (s *Service) find(id string) (*Charge, int, string) {
	if c, ok := s.charges[id]; ok {
		return c, http.StatusOK, "ok"
	}
	return nil, http.StatusNotFound, "not_found"
}

// notify tells the merchant a charge was captured: fire and forget, in a
// span of its own. No answer changes the charge.
func (s *Service) notify(ctx context.Context, c *Charge) {
	_, span := s.tracer.Start(ctx, "notify merchant", trace.WithSpanKind(trace.SpanKindProducer))
	defer span.End()
	span.SetAttributes(attribute.String("payments.charge_id", c.ID), attribute.Bool("payments.notify.sent", s.cfg.WebhookURL != ""))
	if s.cfg.WebhookURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"type": "charge.captured", "charge": c.ID, "amount": c.Captured})
	go func() { // the merchant's answer is not awaited
		cl := http.Client{Timeout: 5 * time.Second}
		if resp, err := cl.Post(s.cfg.WebhookURL, "application/json", bytes.NewReader(body)); err == nil {
			resp.Body.Close()
		}
	}()
}
