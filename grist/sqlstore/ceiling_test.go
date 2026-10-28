package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

// ceiling is what appendCeiling measured: the appends committed, the
// tries that lost the head lock's race or a condition (and were tried
// again), and the time they took.
type ceiling struct {
	appends, retries int64
	took             time.Duration
}

func (c ceiling) rate() float64 { return float64(c.appends) / c.took.Seconds() }

// appendCeiling runs writers goroutines appending to tenants (writer i to
// tenants[i%len]) for d, each append as a command's: one event, a
// condition on the writer's own tag after the head it read (which no
// other writer's event matches: no condition fails for the data), a
// marker and a step row. Every append in a tenant takes its grist_heads
// row's lock (UPDATE ... RETURNING): positions dense, in commit order, so
// one tenant's appends serialize on it whatever the writers.
func appendCeiling(ctx context.Context, s *sqlstore.Store, tenants []string, writers int, d time.Duration) (ceiling, error) {
	for _, t := range tenants {
		if err := s.CreateTenant(ctx, t); err != nil {
			return ceiling{}, err
		}
	}
	var c ceiling
	var wg sync.WaitGroup
	var first atomic.Value
	start := time.Now() //qenv:ok a benchmark of a real database, outside any simulation
	deadline := start.Add(d)
	for w := range writers {
		wg.Add(1)
		go func() { //qenv:ok the writers of a real database's benchmark
			defer wg.Done()
			t := tenants[w%len(tenants)]
			tag := store.TagPair{Key: "writer", Value: fmt.Sprint(w)}
			for i := 0; time.Now().Before(deadline); i++ { //qenv:ok the benchmark's deadline
				for {
					_, head, err := s.Read(ctx, t, store.Selector{}, 0)
					if err == nil {
						_, err = s.Append(ctx, t, store.Batch{Events: []store.Event{{Type: "a", Version: 1, Body: []byte(`{}`), Tags: []store.TagPair{tag}}},
							Condition: &store.Condition{After: head, Selectors: []store.Selector{{Types: []string{"a"}, Tags: []store.TagPair{tag}}}},
							Marker:    &store.Marker{ID: fmt.Sprintf("w%d-%d", w, i), Hash: "h"}, Step: []byte(`{"v":1,"steps":[]}`)})
					}
					var cf *store.ConditionFailed
					if err == nil {
						atomic.AddInt64(&c.appends, 1)
						break
					}
					if store.Transient(err) || errors.As(err, &cf) && cf.Marker == "" {
						atomic.AddInt64(&c.retries, 1)
						continue
					}
					first.CompareAndSwap(nil, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	c.took = time.Since(start) //qenv:ok the benchmark's time
	if err, _ := first.Load().(error); err != nil {
		return c, err
	}
	// the serialization: every tenant's positions dense, one per append
	heads, err := s.Heads(ctx)
	if err != nil {
		return c, err
	}
	var sum int64
	for _, t := range tenants {
		sum += int64(heads[t])
	}
	if sum != c.appends {
		return c, fmt.Errorf("%d appends committed and the heads sum to %d: positions are not one per append", c.appends, sum)
	}
	return c, nil
}

// ceilingTiers are the stores the ceiling is measured on: SQLite (a temp
// file), and PostgreSQL when GRIST_POSTGRES names one (a fresh schema).
func ceilingTiers(tb testing.TB) map[string]func(tb testing.TB) *sqlstore.Store {
	tiers := map[string]func(tb testing.TB) *sqlstore.Store{"sqlite": func(tb testing.TB) *sqlstore.Store {
		db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(filepath.Join(tb.TempDir(), "grist.db"), 5*time.Second))
		if err != nil {
			tb.Fatal(err)
		}
		tb.Cleanup(func() { db.Close() })
		s, err := sqlstore.Open(context.Background(), db, sqlstore.SQLite, sqlstore.Options{})
		if err != nil {
			tb.Fatal(err)
		}
		return s
	}}
	if dsn := os.Getenv("GRIST_POSTGRES"); dsn != "" {
		tiers["postgres"] = func(tb testing.TB) *sqlstore.Store {
			ctx := context.Background()
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				tb.Fatal(err)
			}
			schema := fmt.Sprintf("grist_ceiling_%d", time.Now().UnixNano()) //qenv:ok a scratch schema's unique name, outside any simulation
			if _, err := db.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
				tb.Fatal(err)
			}
			tb.Cleanup(func() { db.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`); db.Close() })
			sdb, err := sql.Open("pgx", dsn+" search_path="+schema)
			if err != nil {
				tb.Fatal(err)
			}
			sdb.SetMaxOpenConns(64)
			tb.Cleanup(func() { sdb.Close() })
			s, err := sqlstore.Open(ctx, sdb, sqlstore.Postgres, sqlstore.Options{})
			if err != nil {
				tb.Fatal(err)
			}
			return s
		}
	}
	return tiers
}

// tenantsN are n tenants' names.
func tenantsN(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("t%d", i)
	}
	return out
}

// BenchmarkAppendCeiling measures a tenant's append ceiling: commands'
// appends per second into one tenant from 1, 8 and 32 writers, beside 8
// writers each on a tenant of its own, on SQLite and (GRIST_POSTGRES) on
// PostgreSQL. One tenant's appends serialize on its grist_heads row, so
// its rate stops growing with writers at about one commit's lock hold
// (the round trips between UPDATE ... RETURNING and COMMIT); tenants of
// their own scale until the database does (docs/grist.md §2.10 has the
// numbers). appends/s is the rate, retries/append the lost races.
func BenchmarkAppendCeiling(b *testing.B) {
	ctx := context.Background()
	for name, open := range ceilingTiers(b) {
		for _, shape := range []struct {
			name             string
			tenants, writers int
		}{{"oneTenant/w=1", 1, 1}, {"oneTenant/w=8", 1, 8}, {"oneTenant/w=32", 1, 32}, {"ownTenants/w=8", 8, 8}} {
			b.Run(name+"/"+shape.name, func(b *testing.B) {
				var c ceiling
				for b.Loop() {
					var err error
					if c, err = appendCeiling(ctx, open(b), tenantsN(shape.tenants), shape.writers, 2*time.Second); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(c.rate(), "appends/s")
				b.ReportMetric(float64(c.retries)/float64(max(c.appends, 1)), "retries/append")
			})
		}
	}
}

// The ceiling's harness measures what it says: eight writers on one
// tenant commit every append once, at dense positions, through the head
// lock's races, and eight on tenants of their own likewise; each rate is
// positive. The numbers are the benchmark's.
func TestAppendCeiling(t *testing.T) {
	ctx := context.Background()
	for name, open := range ceilingTiers(t) {
		for _, tenants := range []int{1, 8} {
			c, err := appendCeiling(ctx, open(t), tenantsN(tenants), 8, 300*time.Millisecond)
			if err != nil || c.appends == 0 {
				t.Errorf("%s, 8 writers on %d tenants: %d appends, %v", name, tenants, c.appends, err)
			}
			t.Logf("%s, 8 writers on %d tenants: %.0f appends/s, %d retries", name, tenants, c.rate(), c.retries)
		}
	}
}
