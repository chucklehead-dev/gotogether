package qenv_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Reset leaves the last reads available until a locking or commit operation
// reports ErrUnavailable. That refusal ends the transaction, so later data
// operations must not silently operate on a handle that has already failed.
func TestResetRefusalEndsStoreTransaction(t *testing.T) {
	for _, reset := range []string{"failover", "restore"} {
		for _, operation := range []string{"lock", "commit"} {
			t.Run(reset+"/"+operation, func(t *testing.T) {
				s := qenv.New(qenv.Nominal{})
				s.Check = true
				defer s.Close()
				st := qenv.NewStore(s, "db", qenv.Serializable)
				st.Load("key", 1)
				st.Backup()
				s.Spawn("client", func(ctx context.Context) {
					tx := mustBegin(t, st, ctx)
					defer tx.Rollback()
					tx.Put("key", 2)
					if reset == "restore" {
						if !st.Restore() {
							t.Error("restore did not find its backup")
						}
					} else {
						st.FailOver(0, 0)
					}
					if value, ok := tx.Get("key"); !ok || value != 2 {
						t.Errorf("reset read before refusal: %v, %v; want the staged last answer 2", value, ok)
					}
					var err error
					if operation == "lock" {
						_, _, err = tx.GetForUpdate("key")
					} else {
						err = tx.Commit()
					}
					if !errors.Is(err, qenv.ErrUnavailable) {
						t.Errorf("reset %s: %v; want ErrUnavailable", operation, err)
					}
					for _, next := range []struct {
						name string
						call func()
					}{
						{"Get", func() { tx.Get("key") }},
						{"Put", func() { tx.Put("unexpected", 3) }},
					} {
						if message := panicOf(next.call); !strings.Contains(message, "transaction used after it ended") {
							t.Errorf("%s after ErrUnavailable: panic=%q; want use-after-end refusal", next.name, message)
						}
					}
					fresh := mustBegin(t, st, ctx)
					defer fresh.Rollback()
					fresh.Put("healthy", 4)
					if value, ok := fresh.Get("healthy"); !ok || value != 4 {
						t.Errorf("fresh transaction was unusable: %v, %v", value, ok)
					}
					if err := fresh.Commit(); err != nil {
						t.Errorf("fresh commit: %v", err)
					}
				})
				runSim(t, s)
				if value, _ := st.Read("key"); value != 1 {
					t.Errorf("failed transaction applied its write: %v; want 1", value)
				}
				if _, ok := st.Read("unexpected"); ok {
					t.Error("use-after-end write was applied")
				}
				if value, ok := st.Read("healthy"); !ok || value != 4 {
					t.Errorf("fresh transaction did not commit: %v, %v", value, ok)
				}
			})
		}
	}
}
