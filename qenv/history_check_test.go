package qenv_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A store that keeps no level commits the anomalies its level forbids,
// and under Check the history says which: a lost update under snapshot
// isolation is G-single, write skew under serializability G2. The same
// runs of a working store pass (TestIsolationAnomalies, with
// GOTOGETHER_CHECK=1 in CI).
func TestHistoryCatchesBrokenLevel(t *testing.T) {
	plain := func(s *qenv.Sim, st *qenv.Store) { increments(s, st, false) }
	cases := []struct {
		iso   qenv.Isolation
		setup func(*qenv.Sim, *qenv.Store)
		want  string
	}{
		{qenv.SnapshotIsolation, plain, "G-single"},
		{qenv.Serializable, offCall, "G2"},
	}
	for _, c := range cases {
		caught := 0
		for seed := uint64(0); seed < 50; seed++ {
			msg := func() (msg string) {
				defer func() {
					if r := recover(); r != nil {
						msg = fmt.Sprint(r)
					}
				}()
				s := qenv.New(qenv.NewSeeded(seed))
				defer s.Close()
				s.Check = true
				st := qenv.NewStore(s, "db", c.iso)
				qenv.BreakValidation(st)
				st.Load("oncall/alice", true)
				st.Load("oncall/bob", true)
				c.setup(s, st)
				if err := s.Run(time.Minute); err != nil {
					t.Fatal(err)
				}
				return ""
			}()
			if msg == "" {
				continue
			}
			if !strings.Contains(msg, "committed a history its level forbids: "+c.want) {
				t.Fatalf("%s: %s", c.iso, msg)
			}
			caught++
		}
		if caught == 0 {
			t.Errorf("%s: the broken store's %s never caught in 50 runs", c.iso, c.want)
		}
	}
}
