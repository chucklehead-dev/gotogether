package grist_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

// downStore fails every processor's append with a transient error once
// down is set, counting them; commands commit.
type downStore struct {
	*memstore.Store
	down    atomic.Bool
	appends atomic.Int64
}

func (s *downStore) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	if s.down.Load() && strings.HasPrefix(b.Cause, "processor:") {
		s.appends.Add(1)
		return 0, errors.New("the store is down") // transient (store.Transient)
	}
	return s.Store.Append(ctx, t, b)
}

// A node stopped while its poller retries a transiently failing commit
// stops retrying: the retry loop ends with its context. It once spun on
// (its backoff returns at once on an ended context, and the next append
// failed alike), taking a core until the process ended: 88% of counters'
// test CPU, after its real tier killed a node whose store then refused
// every append.
func TestPollerStopsWithItsNode(t *testing.T) {
	a, bank, pay := callsApp(grist.AtLeastOnce, true)
	st := &downStore{Store: memstore.New(a.Name)}
	if err := st.CreateTenant(context.Background(), "t1"); err != nil {
		t.Fatal(err)
	}
	st.down.Store(true)
	ctx, stop := context.WithCancel(context.Background())
	if err := a.Start(bank.With(ctx, grist.Local(bankHandlers(pay))), st); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(addAmounts{A: 5})
	if _, err := a.Execute(ctx, st, grist.Envelope{ID: "c1", Tenant: "t1", Actor: "u"}, "add", body); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); st.appends.Load() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the poller never tried to commit within 10 s")
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	time.Sleep(100 * time.Millisecond) // a retry in flight ends
	before := st.appends.Load()
	time.Sleep(200 * time.Millisecond)
	if n := st.appends.Load() - before; n > 0 {
		t.Errorf("%d appends tried in 200 ms after the node stopped: want none (the poller's retries end with its context)", n)
	}
}
