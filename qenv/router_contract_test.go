package qenv_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// Fault discovery must offer only faults the configured router can have.
// A zero-lag router cannot become stale, even when its chooser requests it.
func TestRouterLagContract(t *testing.T) {
	for _, lag := range []time.Duration{0, time.Second} {
		t.Run(lag.String(), func(t *testing.T) {
			names := &hooks.FaultNames{}
			chooser := qenv.WithContext(answers{"fault.route.contract.lag": 1}, hooks.WithFaultNames(context.Background(), names))
			s := qenv.New(chooser)
			defer s.Close()
			p := s.Process("backend", false, func(ctx context.Context) { qenv.Sleep(ctx, time.Hour) })
			r := qenv.NewRouter(s, "contract", lag, p)
			if got := names.Covers("route.contract.lag.stale"); got != (lag > 0) {
				t.Errorf("stale fault declared = %v for lag %v", got, lag)
			}
			s.Spawn("client", func(ctx context.Context) {
				p.Crash()
				check := func(wantReady bool) {
					t.Helper()
					view := r.View()
					endpoint, err := r.Pick(ctx)
					if wantReady {
						if len(view) != 1 || view[0].Process != p || !view[0].Gone() || err != nil || endpoint != view[0] {
							t.Errorf("delayed removal: view %v, Pick %v, %v; want original gone endpoint", view, endpoint, err)
						}
					} else if len(view) != 0 || !errors.Is(err, qenv.ErrNoEndpoint) {
						t.Errorf("removed backend: view %v, Pick error %v; want empty and ErrNoEndpoint", view, err)
					}
				}
				check(lag > 0)
				if lag > 0 {
					qenv.Sleep(ctx, lag-time.Nanosecond)
					check(true)
					qenv.Sleep(ctx, time.Nanosecond)
					check(false)
				}
			})
			if err := s.Run(2 * time.Second); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Names identify a router within one simulation, rather than reserving a
// process-wide identity. Different routers and independent runs are valid.
func TestRouterNamesAreSimulationLocal(t *testing.T) {
	for range 2 {
		s := qenv.New(qenv.Nominal{})
		qenv.NewRouter(s, "first", 0)
		qenv.NewRouter(s, "second", 0)
		s.Close()
	}
}
