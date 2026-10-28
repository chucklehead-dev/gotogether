package validate

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// quint 0.33's Rust backend writes an integer of 16 characters or more
// into ITF as a bignumber.js object, which validate read as a record: a
// pass the evaluator and quint agree on was an evaluator bug under
// GOTOGETHER_EVAL=shadow (the states differed), and a failure's model
// state showed the balance as { c: [...], e: 18, s: 1 }. Read by the
// model's types, the balance is the integer and the model's record of
// fields s, e and c (bigint.qnt's shape) still a record.
func TestRustIntegersReadByType(t *testing.T) {
	skip.Quint(t)
	ms := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	const p = int64(4611686018427387904) // 2^62
	steps := func(left int64) []qtrace.Step {
		return []qtrace.Step{
			{Action: "deposit", Recorder: "c", Seq: 1, Time: ms(1), Args: map[string]any{"amount": p}, Obs: map[string]any{"balance": p}},
			{Action: "withdraw", Recorder: "c", Seq: 2, Time: ms(2), Args: map[string]any{"amount": int64(1)}, Obs: map[string]any{"balance": left}},
		}
	}
	ctx := context.Background()
	for _, eval := range []string{"shadow", "quint"} {
		c, err := NewChecker("testdata/bigint.yaml", Options{Dir: t.TempDir(), Eval: eval, Backend: "rust"})
		if err != nil {
			t.Fatal(err)
		}
		rep, err := c.Check(ctx, steps(p-1))
		if err != nil || !rep.OK() {
			t.Fatalf("%s: a passing run: %v\n%v", eval, err, rep)
		}
		rep, err = c.Check(ctx, steps(p-2))
		if err != nil || rep.OK() {
			t.Fatalf("%s: a failing run: %v\n%v", eval, err, rep)
		}
		f := rep.Actors[0].Failure
		if f == nil || f.State == nil {
			t.Fatalf("%s: no model state at the failure:\n%s", eval, rep.Summary())
		}
		if b := f.State["balance"]; b.Kind != itf.Int || b.String() != "4611686018427387903" {
			t.Errorf("%s: the failure's balance is %s, want 4611686018427387903", eval, b)
		}
		if s := f.State["shape"]; s.Kind != itf.Record || s.String() != "{ c: [10], e: 15, s: 1 }" {
			t.Errorf("%s: the failure's shape is %s, want the record", eval, s)
		}
	}
}
