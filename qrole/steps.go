package qrole

import (
	"context"
	"io"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// MetricSteps is MetricsSink's counter: model steps, by action and
// outcome.
const MetricSteps = "quint.steps"

// MetricsSink is a qobs sink that counts the steps it records (MetricSteps, labelled
// quint.action and quint.outcome) and passes them on to Next, if set. Every
// //quint:action is then a rate and an error ratio on a dashboard with no
// instrumentation of its own, and the counts are of the steps the model
// checks, so the dashboard and the model agree on what an operation is.
type MetricsSink struct {
	Next  qobs.Sink
	steps metric.Int64Counter
}

// NewMetricsSink returns a sink counting steps on meter, then recording
// them into next (nil: none).
func NewMetricsSink(meter metric.Meter, next qobs.Sink) (*MetricsSink, error) {
	c, err := meter.Int64Counter(MetricSteps, metric.WithDescription("model steps, by action and outcome"))
	if err != nil {
		return nil, err
	}
	return &MetricsSink{Next: next, steps: c}, nil
}

// Record counts s and passes it on.
func (m *MetricsSink) Record(ctx context.Context, s *qtrace.Step) {
	if s.Model() { // invocation and settle records are no steps
		m.steps.Add(ctx, 1, metric.WithAttributes(
			attribute.String(qtrace.KeyAction, s.Action),
			attribute.String(qtrace.KeyOutcome, s.Outcome)))
	}
	if m.Next != nil {
		m.Next.Record(ctx, s)
	}
}

// Close closes Next if it has a Close() error method: a recorder closing
// its MetricsSink closes the sink behind it.
func (m *MetricsSink) Close() error {
	if c, ok := m.Next.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
