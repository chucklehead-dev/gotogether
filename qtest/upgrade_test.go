package qtest

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// encodings is a mixed-version bug: v2 writes a register's value in hex
// ("0xa") and reads either encoding, v1 writes and reads decimal. Every
// node on one version conforms; a v1 reader after a v2 writer reads 0.
func encodings(rolling bool) func(c qenv.Chooser) error {
	return func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		stored := "0" // the shared register, durable across restarts
		writer := func(hex bool) func(ctx context.Context) {
			return func(ctx context.Context) {
				for v := int64(1); v <= 3; v++ {
					if qenv.ProcessOf(ctx).Incarnation > 1 && v == 1 {
						continue // restarted: its first write was done
					}
					if hex {
						stored = "0x" + strconv.FormatInt(v, 16)
					} else {
						stored = strconv.FormatInt(v, 10)
					}
					qobs.Record(ctx, "write", nil, "key", "a", "value", v)
					qenv.Sleep(ctx, time.Second)
				}
			}
		}
		reader := func(any bool) func(ctx context.Context) {
			return func(ctx context.Context) {
				qenv.Sleep(ctx, 500*time.Millisecond)
				for range 3 {
					base := 10
					if any {
						base = 0
					}
					v, err := strconv.ParseInt(stored, base, 64)
					if err != nil {
						v = 0 // a v1 reader of a v2 value
					}
					qobs.Record(ctx, "read", nil, "key", "a", "obs.value", v)
					qenv.Sleep(ctx, time.Second)
				}
			}
		}
		w := s.Process("writer", false, writer(false))
		r := s.Process("reader", false, reader(false))
		if rolling {
			s.Upgrades("v2", writer(true), w)
			s.Upgrades("v2", reader(true), r)
		} else {
			// the ablation: an "upgrade" that restarts the old code
			for _, p := range []*qenv.Process{w, r} {
				s.Nemesis("upgrade."+p.Name, func() bool { return p.Incarnation == 1 }, func() { p.Crash(); p.Restart() })
			}
		}
		return s.Run(time.Minute)
	}
}

// A rolling upgrade's mixed window: within one fault Exhaust upgrades the
// writer alone at every point, and a v1 reader then misreads its value.
// Restarting the old code there finds nothing: the bug is the versions'.
func TestUpgradeMixedVersions(t *testing.T) {
	RequireQuint(t)
	sys := System{Binding: "testdata/registers.yaml", Rates: qenv.NoFaults, Run: encodings(true)}
	sys.Exhaust(t, Bound{Faults: 1}).Expect(t, Some(Mismatch("read")))
	sys.Run = encodings(false)
	sys.Exhaust(t, Bound{Faults: 1}).Expect(t, Conforms())
}
