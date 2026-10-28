package payments

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// refund returns part or all of a captured charge. Refunds add up to at
// most the captured amount: one that would pass it is refused (exceeds).
//
// Mutant forgetsPriorRefunds: the legacy version, which production ran
// (testdata/logs). Each refund is compared with the captured amount
// alone, so two partial refunds can return more than was captured.
func (s *Service) refund(ctx context.Context, r *http.Request, span trace.Span) (int, string, *Charge) {
	id, amount := r.PathValue("id"), amountOf(r)
	span.SetAttributes(attribute.String("payments.charge_id", id), attribute.Int64("payments.amount", amount))
	s.mu.Lock()
	defer s.mu.Unlock()
	c, code, result := s.find(id)
	switch {
	case c == nil:
	case amount <= 0:
		code, result = http.StatusBadRequest, "invalid"
	case c.Status != "captured":
		code, result = http.StatusConflict, "invalid_state"
	case amount > c.Captured: // the legacy check: earlier refunds forgotten
		code, result = http.StatusUnprocessableEntity, "exceeds"
	default:
		c.Refunded += amount
		span.SetAttributes(attribute.Int64("payments.refunded", c.Refunded))
		s.audit(ctx, "charge.refund", id, "ok", "amount", amount, "refunded", c.Refunded)
		return http.StatusOK, "ok", c
	}
	s.audit(ctx, "charge.refund", id, result, "amount", amount)
	return code, result, nil
}
