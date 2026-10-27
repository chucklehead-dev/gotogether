package raftkv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	bolt "go.etcd.io/bbolt"
)

// BoltWAL is the real tier's write-ahead log: a bbolt file, every commit
// fsynced before it returns, so what persist wrote survives a kill -9 as
// raft requires (a vote acknowledged and then forgotten can be cast
// twice). It is the WAL the simulation's qenv.Store stands for.
//
// bbolt has a single writer, so a transaction is serializable and
// GetForUpdate is Get: no other transaction can write between them. Only
// the node's raft loop writes; boot reads before the loop starts.
type BoltWAL struct {
	db *bolt.DB
}

var walBucket = []byte("wal")

// OpenBoltWAL opens (or creates) the log in dir. A new log starts as
// every node of a new cluster does (initialLog): a snapshot of the three
// voters and the keys at Initial.
func OpenBoltWAL(dir string) (*BoltWAL, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	// a second process on one log would be two nodes of one name: the
	// file lock refuses it within the timeout
	db, err := bolt.Open(filepath.Join(dir, "wal.db"), 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("raftkv: open the log in %s: %w", dir, err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(walBucket)
		return err
	}); err != nil {
		db.Close()
		return nil, err
	}
	w := &BoltWAL{db: db}
	if _, ok := w.Read("snap"); !ok {
		initialLog(w, len(Nodes), newSM(Keys, Initial))
	}
	return w, nil
}

// Close closes the file.
func (w *BoltWAL) Close() error { return w.db.Close() }

// Begin starts a write transaction, waiting for any other to end.
func (w *BoltWAL) Begin(context.Context) qenv.Txn {
	tx, err := w.db.Begin(true)
	return &boltTxn{tx: tx, err: err}
}

// Read is key's committed value.
func (w *BoltWAL) Read(key string) (any, bool) {
	var out []byte
	err := w.db.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket(walBucket).Get([]byte(key)); v != nil {
			out = bytes.Clone(v) // bbolt's memory is the transaction's
		}
		return nil
	})
	if err != nil || out == nil {
		return nil, false
	}
	return out, true
}

// Keys are the committed keys with prefix, in order.
func (w *BoltWAL) Keys(prefix string) []string {
	var out []string
	_ = w.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(walBucket).Cursor()
		for k, _ := c.Seek([]byte(prefix)); k != nil && bytes.HasPrefix(k, []byte(prefix)); k, _ = c.Next() {
			out = append(out, string(k))
		}
		return nil
	})
	return out
}

// Load writes key in a transaction of its own.
func (w *BoltWAL) Load(key string, val any) {
	must(w.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(walBucket).Put([]byte(key), val.([]byte)) }))
}

// boltTxn is a bbolt write transaction as a qenv.Txn. Values are []byte
// (the log's marshalled protobufs).
type boltTxn struct {
	tx    *bolt.Tx
	err   error // the first failure, returned by Commit
	hooks []func()
	done  bool
}

var errTxnDone = errors.New("raftkv: transaction used after it ended")

func (t *boltTxn) bucket() *bolt.Bucket {
	if t.done {
		panic(errTxnDone)
	}
	if t.err != nil {
		return nil
	}
	return t.tx.Bucket(walBucket)
}

func (t *boltTxn) Get(key string) (any, bool) {
	b := t.bucket()
	if b == nil {
		return nil, false
	}
	v := b.Get([]byte(key))
	if v == nil {
		return nil, false
	}
	return bytes.Clone(v), true
}

// GetForUpdate is Get: bbolt's single writer already excludes every
// other write until this transaction ends.
func (t *boltTxn) GetForUpdate(key string) (any, bool, error) {
	v, ok := t.Get(key)
	return v, ok, t.err
}

func (t *boltTxn) Put(key string, val any) {
	if b := t.bucket(); b != nil {
		if err := b.Put([]byte(key), val.([]byte)); err != nil {
			t.err = err
		}
	}
}

func (t *boltTxn) Delete(key string) {
	if b := t.bucket(); b != nil {
		if err := b.Delete([]byte(key)); err != nil {
			t.err = err
		}
	}
}

// ScanRange reads the whole prefix and filters it (qenv.ScanPrefix):
// nothing here tracks a range read on its own.
func (t *boltTxn) ScanRange(r qenv.KeyRange) []qenv.KV { return qenv.ScanPrefix(t, r) }

func (t *boltTxn) Scan(prefix string) []qenv.KV {
	b := t.bucket()
	if b == nil {
		return nil
	}
	var out []qenv.KV
	c := b.Cursor()
	for k, v := c.Seek([]byte(prefix)); k != nil && bytes.HasPrefix(k, []byte(prefix)); k, v = c.Next() {
		out = append(out, qenv.KV{Key: string(k), Val: bytes.Clone(v)})
	}
	return out
}

// Commit writes and fsyncs the transaction, then runs its OnCommit hooks.
func (t *boltTxn) Commit() error {
	if t.done {
		return errTxnDone
	}
	t.done = true
	if t.err != nil {
		if t.tx != nil {
			_ = t.tx.Rollback()
		}
		return t.err
	}
	if err := t.tx.Commit(); err != nil {
		return err
	}
	for _, h := range t.hooks {
		h()
	}
	return nil
}

// Rollback ends a transaction not committed; after Commit it does
// nothing (qenv.Retry calls it either way).
func (t *boltTxn) Rollback() {
	if t.done {
		return
	}
	t.done = true
	if t.tx != nil {
		_ = t.tx.Rollback()
	}
}

func (t *boltTxn) OnCommit(fn func()) { t.hooks = append(t.hooks, fn) }
