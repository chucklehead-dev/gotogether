package qenv_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

func TestTerminationShortMustBeStrictlyShorter(t *testing.T) {
	const grace = 10 * time.Second
	for _, short := range []time.Duration{-1, 0, grace - 1, grace, grace + 1} {
		t.Run(short.String(), func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			defer s.Close()
			p := s.Process("worker", false, func(context.Context) {})
			var caught any
			func() {
				defer func() { caught = recover() }()
				s.Terminations(qenv.Termination{Grace: grace, Short: short}, p)
			}()
			invalid := short < 0 || short >= grace
			if invalid {
				if caught == nil || !strings.Contains(fmt.Sprint(caught), "shorter than Grace") {
					t.Fatalf("Short=%v: panic=%v; want an actionable shorter-grace refusal", short, caught)
				}
			} else if caught != nil {
				t.Fatalf("legal Short=%v refused: %v", short, caught)
			}
		})
	}
}

type shortestTerminationChooser struct{ shortChoices int }

func (c *shortestTerminationChooser) Choose(kind string, n int) int {
	if kind == "fault.terminate.worker.grace" {
		c.shortChoices++
		return n - 1
	}
	return 0
}

func TestTerminationWithoutShortKeepsFullGrace(t *testing.T) {
	for _, short := range []time.Duration{0, time.Second} {
		t.Run(short.String(), func(t *testing.T) {
			choices := &shortestTerminationChooser{}
			s := qenv.New(choices)
			s.Check = true
			defer s.Close()
			p := s.Process("worker", false, func(ctx context.Context) {
				qenv.Daemon(ctx)
				qenv.Wait(ctx, func() bool { return false })
			})
			const grace = 10 * time.Second
			s.Terminations(qenv.Termination{Grace: grace, Short: short}, p)
			var replaced bool
			var at time.Duration
			s.Spawn("operator", func(ctx context.Context) {
				p.Terminate()
				qenv.Wait(ctx, func() bool { return p.Incarnation == 2 })
				replaced, at = true, s.Elapsed()
			})
			if err := s.Run(2 * grace); err != nil {
				t.Fatal(err)
			}
			want, offered := grace, 0
			if short != 0 {
				want, offered = short, 1
			}
			if !replaced || at != want || choices.shortChoices != offered {
				t.Fatalf("replaced=%v at %v, short choices=%d; want replacement at %v, short choices=%d", replaced, at, choices.shortChoices, want, offered)
			}
		})
	}
}
