package sqlstore_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// benchEvents is n events alternating types a and b, each tagged with
// one of 10 accounts and one of 3 kinds.
func benchEvents(n int) []store.Event {
	evs := make([]store.Event, n)
	for i := range evs {
		typ := "a"
		if i%2 == 1 {
			typ = "b"
		}
		evs[i] = store.Event{Type: typ, Version: 1, Body: []byte(`{"amount":` + strconv.Itoa(i) + `}`),
			Tags: []store.TagPair{{Key: "acct", Value: strconv.Itoa(i % 10)}, {Key: "kind", Value: strconv.Itoa(i % 3)}}}
	}
	return evs
}

// benchSelectors are the reads a read model makes: every event of two
// types (a rebuild), one type's, one account's, and a selector of two
// tags (whose second tag the index does not hold).
var benchSelectors = []struct {
	name string
	sel  store.Selector
}{
	{"all", store.Selector{Types: []string{"a", "b"}}},
	{"type", store.Selector{Types: []string{"a"}}},
	{"tag", store.Selector{Types: []string{"a", "b"}, Tags: []store.TagPair{{Key: "acct", Value: "1"}}}},
	{"twoTags", store.Selector{Types: []string{"a", "b"}, Tags: []store.TagPair{{Key: "acct", Value: "1"}, {Key: "kind", Value: "0"}}}},
	{"limit", store.Selector{Types: []string{"a", "b"}, Limit: 100}},
}

// BenchmarkRead is a Read of a tenant of 10^3 and 10^4 events, per
// selector, on SQLite (a temp file), on qsql (a simulation's Store) and
// on PostgreSQL when GRIST_POSTGRES names one; ns/event is the time per
// event returned.
func BenchmarkRead(b *testing.B) {
	type tier struct {
		name string
		open func(b *testing.B) *sqlstore.Store
	}
	tiers := []tier{{"sqlite", func(b *testing.B) *sqlstore.Store {
		db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(filepath.Join(b.TempDir(), "grist.db"), 5*time.Second))
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { db.Close() })
		s, err := sqlstore.Open(context.Background(), db, sqlstore.SQLite, sqlstore.Options{})
		if err != nil {
			b.Fatal(err)
		}
		return s
	}}}
	tiers = append(tiers, tier{"qsql", func(b *testing.B) *sqlstore.Store {
		// the simulation's creator runs the store's statements, a seam each
		sim := qenv.New(qenv.Nominal{})
		b.Cleanup(sim.Close)
		s, err := sqlstore.Open(context.Background(), qsql.Open(sim, qenv.NewStore(sim, "grist", qenv.ReadCommitted)), sqlstore.QSQL, sqlstore.Options{})
		if err != nil {
			b.Fatal(err)
		}
		return s
	}})
	if dsn := os.Getenv("GRIST_POSTGRES"); dsn != "" {
		tiers = append(tiers, tier{"postgres", func(b *testing.B) *sqlstore.Store {
			ctx := context.Background()
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				b.Fatal(err)
			}
			schema := fmt.Sprintf("grist_bench_%d", time.Now().UnixNano()) //qenv:ok a scratch schema's unique name, outside any simulation
			if _, err := db.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { db.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`); db.Close() })
			sdb, err := sql.Open("pgx", dsn+" search_path="+schema)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { sdb.Close() })
			s, err := sqlstore.Open(ctx, sdb, sqlstore.Postgres, sqlstore.Options{})
			if err != nil {
				b.Fatal(err)
			}
			return s
		}})
	}
	ctx := context.Background()
	for _, tr := range tiers {
		for _, n := range []int{1000, 10000} {
			b.Run(fmt.Sprintf("%s/n=%d", tr.name, n), func(b *testing.B) {
				s := tr.open(b)
				if err := s.CreateTenant(ctx, "t"); err != nil {
					b.Fatal(err)
				}
				evs := benchEvents(n)
				for i := 0; i < n; i += 500 {
					if _, err := s.Append(ctx, "t", store.Batch{Events: evs[i:min(i+500, n)]}); err != nil {
						b.Fatal(err)
					}
				}
				for _, q := range benchSelectors {
					b.Run(q.name, func(b *testing.B) {
						var got int
						for b.Loop() {
							out, _, err := s.Read(ctx, "t", q.sel, 0)
							if err != nil {
								b.Fatal(err)
							}
							got = len(out)
						}
						if got == 0 {
							b.Fatal("the read returned no events")
						}
						b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(got), "ns/event")
					})
				}
			})
		}
	}
}
