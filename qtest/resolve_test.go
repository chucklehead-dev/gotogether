package qtest

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A withdrawal left pending (its caller crashed) and a later deposit's
// balance: 12 says the withdrawal took effect before it, 15 that it never
// did, 99 that nothing explains the run. Checked as recorded, a pending
// call is no step and 12 is a mismatch.
func TestResolvePending(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	history := func(balance int) []qtrace.Step {
		return []qtrace.Step{
			{Action: "deposit", Recorder: "c", Seq: 1, Time: at(1), Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(10)}},
			{Action: "withdraw", Recorder: "c", Seq: 2, Time: at(2), Invocation: true, Args: map[string]any{"amount": int64(3)}},
			{Action: "deposit", Recorder: "c", Seq: 3, Time: at(3), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(balance)}},
		}
	}
	const account = "../validate/testdata/account.yaml"
	Validate(t, account, history(12)).Expect(t, Mismatch("deposit"))
	for _, c := range []struct {
		balance int
		says    string
	}{
		{12, "withdraw(amount=3) took effect after step 1"},
		{15, "withdraw(amount=3) had no effect"},
	} {
		run := Validate(t, account, history(c.balance), ResolvePending())
		run.Expect(t, Conforms())
		if !strings.Contains(fmt.Sprint(run.Report.Issues), c.says) {
			t.Errorf("balance %d: issues %v, want %q", c.balance, run.Report.Issues, c.says)
		}
		if Completes().Holds(run) {
			t.Errorf("balance %d: a resolved call completed: its caller never knew", c.balance)
		}
	}
	Validate(t, account, history(99), ResolvePending()).Expect(t, Mismatch("deposit"))
}

// Calls that completed before a pending one stay paired, and the pending
// one stays pending, however the resolution re-sequences the steps (their
// call ids once named records of the new sequence: Completes held). A call
// invoked at the same instant as a step recorded before it takes effect
// only after that step: deposit(3) cannot explain a balance of 8 seen by
// the deposit(5) recorded first.
func TestResolvePendingKeepsCallsAndOrder(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	const account = "../validate/testdata/account.yaml"
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "c", Seq: 1, Time: at(1), Invocation: true, Args: map[string]any{"amount": int64(10)}},
		{Action: "deposit", Recorder: "c", Seq: 2, Time: at(2), Call: 1, Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(10)}},
		{Action: "deposit", Recorder: "c", Seq: 3, Time: at(3), Invocation: true, Args: map[string]any{"amount": int64(5)}},
		{Action: "deposit", Recorder: "c", Seq: 4, Time: at(4), Call: 3, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(15)}},
		{Action: "withdraw", Recorder: "c", Seq: 5, Time: at(5), Invocation: true, Args: map[string]any{"amount": int64(3)}},
	}
	run := Validate(t, account, steps, ResolvePending())
	run.Expect(t, Conforms())
	if Completes().Holds(run) || !Pending("withdraw").Holds(run) {
		t.Errorf("the pending withdrawal vanished: %s", run.Report.Summary())
	}
	same := []qtrace.Step{
		{Action: "deposit", Recorder: "c", Seq: 1, Time: at(1), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(8)}},
		{Action: "deposit", Recorder: "c", Seq: 2, Time: at(1), Invocation: true, Args: map[string]any{"amount": int64(3)}},
	}
	Validate(t, account, same, ResolvePending()).Expect(t, Mismatch("deposit"))
}

// A pending call cannot have taken effect after the system settled: it
// was quiet there, nothing in flight. A write left pending, a settle, a
// probe that saw it missing: placed after the probe, it once made the
// eventual val hold and the run conform.
func TestResolvePendingStopsAtTheSettle(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(100, 0).Add(time.Duration(u) * time.Second) }
	steps := []qtrace.Step{
		{Action: "write", Recorder: "r", Seq: 1, Thread: "c", Invocation: true, Time: at(1), Args: map[string]any{"value": int64(1)}},
		{Action: "settled", Recorder: "r", Seq: 2, Settled: true, Time: at(2)},
		{Action: "observe", Recorder: "r", Seq: 3, Thread: "probe", Time: at(3), Obs: map[string]any{"value": int64(0)}},
		{Action: "settled", Recorder: "r", Seq: 4, Settled: true, Time: at(4)},
	}
	run := Validate(t, "testdata/probed.yaml", steps, ResolvePending())
	if Conforms().Holds(run) {
		t.Errorf("a write placed after the settle that preceded its probe:\n%s", run.Report.Summary())
	}
}

// A settle another recorder worked past (it settles nothing) bounds no
// placement: the write left pending landed after it, as a later probe
// saw, and once was a false Mismatch.
func TestResolvePendingIgnoresASettleWorkedPast(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(100, 0).Add(time.Duration(u) * time.Second) }
	steps := []qtrace.Step{
		{Action: "write", Recorder: "c", Seq: 1, Thread: "c", Invocation: true, Time: at(1), Args: map[string]any{"value": int64(1)}},
		{Action: "settled", Recorder: "h", Seq: 1, Settled: true, Time: at(2)},
		{Action: "observe", Recorder: "s", Seq: 1, Thread: "s", Time: at(30), Obs: map[string]any{"value": int64(0)}},
		{Action: "observe", Recorder: "s", Seq: 2, Thread: "s", Time: at(50), Obs: map[string]any{"value": int64(1)}},
	}
	Validate(t, "testdata/probed.yaml", steps, ResolvePending()).Expect(t, Conforms())
}

// On small random histories of a counter (incs, a finish, peeks that
// observe it) with pending incs, ResolvePending's verdicts are a brute
// force's over every placement: the run conforms if some placement does,
// and violates ok only if every placement that conforms violates it. A
// pending inc takes effect after the steps recorded before its
// invocation, or never. Judged on the first placement that conforms
// alone, it said Violates where another placement kept ok (review 11, F3).
func TestResolvePendingInvariantsAgainstBruteForce(t *testing.T) {
	RequireQuint(t)
	if testing.Short() {
		t.Skip("a validation per history")
	}
	t.Parallel()
	rng := rand.New(rand.NewPCG(11, 3))
	at := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	counts := map[string]int{}
	for trial := range 24 {
		type rec struct {
			action string
			x      int64 // a peek's observation
		}
		var recorded []rec
		for range 2 + rng.IntN(3) {
			switch rng.IntN(3) {
			case 0:
				recorded = append(recorded, rec{action: "inc"})
			case 1:
				recorded = append(recorded, rec{action: "finish"})
			default:
				recorded = append(recorded, rec{action: "peek", x: int64(rng.IntN(3))})
			}
		}
		// pending incs, invoked after from[i] recorded steps
		var from []int
		for range 1 + rng.IntN(2) {
			from = append(from, rng.IntN(len(recorded)+1))
		}
		var steps []qtrace.Step
		seq := 0
		add := func(s qtrace.Step) {
			seq++
			s.Recorder, s.Seq, s.Time = "c", uint64(seq), at(seq)
			steps = append(steps, s)
		}
		for pos := 0; pos <= len(recorded); pos++ {
			for _, f := range from {
				if f == pos {
					add(qtrace.Step{Action: "inc", Invocation: true})
				}
			}
			if pos < len(recorded) {
				r := recorded[pos]
				s := qtrace.Step{Action: r.action}
				if r.action == "peek" {
					s.Obs = map[string]any{"x": r.x}
				}
				add(s)
			}
		}
		// brute force: every placement (-1: no effect)
		conforms, keeps := false, false
		var place func(i int, pl []int)
		place = func(i int, pl []int) {
			if i == len(from) {
				x, done, ok, fits := int64(0), false, true, true
				for pos := 0; pos <= len(recorded) && fits; pos++ {
					for _, p := range pl {
						if p == pos {
							x++
						}
					}
					ok = ok && (!done || x >= 2)
					if pos == len(recorded) {
						break
					}
					switch r := recorded[pos]; r.action {
					case "inc":
						x++
					case "finish":
						done = true
					case "peek":
						fits = r.x == x
					}
					ok = ok && (!done || x >= 2)
				}
				conforms = conforms || fits
				keeps = keeps || fits && ok
				return
			}
			place(i+1, append(pl, -1))
			for pos := from[i]; pos <= len(recorded); pos++ {
				place(i+1, append(pl, pos))
			}
		}
		place(0, nil)
		run := Validate(t, "testdata/peekflag.yaml", steps, ResolvePending())
		gotConforms := run.Report.Actors[0].Conforms == validate.Pass
		gotViolates := Violates("ok").Holds(run)
		if gotConforms != conforms || gotViolates != (conforms && !keeps) {
			t.Fatalf("trial %d: conforms %v violates %v, brute force conforms %v keeps ok %v, for\n%v\n%s", trial, gotConforms, gotViolates, conforms, keeps, steps, run.Report.Summary())
		}
		counts[fmt.Sprintf("conforms=%v keeps=%v", conforms, keeps)]++
	}
	if len(counts) < 3 {
		t.Errorf("verdicts %v: the property compared too little", counts)
	}
	t.Logf("verdicts: %v", counts)
}
