package qenv

import (
	"fmt"
	"reflect"
	"strings"
)

// A Table is the keys of a DB under one prefix, all holding values of one
// type T: an application's own records (accounts, orders), named once
// where they are declared,
//
//	var accounts = qenv.NewTable[[]Entry]("acct/")
//
// and read without assertions (es, ok := accounts.Get(tx, "alice")).
//
// It is a thin value over Txn: each method makes the one Txn call its raw
// form would, on the store key Prefix+k, so isolation levels,
// CommitFaults, the history check (GOTOGETHER_CHECK) and a Txn wrapper (a
// recording DB, an adapter over database/sql) see exactly what they see
// of Txn.Get(prefix + k). The store still holds any: a Table changes no
// speed and no stored value, and Txn.Get still reads its keys raw.
//
// Values are shared and immutable, as every Store value is: Get returns
// the stored value itself, so a slice or map read from a Table must not
// be changed in place. Append to a clipped copy,
//
//	accounts.Put(tx, a, append(slices.Clip(es), e))
//
// since another transaction's snapshot may hold the same backing array.
//
// Use a Table for an application's own records; raw Txn for untyped
// keys (a flag, a marker row), for code that implements or wraps Txn,
// and from a test through Store.Read and Store.Load (whose keys are whole
// store keys: Table.Key builds one); qsql for relational data.
type Table[T any] struct {
	prefix string
}

// NewTable is the table of the keys under prefix, holding values of type
// T. The prefix is the whole store key's start ("acct/"): give it a
// separator, so one table's prefix is not another's key's start.
func NewTable[T any](prefix string) Table[T] {
	return Table[T]{prefix: prefix}
}

// A Row is a table's key and its value, as a scan reads them: Key is the
// key after the table's prefix, as the table's methods take it.
type Row[T any] struct {
	Key string
	Val T
}

// Prefix is the table's prefix.
func (t Table[T]) Prefix() string { return t.prefix }

// Key is the store key of k: Prefix+k. Every method takes k, the key after
// the prefix, never a whole store key, and adds the prefix whatever k
// starts with: a k that itself starts with the prefix ("acct/" and
// "acct/x") is a legitimate key of its own (a prefix "a" and an account
// "ab"), so it is not refused, and t.Get(tx, t.Key(k)) reads another key
// than t.Get(tx, k).
func (t Table[T]) Key(k string) string { return t.prefix + k }

// Range is the table's whole key range, for a Txn's ScanRange (or
// ScanPrefix, in an adapter) narrowed with From and To, whole store keys
// built with Key.
func (t Table[T]) Range() KeyRange { return KeyRange{Prefix: t.prefix} }

// Get is Txn.Get of k's store key, its value as a T (the zero T if
// absent). A value of another type there (written raw, by Txn.Put or
// Store.Load) panics, naming the key and both types.
func (t Table[T]) Get(tx Txn, k string) (T, bool) {
	v, ok := tx.Get(t.Key(k))
	return t.value(t.Key(k), v, ok), ok
}

// GetForUpdate is Txn.GetForUpdate of k's store key, its value as a T.
// The value is the zero T when the read fails (err) or finds no row.
func (t Table[T]) GetForUpdate(tx Txn, k string) (T, bool, error) {
	v, ok, err := tx.GetForUpdate(t.Key(k))
	if err != nil {
		var zero T
		return zero, ok, err
	}
	return t.value(t.Key(k), v, ok), ok, nil
}

// Put is Txn.Put of k's store key.
func (t Table[T]) Put(tx Txn, k string, v T) { tx.Put(t.Key(k), v) }

// Delete is Txn.Delete of k's store key.
func (t Table[T]) Delete(tx Txn, k string) { tx.Delete(t.Key(k)) }

// Scan is Txn.Scan of the table's prefix: its rows, sorted, keys after the
// prefix. Under Serializable it is a read of the whole table, as the raw
// scan is.
func (t Table[T]) Scan(tx Txn) []Row[T] { return t.rows(tx.Scan(t.prefix)) }

// ScanRange is Txn.ScanRange of r, a range within the table (r.Prefix
// starts with the table's prefix; Range builds one), its rows typed as
// Scan's. A range outside the table panics: its rows are not the
// table's.
func (t Table[T]) ScanRange(tx Txn, r KeyRange) []Row[T] {
	if !strings.HasPrefix(r.Prefix, t.prefix) {
		panic(fmt.Sprintf("qenv: table %q: ScanRange of prefix %q, outside the table: start the range from the table's Range()", t.prefix, r.Prefix))
	}
	return t.rows(tx.ScanRange(r))
}

func (t Table[T]) rows(kvs []KV) []Row[T] {
	out := make([]Row[T], 0, len(kvs))
	for _, kv := range kvs {
		out = append(out, Row[T]{Key: strings.TrimPrefix(kv.Key, t.prefix), Val: t.value(kv.Key, kv.Val, true)})
	}
	return out
}

// value is v as a T, or the zero T for an absent key; a value of another
// type panics with the fix.
func (t Table[T]) value(key string, v any, ok bool) T {
	var zero T
	if !ok {
		return zero
	}
	if x, is := v.(T); is {
		return x
	}
	typ := reflect.TypeFor[T]()
	if v == nil && typ.Kind() == reflect.Interface {
		return zero // a nil T, put through the table
	}
	panic(fmt.Sprintf("qenv: table %q: key %q holds a %T, not a %v: write the key through the table (Table.Put), or read it raw (Txn.Get) where it holds other types",
		t.prefix, key, v, typ))
}
