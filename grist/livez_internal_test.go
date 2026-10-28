package grist

import (
	"context"
	"testing"
	"time"
)

// A poller backing off from a store error is waiting on the store, not
// stuck: its backoff restarts /livez's clock, so a commit retried through
// a database outage longer than LiveBound does not restart the pod
// (liveness never the store).
func TestPauseIsProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the backoff returns at once
	pl := &poller{p: &processorInfo{name: "note"}, tenant: "t1"}
	long := time.Now().Add(-time.Hour).UnixNano()
	pl.busy.Store(long)
	n := &node{list: []*poller{pl}}
	if n.stuck(time.Now().UnixNano(), time.Minute) == "" {
		t.Fatal("a poller holding a batch for an hour is not stuck")
	}
	pl.pause(ctx)
	if why := n.stuck(time.Now().UnixNano(), time.Minute); why != "" {
		t.Errorf("after a backoff from a store error: %s, want not stuck", why)
	}
	idle := &poller{}
	idle.pause(ctx)
	if idle.busy.Load() != 0 {
		t.Error("a backoff outside a batch marked the poller busy")
	}
}
