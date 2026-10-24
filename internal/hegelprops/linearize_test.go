package hegelprops

import (
	"fmt"
	"testing"
	"time"

	"github.com/anishathalye/porcupine"
	"hegel.dev/go/hegel"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// regOp is a register operation as both checkers see it.
type regOp struct {
	client     int
	write      bool
	v          int64 // the value written, or read
	call, back int64 // its interval, in units of 10 ms (each beyond RealTimeSlack)
}

// register is the model qtrace.Linearize checks orders against: a read
// sees the last write (0 before any). It returns each order's first read
// that does not, or -1.
func register(orders [][]qtrace.Step) []int {
	out := make([]int, len(orders))
	for i, o := range orders {
		out[i] = -1
		var v int64
		for j, s := range o {
			if s.Action == "write" {
				v = s.Args["v"].(int64)
			} else if s.Obs["v"] != v {
				out[i] = j
				break
			}
		}
	}
	return out
}

// porcupineRegister is the same register for Porcupine.
var porcupineRegister = porcupine.Model{
	Init: func() any { return int64(0) },
	Step: func(state, input, output any) (bool, any) {
		op := input.(regOp)
		if op.write {
			return true, op.v
		}
		return op.v == state.(int64), state
	},
}

// Linearize agrees with Porcupine (Lowe's WGL, a linearizability checker
// in wide use) on register histories of up to 16 operations of up to four
// clients, too many to enumerate every order of: linearizable when it
// finds an order, not when its complete search, uncut, finds none. A
// history its budget cuts is no verdict, counted and not compared.
func TestLinearizeAgreesWithPorcupine(t *testing.T) {
	cut, compared, rejected := 0, 0, 0
	hegel.Test(t, func(ht *hegel.T) {
		clients := hegel.Draw(ht, hegel.Integers(1, 4))
		var ops []regOp
		written := []int64{0}
		for c := range clients {
			at := int64(hegel.Draw(ht, hegel.Integers(0, 3)))
			for range hegel.Draw(ht, hegel.Integers(1, 4)) {
				// distinct instants: 4 per unit, one per client
				call := at*4 + int64(c)
				back := call + 4*int64(hegel.Draw(ht, hegel.Integers(1, 6)))
				o := regOp{client: c, call: call, back: back}
				if hegel.Draw(ht, hegel.Booleans()) {
					o.write, o.v = true, int64(len(ops)+1)
					written = append(written, o.v)
				} else {
					o.v = written[hegel.Draw(ht, hegel.Integers(0, len(written)-1))]
				}
				ops = append(ops, o)
				at = back/4 + 1 + int64(hegel.Draw(ht, hegel.Integers(0, 2)))
			}
		}
		var hist []porcupine.Operation
		var steps []qtrace.Step
		seq := map[int]uint64{}
		unit := func(u int64) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
		for _, o := range ops {
			hist = append(hist, porcupine.Operation{ClientId: o.client, Input: o, Call: o.call, Output: nil, Return: o.back})
			seq[o.client]++
			rec := fmt.Sprint("c", o.client)
			s := qtrace.Step{Recorder: rec, Seq: seq[o.client], Thread: rec, Actor: "r", Process: "p",
				Invoked: unit(o.call), Time: unit(o.back), Args: map[string]any{}, Obs: map[string]any{}}
			if o.write {
				s.Action, s.Args["v"] = "write", o.v
			} else {
				s.Action, s.Obs["v"] = "read", o.v
			}
			steps = append(steps, s)
		}
		want := porcupine.CheckOperations(porcupineRegister, hist)
		_, got, _, wasCut := qtrace.Linearize(steps, 20000, register)
		if wasCut && !got {
			cut++
			return
		}
		compared++
		if !got {
			rejected++
		}
		if got != want {
			ht.Fatalf("Linearize says %v, Porcupine %v, of %+v", got, want, ops)
		}
	}, hegel.WithTestCases(300))
	t.Logf("%d histories compared (%d not linearizable), %d cut short", compared, rejected, cut)
	if rejected == 0 || compared-rejected == 0 {
		t.Errorf("the property compared %d linearizable and %d not: it needs both", compared-rejected, rejected)
	}
}
