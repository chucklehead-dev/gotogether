package qenv_test

import (
	"context"
	"encoding/hex"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Restoring stored IDs must not restore the allocator: the retained event keeps
// its ID while replacement work and restarted processes get fresh identities.
func TestNewIDSurvivesStoreRestoreAndProcessRestart(t *testing.T) {
	run := func(c qenv.Chooser) []string {
		s := qenv.New(c)
		defer s.Close()
		st := qenv.NewStore(s, "ids", qenv.Serializable)
		var ids []string
		p := s.Process("writer", false, func(ctx context.Context) {
			// A library using Background must remain in its caller's simulation.
			ids = append(ids, qenv.NewID(context.Background()))
		})
		s.Spawn("restore", func(ctx context.Context) {
			qenv.Wait(ctx, func() bool { return len(ids) == 1 })
			retained := ids[0]
			if err := put(ctx, st, "event", retained); err != nil {
				t.Error(err)
				return
			}
			st.Backup()
			for generation := 0; generation < 2; generation++ {
				ids = append(ids, qenv.NewID(ctx))
				if err := put(ctx, st, "event", ids[len(ids)-1]); err != nil {
					t.Error(err)
					return
				}
				if !st.Restore() {
					t.Error("expected a real store restore")
					return
				}
				if got, ok := st.Read("event"); !ok || got != retained {
					t.Errorf("restore changed retained ID: %v %v", got, ok)
				}
				before := len(ids)
				p.Crash()
				p.Restart()
				qenv.Wait(ctx, func() bool { return len(ids) == before+1 })
			}
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		if st.Restored() != 2 || p.Incarnation != 3 || len(ids) != 5 {
			t.Fatalf("fixture did not restore and restart twice: restores=%d incarnation=%d ids=%v", st.Restored(), p.Incarnation, ids)
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("restore or restart reused ID %q", id)
			}
			seen[id] = true
		}
		return ids
	}
	want := run(qenv.Nominal{})
	for _, chooser := range []qenv.Chooser{qenv.Nominal{}, &qenv.Script{}, qenv.NewSeeded(1), qenv.NewSeeded(2)} {
		if got := run(chooser); !reflect.DeepEqual(got, want) {
			t.Errorf("ID source depends on chooser entropy or previous simulations: got %v, want %v", got, want)
		}
	}
}

func TestNewIDOutsideSimulation(t *testing.T) {
	seen := map[string]bool{}
	for range 32 {
		id := qenv.NewID(context.Background())
		b, err := hex.DecodeString(id)
		if err != nil || len(b) != 16 || id != strings.ToLower(id) || seen[id] {
			t.Fatalf("not a fresh canonical 128-bit ID: %q (%v)", id, err)
		}
		seen[id] = true
	}
}

func TestNewIDDoesNotAlterChoices(t *testing.T) {
	run := func(allocate bool) []qenv.Choice {
		log := &qenv.ChoiceLog{C: qenv.NewSeeded(17)}
		s := qenv.New(log)
		defer s.Close()
		for _, name := range []string{"one", "two"} {
			s.Spawn(name, func(ctx context.Context) {
				for range 3 {
					if allocate {
						qenv.NewID(ctx)
					}
					qenv.Yield(ctx)
				}
			})
		}
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		return log.Choices
	}
	if plain, allocated := run(false), run(true); !reflect.DeepEqual(plain, allocated) {
		t.Fatalf("allocating opaque IDs changed scheduling/fault/entropy choices:\nplain: %v\nallocated: %v", plain, allocated)
	}
}

// A native caller may share an explicit task context among concurrent calls.
// Allocation must still be unique without making the counter a scheduling seam.
func TestNewIDConcurrentContext(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var taskContext context.Context
	s.Spawn("context", func(ctx context.Context) { taskContext = ctx })
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	const n = 64
	ids := make(chan string, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { ids <- qenv.NewID(taskContext) })
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("concurrent allocation reused %q", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatalf("allocated %d IDs, want %d", len(seen), n)
	}
}
