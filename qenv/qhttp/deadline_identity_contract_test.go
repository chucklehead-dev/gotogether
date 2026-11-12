package qhttp_test

import (
	"context"
	"errors"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
)

func TestDistinctDeadlineInstantsDoNotAlias(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	s.Check = true
	defer s.Close()
	calls, completed := 0, false
	srv, tr, client := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		qenv.Sleep(r.Context(), 3*time.Second)
		w.WriteHeader(http.StatusNoContent)
	}))
	tr.Timeout = 2 * time.Second
	s.Spawn("client", func(ctx context.Context) {
		nearContext, cancelNear := qhttp.WithTimeout(ctx, time.Second)
		defer cancelNear()
		near, _ := nearContext.Deadline()
		// Two largest durations plus 2ns is exactly 2^64ns. Each Add is
		// representable, but UnixNano cannot distinguish the two instants.
		far := near.Add(time.Duration(math.MaxInt64)).Add(time.Duration(math.MaxInt64)).Add(2 * time.Nanosecond)
		if near.Equal(far) || near.UnixNano() != far.UnixNano() {
			t.Error("fixture did not construct distinct deadline instants with equal UnixNano")
			return
		}
		farContext, cancelFar := context.WithDeadline(ctx, far)
		defer cancelFar()
		checkDeadlineAttempt(t, s, client, srv.URL(), nearContext, time.Second, true)
		checkDeadlineAttempt(t, s, client, srv.URL(), farContext, 2*time.Second, false)
		completed = true
	})
	if err := s.Run(6 * time.Second); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Error("client did not complete all deadline attempts within the simulation horizon")
	}
	if calls != 2 {
		t.Errorf("handler received %d requests, want both independently bounded requests", calls)
	}
}

func checkDeadlineAttempt(t *testing.T, s *qenv.Sim, client *http.Client, url string, ctx context.Context, after time.Duration, deadline bool) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Error(err)
		return
	}
	start := s.Elapsed()
	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
		t.Errorf("received response %d instead of expected timeout", resp.StatusCode)
	}
	te, ok := errors.AsType[*qhttp.TimeoutError](err)
	if !ok {
		t.Errorf("request error=%v, want TimeoutError", err)
		return
	}
	if elapsed := s.Elapsed() - start; te.After != after || elapsed != after || te.Deadline != deadline {
		t.Errorf("timeout after=%v elapsed=%v deadline=%v; want %v/%v/%v", te.After, elapsed, te.Deadline, after, after, deadline)
	}
}

func TestSameDeadlineInstantSharesConsumedBudget(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	s.Check = true
	defer s.Close()
	calls, completed := 0, false
	srv, tr, client := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		qenv.Sleep(r.Context(), 3*time.Second)
		w.WriteHeader(http.StatusNoContent)
	}))
	tr.Timeout = 500 * time.Millisecond
	s.Spawn("client", func(ctx context.Context) {
		exact, cancelExact := qhttp.WithTimeout(ctx, time.Second)
		defer cancelExact()
		deadline, _ := exact.Deadline()
		zoned := deadline.In(time.FixedZone("deadline-test", 3600))
		if !deadline.Equal(zoned) {
			t.Error("fixture did not preserve the deadline instant across zones")
			return
		}
		otherZone, cancelZone := context.WithDeadline(ctx, zoned)
		defer cancelZone()
		qenv.Sleep(ctx, 250*time.Millisecond)
		checkDeadlineAttempt(t, s, client, srv.URL(), exact, 500*time.Millisecond, false)
		tr.Timeout = 2 * time.Second
		checkDeadlineAttempt(t, s, client, srv.URL(), otherZone, 250*time.Millisecond, true)
		checkDeadlineAttempt(t, s, client, srv.URL(), exact, 0, true)
		completed = true
	})
	if err := s.Run(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Error("client did not complete all deadline attempts within the simulation horizon")
	}
	if calls != 2 {
		t.Errorf("handler received %d requests, want two before the shared deadline expired", calls)
	}
}
