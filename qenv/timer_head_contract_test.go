package qenv_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// The old deadline of an early-notified wait must not wake a later wait
// or block a later Run. The later real deadline and independent work retain
// their own times across bounded Run calls, followed by quiet completion.
func TestStaleTimerHeadAcrossRunHorizons(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	s.Check = true
	defer s.Close()
	var first, second qenv.Signal
	firstFinished, guardFinished, secondFinished, tailFinished := false, false, false, false
	firstNotified, secondNotified := false, true
	var firstAt, guardAt, secondAt, tailAt time.Duration
	s.Spawn("waiter", func(ctx context.Context) {
		firstNotified = first.Wait(ctx, 10*time.Millisecond)
		firstAt = s.Elapsed()
		firstFinished = true
		secondNotified = second.Wait(ctx, 20*time.Millisecond)
		secondAt = s.Elapsed()
		secondFinished = true
	})
	s.Spawn("notifier", func(ctx context.Context) { qenv.Sleep(ctx, time.Millisecond); first.Notify() })
	// This earlier live timer keeps the old first-wait deadline below the
	// heap head until the waiter has started its second generation.
	s.Spawn("intervening deadline", func(ctx context.Context) {
		qenv.Sleep(ctx, 5*time.Millisecond)
		guardAt = s.Elapsed()
		guardFinished = true
	})
	s.Spawn("independent deadline", func(ctx context.Context) {
		qenv.Sleep(ctx, 30*time.Millisecond)
		tailAt = s.Elapsed()
		tailFinished = true
	})
	if err := s.Run(15 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !firstFinished || !firstNotified || firstAt != time.Millisecond || !guardFinished || guardAt != 5*time.Millisecond || secondFinished || tailFinished || s.Elapsed() != 15*time.Millisecond {
		t.Fatalf("first horizon: first=%t/%t/%v guard=%t/%v second=%t tail=%t time=%v; want first at1ms, guard at5ms, horizon15ms", firstFinished, firstNotified, firstAt, guardFinished, guardAt, secondFinished, tailFinished, s.Elapsed())
	}
	if err := s.Run(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !secondFinished || secondNotified || secondAt != 21*time.Millisecond || tailFinished || s.Elapsed() != 25*time.Millisecond {
		t.Fatalf("second horizon: second=%t/%t/%v tail=%t time=%v; want timeout21ms only, horizon25ms", secondFinished, secondNotified, secondAt, tailFinished, s.Elapsed())
	}
	if err := s.Run(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !tailFinished || tailAt != 30*time.Millisecond || s.Elapsed() != 30*time.Millisecond {
		t.Fatalf("independent deadline finished=%t at%v, quiet time=%v; want30ms", tailFinished, tailAt, s.Elapsed())
	}
	if err := s.Run(time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if s.Elapsed() != 30*time.Millisecond {
		t.Fatalf("stale deadline moved quiet time to%v, want30ms", s.Elapsed())
	}
}
