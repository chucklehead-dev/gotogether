package shop_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/checkout/shop"
	"github.com/chucklehead-dev/gotogether/qenv"
)

type relayDB struct {
	qenv.DB
	refused int
}

func (d *relayDB) Begin(ctx context.Context) (qenv.Txn, error) {
	tx, err := d.DB.Begin(ctx)
	if errors.Is(err, qenv.ErrTransactionCapacity) {
		d.refused++
	}
	return tx, err
}

type relayBroker struct{ events []shop.Event }

func (b *relayBroker) Publish(_ context.Context, ev shop.Event) { b.events = append(b.events, ev) }

// Admission refusal does not make the relay mistake an unread outbox for an
// empty one: it retries after the holder releases its transaction.
func TestRelayRetriesRefusedBegin(t *testing.T) {
	sim := qenv.New(qenv.Nominal{})
	defer sim.Close()
	st := qenv.NewStore(sim, "db", qenv.Serializable)
	st.MaxTransactions = 1
	st.Load("outbox/one", shop.Event{Type: "placed", Order: "one"})
	db := &relayDB{DB: st}
	broker := &relayBroker{}
	s := &shop.Shop{DB: db, Broker: broker, PollInterval: time.Millisecond}
	sim.Spawn("holder", func(ctx context.Context) {
		tx, err := st.Begin(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		qenv.Sleep(ctx, 2*time.Millisecond)
	})
	sim.Spawn("relay", s.RunRelay)
	sim.Spawn("observer", func(ctx context.Context) {
		qenv.Wait(ctx, func() bool { return len(broker.events) == 1 && len(st.Keys("outbox/")) == 0 })
	})
	if err := sim.Run(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if db.refused == 0 || len(broker.events) != 1 || broker.events[0].Order != "one" || len(st.Keys("outbox/")) != 0 {
		t.Fatalf("relay recovery: refusals%d published%v remaining%v", db.refused, broker.events, st.Keys("outbox/"))
	}
}
