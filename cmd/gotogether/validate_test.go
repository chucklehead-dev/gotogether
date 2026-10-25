package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// validate ends with how many traces fail and why, in the claims' words:
// it said "VIOLATION" for any failure, a mismatch included.
func TestValidateResultLine(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not installed")
	}
	step := func(actor string, seq uint64, action string, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: action, Actor: actor, Recorder: actor, Thread: actor, Seq: seq,
			Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	steps := []qtrace.Step{
		step("good", 1, "deposit", 5, 5),
		step("off", 1, "deposit", 5, 6),    // a mismatch
		step("over", 1, "withdraw", 5, -5), // a step the model cannot take
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "run.jsonl")
	f, err := os.Create(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := qtrace.WriteJSONL(f, steps); err != nil {
		t.Fatal(err)
	}
	f.Close()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	code, err := runValidate([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out"), log})
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if err != nil || code != 1 {
		t.Fatalf("exit %d, %v:\n%s", code, err, out)
	}
	if want := "RESULT: FAIL (2 of 3 actors: 1 mismatch after deposit, 1 rejects withdraw)"; !strings.Contains(string(out), want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
	// a passing run says which invariants it checked, if any
	f, err = os.Create(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := qtrace.WriteJSONL(f, steps[:1]); err != nil {
		t.Fatal(err)
	}
	f.Close()
	r, w, _ = os.Pipe()
	os.Stdout = w
	code, err = runValidate([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out2"), log})
	os.Stdout = stdout
	w.Close()
	out, _ = io.ReadAll(r)
	if want := "RESULT: PASS (1 actor conforms to the model and satisfies its 2 invariants)"; err != nil || code != 0 || !strings.Contains(string(out), want) {
		t.Errorf("exit %d, %v; output lacks %q:\n%s", code, err, want, out)
	}
}

// -linearize checks a client history in an order real time allows: a
// withdrawal that overlapped the deposit it depended on returned first,
// which recorded order rejects and the order the intervals allow accepts.
func TestValidateLinearize(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not installed")
	}
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "a", Process: "p", Seq: 1, Thread: "a", Invoked: at(0), Time: at(10),
			Args: map[string]any{"amount": int64(60)}, Obs: map[string]any{"balance": int64(60)}},
		{Action: "withdraw", Recorder: "b", Process: "p", Seq: 1, Thread: "b", Invoked: at(1), Time: at(5),
			Args: map[string]any{"amount": int64(50)}, Obs: map[string]any{"balance": int64(10)}},
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "run.jsonl")
	f, err := os.Create(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := qtrace.WriteJSONL(f, steps); err != nil {
		t.Fatal(err)
	}
	f.Close()
	for _, c := range []struct {
		flags []string
		code  int
	}{{nil, 1}, {[]string{"-linearize"}, 0}, {[]string{"-history", filepath.Join(dir, "h.svg")}, 1}} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout := os.Stdout
		os.Stdout = w
		args := append(append([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out")}, c.flags...), log)
		code, err := runValidate(args)
		os.Stdout = stdout
		w.Close()
		out, _ := io.ReadAll(r)
		if err != nil || code != c.code {
			t.Errorf("%v: exit %d, %v, want %d:\n%s", c.flags, code, err, c.code, out)
		}
	}
	// -history drew the failing run: the withdrawal checked first, red
	svg, err := os.ReadFile(filepath.Join(dir, "h.svg"))
	if err != nil || !strings.Contains(string(svg), "<svg") || strings.Count(string(svg), `fill="#f6b5b5"`) != 1 {
		t.Errorf("-history: %v\n%s", err, svg)
	}
}

// The CLI's column of the verdict-path matrix (validate's
// TestVerdictMatrix): every kind of verdict, its passing and failing run
// written to a step log and read back, judged by gotogether validate with
// the flags it needs. A settle record, an invocation record or a time
// lost on the way, or a flag not passed to the search, shows here.
func TestValidateVerdictMatrix(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not installed")
	}
	at := func(s int) time.Time { return time.Unix(0, 0).Add(time.Duration(s) * time.Second) }
	ms := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	seq := func(steps ...qtrace.Step) []qtrace.Step {
		for i := range steps {
			steps[i].Recorder, steps[i].Seq = "r", uint64(i+1)
			if steps[i].Time.IsZero() {
				steps[i].Time = at(i)
			}
		}
		return steps
	}
	acct := func(action string, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: action, Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	timed := func(action string, s int) qtrace.Step { return qtrace.Step{Action: action, Time: at(s)} }
	gossip := func(i int, node string, value int, seen string) qtrace.Step {
		s := qtrace.Step{Recorder: "r", Seq: uint64(i), Actor: "g", Args: map[string]any{"node": node}, Obs: map[string]any{}}
		if seen == "" {
			s.Action, s.Args["value"] = "write", int64(value)
		} else {
			s.Action, s.Obs["seen"] = "read", seen
		}
		return s
	}
	pending := func(balance int64) []qtrace.Step {
		return []qtrace.Step{
			{Action: "deposit", Recorder: "c", Seq: 1, Time: ms(1), Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(10)}},
			{Action: "withdraw", Recorder: "c", Seq: 2, Time: ms(2), Invocation: true, Args: map[string]any{"amount": int64(3)}},
			{Action: "deposit", Recorder: "c", Seq: 3, Time: ms(3), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": balance}},
		}
	}
	settled := qtrace.Step{Action: "settled", Settled: true}
	td := "../../validate/testdata/"
	for _, x := range []struct {
		name, binding string
		flags         []string
		pass, fail    []qtrace.Step
		says          string // what the failing run's report says
	}{
		{"conformance", "account.yaml", nil, seq(acct("deposit", 5, 5)), seq(acct("deposit", 5, 6)), "conformance: FAIL"},
		{"invariant", "account.yaml", nil, seq(acct("deposit", 50, 50)), seq(acct("deposit", 150, 150)), "invariant underLimit: FAIL"},
		{"eventual", "funds.binding.yaml", nil, seq(acct("deposit", 5, 5), acct("withdraw", 5, 0)),
			seq(acct("deposit", 5, 5), acct("withdraw", 5, 0), settled), "funded: FAIL"},
		{"within", "within.yaml", nil, seq(timed("crash", 0), timed("heal", 1), timed("elect", 2)),
			seq(timed("crash", 0), timed("heal", 1), timed("elect", 5)), "hasLeader: FAIL"},
		{"hidden", "gossip.yaml", nil, []qtrace.Step{gossip(1, "a", 1, ""), gossip(2, "b", 0, "[1]")},
			[]qtrace.Step{gossip(1, "a", 1, ""), gossip(2, "b", 0, "[9]")}, "conformance: FAIL"},
		{"pending", "account.yaml", []string{"-resolve-pending"}, pending(12), pending(99), "conformance: FAIL"},
	} {
		dir := t.TempDir()
		for _, run := range []struct {
			steps []qtrace.Step
			code  int
		}{{x.pass, 0}, {x.fail, 1}} {
			log := filepath.Join(dir, fmt.Sprintf("run%d.jsonl", run.code))
			f, err := os.Create(log)
			if err != nil {
				t.Fatal(err)
			}
			if err := qtrace.WriteJSONL(f, run.steps); err != nil {
				t.Fatal(err)
			}
			f.Close()
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout := os.Stdout
			os.Stdout = w
			args := append(append([]string{"-binding", td + x.binding, "-dir", filepath.Join(dir, "out")}, x.flags...), log)
			code, err := runValidate(args)
			os.Stdout = stdout
			w.Close()
			out, _ := io.ReadAll(r)
			if err != nil || code != run.code || run.code == 1 && !strings.Contains(string(out), x.says) {
				t.Errorf("%s: exit %d, %v, want %d (saying %q):\n%s", x.name, code, err, run.code, x.says, out)
			}
		}
	}
}
