package qenv_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

type entry struct{ Amount int }

// callLog is a Txn wrapper that logs every call it forwards, as a
// recording DB or an adapter sees them.
type callLog struct {
	qenv.Txn
	log *[]string
}

func (c callLog) Get(k string) (any, bool) {
	v, ok := c.Txn.Get(k)
	*c.log = append(*c.log, fmt.Sprintf("Get %s = %v %v", k, v, ok))
	return v, ok
}

func (c callLog) GetForUpdate(k string) (any, bool, error) {
	v, ok, err := c.Txn.GetForUpdate(k)
	*c.log = append(*c.log, fmt.Sprintf("GetForUpdate %s = %v %v %v", k, v, ok, err))
	return v, ok, err
}

func (c callLog) Put(k string, v any) {
	c.Txn.Put(k, v)
	*c.log = append(*c.log, fmt.Sprintf("Put %s %v", k, v))
}

func (c callLog) Delete(k string) {
	c.Txn.Delete(k)
	*c.log = append(*c.log, "Delete "+k)
}

func (c callLog) Scan(p string) []qenv.KV {
	kvs := c.Txn.Scan(p)
	*c.log = append(*c.log, fmt.Sprintf("Scan %s = %v", p, kvs))
	return kvs
}

func (c callLog) ScanRange(r qenv.KeyRange) []qenv.KV {
	kvs := c.Txn.ScanRange(r)
	*c.log = append(*c.log, fmt.Sprintf("ScanRange %+v = %v", r, kvs))
	return kvs
}

// A Table makes exactly the Txn calls its raw form makes, on the prefixed
// keys, so a wrapper, the isolation level and the history check see the
// same transaction; its reads come back typed, its scans' keys after the
// prefix.
func TestTableIsItsRawCalls(t *testing.T) {
	accts := qenv.NewTable[[]entry]("acct/")
	typed := func(tx qenv.Txn) string {
		es, ok := accts.Get(tx, "a")
		if !ok || len(es) != 1 || es[0].Amount != 5 {
			return fmt.Sprintf("Get a: %v %v", es, ok)
		}
		es, ok, err := accts.GetForUpdate(tx, "b")
		if err != nil || ok || es != nil {
			return fmt.Sprintf("GetForUpdate b: %v %v %v", es, ok, err)
		}
		accts.Put(tx, "b", append(slices.Clip(es), entry{7}))
		accts.Delete(tx, "c")
		rows := accts.Scan(tx)
		if fmt.Sprint(rows) != "[{a [{5}]} {b [{7}]}]" {
			return fmt.Sprintf("Scan: %v", rows)
		}
		r := accts.Range()
		r.From = accts.Key("b")
		if rows := accts.ScanRange(tx, r); fmt.Sprint(rows) != "[{b [{7}]}]" {
			return fmt.Sprintf("ScanRange: %v", rows)
		}
		return ""
	}
	raw := func(tx qenv.Txn) string {
		tx.Get("acct/a")
		tx.GetForUpdate("acct/b")
		tx.Put("acct/b", append([]entry(nil), entry{7}))
		tx.Delete("acct/c")
		tx.Scan("acct/")
		tx.ScanRange(qenv.KeyRange{Prefix: "acct/", From: "acct/b"})
		return ""
	}
	run := func(f func(qenv.Txn) string) []string {
		s := qenv.New(qenv.Nominal{})
		defer s.Close()
		s.Check = true
		st := qenv.NewStore(s, "db", qenv.Serializable)
		st.Load("acct/a", []entry{{5}})
		st.Load("acct/c", []entry{{1}})
		st.Load("other", "x")
		var log []string
		s.Spawn("t", func(ctx context.Context) {
			err := qenv.Retry(ctx, st, 1, func(tx qenv.Txn) error {
				if bad := f(callLog{tx, &log}); bad != "" {
					t.Error(bad)
				}
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		})
		if err := s.Run(time.Hour); err != nil {
			t.Fatal(err)
		}
		if _, ok := st.Read("acct/c"); ok {
			t.Error("acct/c not deleted")
		}
		return log
	}
	if got, want := run(typed), run(raw); !slices.Equal(got, want) {
		t.Fatalf("the table's calls:\n%s\nthe raw calls:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A value of another type under a table's key (written raw) is a panic
// naming the key, the type found and the table's, at every typed read.
func TestTableWrongTypePanics(t *testing.T) {
	accts := qenv.NewTable[[]entry]("acct/")
	for name, read := range map[string]func(tx qenv.Txn){
		"Get":          func(tx qenv.Txn) { accts.Get(tx, "a") },
		"GetForUpdate": func(tx qenv.Txn) { _, _, _ = accts.GetForUpdate(tx, "a") },
		"Scan":         func(tx qenv.Txn) { accts.Scan(tx) },
		"ScanRange":    func(tx qenv.Txn) { accts.ScanRange(tx, accts.Range()) },
	} {
		s := qenv.New(qenv.Nominal{})
		st := qenv.NewStore(s, "db", qenv.Serializable)
		st.Load("acct/a", "fifty")
		var caught string
		s.Spawn("t", func(ctx context.Context) {
			defer func() { caught = fmt.Sprint(recover()) }()
			tx := st.Begin(ctx)
			defer tx.Rollback()
			read(tx)
		})
		if err := s.Run(time.Hour); err != nil {
			t.Fatal(err)
		}
		s.Close()
		want := `qenv: table "acct/": key "acct/a" holds a string, not a []qenv_test.entry: write the key through the table (Table.Put), or read it raw (Txn.Get) where it holds other types`
		if caught != want {
			t.Errorf("%s of a raw string: %s; want %s", name, caught, want)
		}
	}
}

// An interface-typed table reads back the nil it was given; a range
// outside the table is refused.
func TestTableEdges(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	st := qenv.NewStore(s, "db", qenv.Serializable)
	errs := qenv.NewTable[error]("err/")
	accts := qenv.NewTable[int]("acct/")
	s.Spawn("t", func(ctx context.Context) {
		tx := st.Begin(ctx)
		defer tx.Rollback()
		errs.Put(tx, "x", nil)
		if v, ok := errs.Get(tx, "x"); !ok || v != nil {
			t.Errorf("a nil error put: read %v %v", v, ok)
		}
		if v, ok := accts.Get(tx, "none"); ok || v != 0 {
			t.Errorf("an absent key: %v %v", v, ok)
		}
		func() {
			defer func() {
				if p := fmt.Sprint(recover()); !strings.Contains(p, `ScanRange of prefix "err/", outside the table`) {
					t.Errorf("a range outside the table: %s", p)
				}
			}()
			accts.ScanRange(tx, errs.Range())
		}()
	})
	if err := s.Run(time.Hour); err != nil {
		t.Fatal(err)
	}
}
