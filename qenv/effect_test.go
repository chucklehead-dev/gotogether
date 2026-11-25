package qenv_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

type capture struct {
	Key    string
	Amount int
	secret string // unexported: no field of the effect's
}

// The doubles record what the code sends, at the send: a Service call's
// request by its exported fields (a lost one too: the network's fate is
// not the code's), a Queue's publish by its body. A step the task
// records next is observed, and names them (rule 2); a call's scope
// takes them as its own (rule 1).
func TestDoublesRecordEffects(t *testing.T) {
	t.Parallel()
	sink := &qobs.MemorySink{}
	rec := qobs.NewRecorder(sink)
	s := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(t.Context(), rec)))
	svc := qenv.NewService(s, "provider", nil, func(context.Context, capture) (string, error) { return "ok", nil })
	q := qenv.NewQueue(s, "mail", nil)
	s.Spawn("shop", func(ctx context.Context) {
		if _, err := svc.Call(ctx, capture{Key: "k1", Amount: 10, secret: "s"}, time.Second); err != nil {
			t.Errorf("call: %v", err)
		}
		q.Publish(ctx, map[string]any{"order": "o1"})
		qobs.Record(ctx, "mark", nil)
		c := qobs.Invoke(ctx, "pay")
		_, _ = svc.Call(c.Scope(ctx), capture{Key: "k2", Amount: 20}, time.Second)
		c.Done(nil)
	})
	if err := s.Run(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close()
	trs, _ := qtrace.Reconstruct(sink.Steps())
	if len(trs) != 1 || len(trs[0].Steps) != 2 {
		t.Fatalf("traces %v", trs)
	}
	mark, pay := trs[0].Steps[0], trs[0].Steps[1]
	if !mark.EffectsObserved || len(mark.Effects) != 2 {
		t.Fatalf("mark: observed %v, effects %v", mark.EffectsObserved, mark.Effects)
	}
	call, pub := mark.Effects[0], mark.Effects[1]
	if call.Kind != "rpc.provider.request" || call.By != "task" || call.Fields["Key"] != "k1" || call.Fields["Amount"] != int64(10) || len(call.Fields) != 2 {
		t.Errorf("the call's effect %v", call)
	}
	if pub.Kind != "queue.mail.publish" || pub.Fields["order"] != "o1" {
		t.Errorf("the publish's effect %v", pub)
	}
	if len(pay.Effects) != 1 || pay.Effects[0].By != "call" || pay.Effects[0].Fields["Key"] != "k2" {
		t.Errorf("pay's effects %v", pay.Effects)
	}
}

// EffectFields: a struct's exported fields by name, a map's entries in
// order, anything else its value.
func TestEffectFields(t *testing.T) {
	t.Parallel()
	got := qenv.EffectFields(&capture{Key: "k", Amount: 1})
	if len(got) != 4 || got[0] != "Key" || got[1] != "k" || got[2] != "Amount" || got[3] != 1 {
		t.Errorf("struct: %v", got)
	}
	if got := qenv.EffectFields(map[string]int{"b": 2, "a": 1}); len(got) != 4 || got[0] != "a" || got[2] != "b" {
		t.Errorf("map: %v", got)
	}
	if got := qenv.EffectFields("x"); len(got) != 2 || got[0] != "value" || got[1] != "x" {
		t.Errorf("scalar: %v", got)
	}
	if got := qenv.EffectFields(nil); got != nil {
		t.Errorf("nil: %v", got)
	}
}
