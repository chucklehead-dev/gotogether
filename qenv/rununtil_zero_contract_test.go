package qenv_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

func TestRunUntilZeroExecutesOnlyCurrentTimeWork(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	s.Check = true
	defer s.Close()
	started, finished := false, false
	s.Spawn("worker", func(ctx context.Context) {
		started = true
		qenv.Sleep(ctx, time.Second)
		finished = true
	})
	if reached, err := s.RunUntil(0, func() bool { return started }); err != nil || !reached || finished || s.Elapsed() != 0 {
		t.Fatalf("initial completion: reached=%v error=%v finished=%v time=%v; want true/nil/false/0", reached, err, finished, s.Elapsed())
	}
	if reached, err := s.RunUntil(0, func() bool { return finished }); err != nil || reached || s.Elapsed() != 0 {
		t.Fatalf("future completion at zero bound: reached=%v error=%v time=%v; want false/nil/0", reached, err, s.Elapsed())
	}
	if reached, err := s.RunUntil(time.Second, func() bool { return finished }); err != nil || !reached || s.Elapsed() != time.Second {
		t.Fatalf("later completion: reached=%v error=%v time=%v; want true/nil/1s", reached, err, s.Elapsed())
	}
}
