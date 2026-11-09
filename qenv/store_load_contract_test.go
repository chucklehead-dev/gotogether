package qenv_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A client waiting on Changed must see fixture or migration writes made by
// another client through Load, including replacement of an existing key.
func TestStoreLoadNotifiesWaitingClient(t *testing.T) {
	t.Parallel()
	for _, existing := range []bool{false, true} {
		name := "new key"
		if existing {
			name = "existing key"
		}
		t.Run(name, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			defer s.Close()
			st := qenv.NewStore(s, "db", qenv.Serializable)
			if existing {
				st.Load("key", "before")
			}
			before := st.Version()
			ready, woke := false, false
			s.Spawn("client", func(ctx context.Context) {
				ready = true
				woke = st.Changed().Wait(ctx, time.Second)
				if !woke {
					t.Error("Load did not notify waiting client")
				}
				if v, ok := st.Read("key"); !ok || v != "loaded" {
					t.Errorf("notified client read %v, %v; want loaded, true", v, ok)
				}
				if st.Version() <= before {
					t.Error("Load did not advance Version")
				}
			})
			s.Spawn("migration", func(ctx context.Context) {
				qenv.Wait(ctx, func() bool { return ready })
				// Let the client enter its wait before applying the migration.
				qenv.Sleep(ctx, time.Millisecond)
				st.Load("key", "loaded")
			})
			if err := s.Run(2 * time.Second); err != nil {
				t.Fatal(err)
			}
			if !woke {
				t.Fatal("waiting client did not observe notification")
			}
		})
	}
}
