package qtest

import (
	"context"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qobs"
)

// Runs judges claims over runs made one by one (a real deployment's,
// each validated from its logs) as an exploration judges its own: Some
// wants one run showing it, All and None every run, and a failure names
// the run by its place.
func TestRunsGiven(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	b := "../validate/testdata/account.yaml"
	rec := func(ops ...any) func(*qobs.Recorder) {
		return func(r *qobs.Recorder) {
			for i := 0; i+2 < len(ops); i += 3 {
				r.Record(context.Background(), ops[i].(string), nil, "amount", ops[i+1], "obs.balance", ops[i+2])
			}
		}
	}
	ok := Record(t, b, rec("deposit", 5, 5))
	overdrawn := Record(t, b, rec("deposit", 5, 5, "withdraw", 9, -4))
	Runs(ok, overdrawn).Expect(t, Some(Rejects("withdraw")), None(Violates("underLimit")), Some(Conforms()))
	for _, c := range []struct {
		claim Claim
		want  string
	}{
		{All(Conforms()), "run 2"},
		{None(Rejects("withdraw")), "run 2"},
		{Some(Mismatch("deposit")), "none as"},
	} {
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { defer close(done); Runs(ok, overdrawn).Expect(f, c.claim) }() //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		<-done
		if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), c.want) {
			t.Errorf("%v: want a failure naming %q, got %v", c.claim, c.want, f.errs)
		}
	}
}
