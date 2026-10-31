package sqlstore

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// A hint arriving while the authoritative read is in progress must force a
// second read, rather than sleeping until the polling backstop.
func TestNativeHintDuringRead(t *testing.T) {
	s := &Store{o: Options{Poll: time.Hour}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reads := 0
	err := s.wait(ctx, func() (bool, error) {
		reads++
		if reads == 1 {
			s.NotifyExternal()
			return false, nil
		}
		return true, nil
	})
	if err != nil || reads != 2 {
		t.Fatalf("wait = %v, reads = %d; want nil, 2", err, reads)
	}
}

func TestNativeHintsBroadcastAndReread(t *testing.T) {
	s := &Store{o: Options{Poll: time.Hour}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	const count = 8
	reads := make(chan struct{}, count*16)
	results := make(chan error, count)
	var ready atomic.Bool
	for range count {
		go func() { //qenv:ok tests native goroutine broadcasts outside simulation
			results <- s.wait(ctx, func() (bool, error) { reads <- struct{}{}; return ready.Load(), nil })
		}()
	}
	for range count {
		select {
		case <-reads:
		case <-ctx.Done():
			t.Fatal("initial reads:", ctx.Err())
		}
	}
	// Every waiter must reread, but a hint alone cannot report progress.
	s.NotifyExternal()
	for range count {
		select {
		case <-reads:
		case <-ctx.Done():
			t.Fatal("hint did not broadcast:", ctx.Err())
		}
	}
	select {
	case err := <-results:
		t.Fatalf("spurious hint completed wait: %v", err)
	default:
	}
	// Duplicate hints are harmless, and a later real change reaches everyone.
	s.NotifyExternal()
	s.NotifyExternal()
	ready.Store(true)
	s.NotifyExternal()
	for range count {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("progress:", ctx.Err())
		}
	}
}

func TestNativeHintDoesNotInventProgress(t *testing.T) {
	s := &Store{o: Options{Poll: time.Hour}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	reads := make(chan struct{}, 1)
	result := make(chan error, 1)
	go func() { //qenv:ok native cancellation and notification contract
		result <- s.wait(ctx, func() (bool, error) {
			select {
			case reads <- struct{}{}:
			default:
			}
			return false, nil
		})
	}()
	<-reads
	s.NotifyExternal()
	<-reads
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("wait = %v, want cancellation", err)
	}
}

// Registration precedes the final generation check. A notification after that
// check must still close the captured ticket; replacing it with a fresh wait
// registration would lose the hint.
func TestNativeTicketPreservesHintAfterGenerationCheck(t *testing.T) {
	s := &Store{}
	ticket := s.wakeTicket()
	seen := s.version()
	if s.version() != seen {
		t.Fatal("unexpected generation change")
	}
	s.NotifyExternal()
	select {
	case <-ticket:
	default:
		t.Fatal("hint after generation check was lost")
	}
	next := s.wakeTicket()
	select {
	case <-next:
		t.Fatal("old hint leaked into next ticket")
	default:
	}
}

func TestNativePollingBackstopWithoutHint(t *testing.T) {
	s := &Store{o: Options{Poll: time.Millisecond}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	read := make(chan struct{}, 1)
	var progressed atomic.Bool
	result := make(chan error, 1)
	go func() { //qenv:ok native polling recovery without notification
		result <- s.wait(ctx, func() (bool, error) {
			ok := progressed.Load()
			select {
			case read <- struct{}{}:
			default:
			}
			return ok, nil
		})
	}()
	select {
	case <-read:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	progressed.Store(true)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
