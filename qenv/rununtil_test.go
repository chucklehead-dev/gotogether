package qenv_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qenv"
)

func TestRunUntilStopsAtActualCompletionWithoutDraining(t *testing.T) {
	calls := &hooks.Simulations{}
	s := qenv.New(qenv.WithContext(qenv.Nominal{}, hooks.WithSimulations(context.Background(), calls)))
	defer s.Close()
	done, later := false, false
	s.Spawn("prerequisite", func(ctx context.Context) { qenv.Sleep(ctx, 10*time.Millisecond); done = true })
	s.Spawn("later", func(ctx context.Context) { qenv.Sleep(ctx, 20*time.Millisecond); later = true })
	reached, err := s.RunUntil(time.Minute, func() bool { return done })
	if err != nil || !reached || later || s.Now().Sub(qenv.Epoch) != 10*time.Millisecond {
		t.Fatalf("completion reached=%t err=%v later=%t time=%v", reached, err, later, s.Now())
	}
	observed := calls.Calls()
	if len(observed) != 1 || observed[0].Operation != "until" || observed[0].Horizon != time.Minute {
		t.Fatalf("lost actual bounded invocation: %+v", observed)
	}
	if err := s.Run(time.Second); err != nil || !later {
		t.Fatalf("later work was dropped: %v %t", err, later)
	}
}

func TestRunUntilBoundAndSchedulerErrorsStayDistinct(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	done := false
	s.Spawn("slow", func(ctx context.Context) { qenv.Sleep(ctx, 2*time.Second); done = true })
	if reached, err := s.RunUntil(time.Second, func() bool { return done }); err != nil || reached || s.Now().Sub(qenv.Epoch) != time.Second {
		t.Fatalf("bound reached=%t err=%v time=%v", reached, err, s.Now())
	}
	if reached, err := s.RunUntil(time.Second, func() bool { return done }); err != nil || !reached {
		t.Fatalf("exact deadline completion reached=%t err=%v", reached, err)
	}
	busy := qenv.New(qenv.Nominal{})
	defer busy.Close()
	busy.MaxSteps = 3
	busy.Spawn("spin", func(ctx context.Context) {
		for {
			qenv.Yield(ctx)
		}
	})
	if reached, err := busy.RunUntil(time.Second, func() bool { return false }); !errors.Is(err, qenv.ErrMaxSteps) || reached {
		t.Fatalf("step limit became timeout/success: %t %v", reached, err)
	}
}

func TestRunUntilRejectsUnsupportedRuntimeAndInvalidBound(t *testing.T) {
	s := qenv.NewNative(qenv.Nominal{}, nil)
	defer s.Close()
	if _, err := s.RunUntil(time.Second, func() bool { return true }); err == nil {
		t.Fatal("native runtime claimed deterministic completion")
	}
	d := qenv.New(qenv.Nominal{})
	defer d.Close()
	if _, err := d.RunUntil(time.Second, nil); err == nil {
		t.Fatal("missing predicate accepted")
	}
	if _, err := d.RunUntil(-time.Second, func() bool { return true }); err == nil {
		t.Fatal("negative bound accepted")
	}
}

func TestPauseFaultsSuppressesScriptsWithoutHealing(t *testing.T) {
	script := &qenv.Script{Faults: []qenv.Scripted{{Kind: "nemesis.partition.a.b"}}}
	s := qenv.New(script)
	defer s.Close()
	a := s.Process("a", false, func(context.Context) {})
	b := s.Process("b", false, func(context.Context) {})
	s.Net().PartitionNemesis(a, b)
	done := false
	s.Spawn("first", func(ctx context.Context) { qenv.Sleep(ctx, time.Millisecond); done = true })
	s.PauseFaults()
	if reached, err := s.RunUntil(time.Second, func() bool { return done }); err != nil || !reached {
		t.Fatal(reached, err)
	}
	if len(script.Remaining()) != 1 || s.Net().Partitioned(a, b) {
		t.Fatal("paused scripted fault executed")
	}
	s.Net().Partition(a, b)
	s.PauseFaults()
	if !s.Net().Partitioned(a, b) {
		t.Fatal("pausing faults healed an existing partition")
	}
	s.Net().Heal(a, b)
	s.ResumeFaults()
	done = false
	s.Spawn("second", func(ctx context.Context) { qenv.Sleep(ctx, time.Millisecond); done = true })
	if reached, err := s.RunUntil(time.Second, func() bool { return done }); err != nil || !reached {
		t.Fatal(reached, err)
	}
	if len(script.Remaining()) != 0 || !s.Net().Partitioned(a, b) {
		t.Fatal("resume did not restore scripted faults")
	}
}
