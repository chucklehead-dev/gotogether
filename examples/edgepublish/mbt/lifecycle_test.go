package mbt

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/itf"
)

// A finite trace need not complete its pushes or seals. Neither successful
// replay nor a failed later step may leave that work waiting for another step.
func TestReplayJoinsBoundedAndFailedWork(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "bounded"
		if failed {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			tr := lifecyclePrefix()
			if failed {
				last := tr.States[len(tr.States)-1]
				last.Action = "missing-handler"
				tr.States = append(tr.States, last)
			}
			d := &Driver{}
			defer releaseReplay(d)
			err := d.Replay(tr)
			if failed && (err == nil || !strings.Contains(err.Error(), "missing-handler")) {
				t.Fatalf("replay error = %v; want the original missing-handler error", err)
			}
			if !failed && err != nil {
				t.Fatal(err)
			}
			if len(d.pushes) != 1 || len(d.seals) != 1 {
				t.Fatalf("prefix left %d pushes and %d seals; want one of each", len(d.pushes), len(d.seals))
			}
			for _, fl := range d.pushes {
				select {
				case <-fl.done:
				default:
					t.Error("Replay returned with its bounded push still running")
				}
			}
			drained := make(chan struct{})
			go func() { d.pub.Drain(); close(drained) }()
			select {
			case <-drained:
			case <-time.After(2 * time.Second):
				t.Error("Replay returned with its bounded background seal still running")
			}
			if d.ctx.Err() != context.Canceled {
				t.Error("Replay did not end its worker lifetime")
			}
		})
	}
}

// The second push reaches a gate but fails generation mapping before entering
// d.pushes. Teardown must own spawned work, not just successfully mapped work.
func TestReplayJoinsRejectedPushStart(t *testing.T) {
	first, second := lifecyclePush(1, 1), lifecyclePush(2, 2)
	tr := &itf.Trace{States: []itf.State{
		lifecycleState("init", 1),
		lifecycleState("pushStart", 1, first),
		lifecycleState("pushStart", 1, first, second),
	}}
	d := &Driver{}
	defer releaseReplay(d)
	if err := d.Replay(tr); err == nil || !strings.Contains(err.Error(), "but was") {
		t.Fatalf("replay error = %v; want generation mapping rejection", err)
	}
	if len(d.pushes) != 1 {
		t.Fatalf("mapped pushes = %d; want only the first push", len(d.pushes))
	}
	joined := make(chan struct{})
	go func() { d.work.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Error("Replay returned with a rejected, unmapped push still running")
	}
}

func TestReplayEmptyTrace(t *testing.T) {
	if err := (&Driver{}).Replay(&itf.Trace{}); err == nil {
		t.Fatal("empty trace passed")
	}
}

// releaseReplay keeps the negative control's failures from leaking their own
// gated workers when Replay's deferred cleanup is deliberately disabled.
func releaseReplay(d *Driver) {
	if d.cancel != nil {
		d.cancel()
		d.work.Wait()
	}
	if d.pub != nil {
		d.pub.Drain()
	}
}

func lifecyclePrefix() *itf.Trace {
	return &itf.Trace{States: []itf.State{
		lifecycleState("init", 1),
		lifecycleState("rotateGen", 2),
		lifecycleState("pushStart", 2, lifecyclePush(1, 2)),
	}}
}

func lifecycleState(action string, gen int64, pushes ...itf.Value) itf.State {
	return itf.State{Action: action, Vars: map[string]itf.Value{
		"writer": {Kind: itf.Record, Fields: map[string]itf.Value{
			"epoch": lifecycleInt(1), "gen": lifecycleInt(gen), "pushes": {Kind: itf.Set, Elems: pushes},
		}},
		"tableRows": {Kind: itf.Set}, "parquet": {Kind: itf.Set},
		"manifests": {Kind: itf.Set}, "seals": {Kind: itf.Set},
	}}
}

func lifecyclePush(batch, gen int64) itf.Value {
	return itf.Value{Kind: itf.Record, Fields: map[string]itf.Value{
		"payload": lifecycleInt(batch),
		"bk":      {Kind: itf.Record, Fields: map[string]itf.Value{"epoch": lifecycleInt(1), "batch": lifecycleInt(batch)}},
		"gk":      {Kind: itf.Record, Fields: map[string]itf.Value{"epoch": lifecycleInt(1), "gen": lifecycleInt(gen)}},
		"phase":   {Kind: itf.Variant, Tag: "Encoded"},
	}}
}

func lifecycleInt(n int64) itf.Value { return itf.Value{Kind: itf.Int, I: big.NewInt(n)} }
