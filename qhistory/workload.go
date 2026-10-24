package qhistory

import (
	"fmt"
	"math/rand/v2"
)

// A Workload is a random list-append workload: Txns transactions of 1 to
// MaxOps operations each (default 4), half appends and half reads, over
// Keys keys ("k0", "k1", ...; default 3), drawn from Seed. Few keys and
// short transactions make transactions conflict, which is what shows
// anomalies.
type Workload struct {
	Keys, Txns, MaxOps int
	Seed               uint64
}

// Generate returns the workload's transactions, IDs 1.., every value
// appended to a key unique (1, 2, ... per key), Outcomes Unknown and
// reads' Lists empty, for the run to fill in.
func (w Workload) Generate() []Txn {
	if w.Keys <= 0 {
		w.Keys = 3
	}
	if w.MaxOps <= 0 {
		w.MaxOps = 4
	}
	r := rand.New(rand.NewPCG(w.Seed, 0x2545f4914f6cdd1d))
	next := map[string]int{}
	txns := make([]Txn, w.Txns)
	for i := range txns {
		t := Txn{ID: i + 1}
		for range 1 + r.IntN(w.MaxOps) {
			key := fmt.Sprintf("k%d", r.IntN(w.Keys))
			if r.IntN(2) == 0 {
				next[key]++
				t.Ops = append(t.Ops, Op{Kind: Append, Key: key, Value: next[key]})
			} else {
				t.Ops = append(t.Ops, Op{Kind: Read, Key: key})
			}
		}
		txns[i] = t
	}
	return txns
}
