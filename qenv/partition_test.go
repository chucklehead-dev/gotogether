package qenv_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A call across a partition is lost, both ways, until it heals; a task of
// no process is never partitioned.
func TestPartition(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	echo := func(ctx context.Context, n int) (int, error) { return n, nil }
	server := s.Process("server", false, func(context.Context) {})
	svc := qenv.NewService(s, "echo", server, echo)
	var results []error
	var client *qenv.Process
	client = s.Process("client", false, func(ctx context.Context) {
		call := func() {
			_, err := svc.Call(ctx, 1, 100*time.Millisecond)
			results = append(results, err)
		}
		call()
		s.Partition(client, server)
		call()
		s.Heal(client, server)
		call()
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	var outside error
	s.Spawn("outsider", func(ctx context.Context) {
		s.Partition(client, server)
		_, outside = svc.Call(ctx, 1, 100*time.Millisecond)
		s.Heal(client, server)
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0] != nil || !errors.Is(results[1], qenv.ErrTimeout) || results[2] != nil {
		t.Errorf("before, during, after the partition: %v", results)
	}
	if outside != nil {
		t.Errorf("a task of no process was partitioned: %v", outside)
	}
}

// Partitions as nemeses cut and heal links at the nemesis rate, and Settle
// heals them all: a client that retries gets through once it settles.
func TestPartitionsNemesisAndSettle(t *testing.T) {
	cut := 0
	for seed := uint64(1); seed <= 20; seed++ {
		c := qenv.NewSeeded(seed)
		c.Rates = qenv.NoFaults.Set("nemesis", 0.2)
		s := qenv.New(c)
		server := s.Process("server", false, func(context.Context) {})
		svc := qenv.NewService(s, "echo", server, func(ctx context.Context, n int) (int, error) { return n, nil })
		failed, done := 0, false
		client := s.Process("client", false, func(ctx context.Context) {
			for i := 0; i < 50; i++ {
				if _, err := svc.Call(ctx, i, 10*time.Millisecond); err != nil {
					failed++
				}
			}
			for {
				if _, err := svc.Call(ctx, 0, 10*time.Millisecond); err == nil {
					done = true
					return
				}
			}
		})
		s.Partitions(client, server)
		if err := s.Run(2 * time.Second); err != nil {
			t.Fatal(err)
		}
		if err := s.Settle(time.Minute); err != nil {
			t.Fatal(err)
		}
		if !done {
			t.Fatalf("seed %d: the client never got through after Settle", seed)
		}
		if failed > 0 {
			cut++
		}
		s.Close()
	}
	if cut == 0 {
		t.Error("no run was ever partitioned")
	}
}
