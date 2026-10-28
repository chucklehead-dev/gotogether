package connect

import (
	"fmt"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// bigDriver is the implementation of validate's testdata/bigint.qnt: a
// balance past 15 digits, and a record of fields s, e and c.
type bigDriver struct{ bal int64 }

type digits struct {
	S, E int64
	C    []int64
}

func (d *bigDriver) Step(s *Step) error {
	return Switch{
		"init":     func(*Step) error { d.bal = 0; return nil },
		"deposit":  func(*Step) error { d.bal += 1_000_000_000_000_000; return nil },
		"withdraw": func(*Step) error { d.bal--; return nil },
	}.Step(s)
}

func (d *bigDriver) State() (any, error) {
	return struct {
		Balance int64
		Shape   digits
	}{d.bal, digits{1, 15, []int64{10}}}, nil
}

// pickDriver checks the picks of itf's testdata/bignumber.qnt: an
// integer pick past 15 digits and a record pick of fields s, e and c.
type pickDriver struct{}

func (pickDriver) Step(s *Step) error {
	if s.Action != "step" {
		return nil
	}
	if n := MustPick[int64](s, "n"); n != -1<<63 {
		return fmt.Errorf("the pick n is %d", n)
	}
	if d := MustPick[digits](s, "d"); d.S != 1 || d.E != 15 || len(d.C) != 1 || d.C[0] != 10 {
		return fmt.Errorf("the pick d is %+v", d)
	}
	return nil
}

// quint 0.33's Rust backend writes an integer of 16 characters or more
// into ITF as a bignumber.js object, which connect read as a record: a
// state that agreed diverged ("want int, have record { c: [10], e: 15,
// s: 1 }"), and a pick was no integer. Its traces are read by the
// model's types, the model's own records of those fields still records,
// on both backends (Rust alone under -short).
func TestRustIntegersReadByType(t *testing.T) {
	skip.Quint(t)
	backends := []string{"rust", "typescript"}
	if testing.Short() {
		backends = backends[:1] // the one that writes them
	}
	for _, backend := range backends {
		t.Run(backend, func(t *testing.T) {
			Run(t, Config{Spec: "../validate/testdata/bigint.qnt", MaxSteps: 4, MaxSamples: 3, Backend: backend}, func() Driver { return &bigDriver{} })
			Run(t, Config{Spec: "../itf/testdata/bignumber.qnt", MaxSteps: 2, MaxSamples: 2, Backend: backend}, func() Driver { return pickDriver{} })
		})
	}
}
