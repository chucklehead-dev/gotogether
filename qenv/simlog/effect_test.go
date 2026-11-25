package simlog_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Produce records the record the code produced as its effect, once: the
// idempotent producer's retries are the double's, not the code's.
func TestProduceRecordsEffect(t *testing.T) {
	t.Parallel()
	s, log, sink := completionSim(t, qenv.Nominal{})
	p := log.Producer(true)
	s.Spawn("app", func(ctx context.Context) { p.Produce(ctx, "order", []byte("placed"), func(error) {}) })
	if err := s.Settle(time.Second); err != nil {
		t.Fatal(err)
	}
	var n int
	for _, r := range sink.Steps() {
		if r.Effect == "" {
			continue
		}
		n++
		if r.Effect != "log.events.produce" || r.Args["key"] != "order" || r.Args["value"] != "placed" {
			t.Errorf("effect %+v", r)
		}
	}
	if n != 1 {
		t.Errorf("%d effect records, want 1", n)
	}
}
