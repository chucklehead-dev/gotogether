package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// validate ends with how many traces fail and why, in the claims' words:
// it said "VIOLATION" for any failure, a mismatch included.
func TestValidateResultLine(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
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
	code, out, err := validated([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out"), log})
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
	code, out, err = validated([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out2"), log})
	if want := "RESULT: PASS (1 actor conforms to the model and satisfies its 2 invariants)"; err != nil || code != 0 || !strings.Contains(string(out), want) {
		t.Errorf("exit %d, %v; output lacks %q:\n%s", code, err, want, out)
	}
}

// -linearize checks a client history in an order real time allows: a
// withdrawal that overlapped the deposit it depended on returned first,
// which recorded order rejects and the order the intervals allow accepts.
func TestValidateLinearize(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
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
		args := append(append([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out")}, c.flags...), log)
		code, out, err := validated(args)
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
	t.Parallel()
	skip.Quint(t)
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
		// -require-settle: a conforming run that is no finished one fails,
		// saying what it lacks, as qtest.Finishes does (without the flag,
		// the "eventual" row's unsettled run passes)
		{"require-settle: no settle", "funds.binding.yaml", []string{"-require-settle"}, seq(acct("deposit", 5, 5), settled),
			seq(acct("deposit", 5, 5)), "1 of 1 actor: 1 incomplete (no settle)"},
		{"require-settle: pending", "funds.binding.yaml", []string{"-require-settle"}, seq(acct("deposit", 5, 5), settled),
			seq(acct("deposit", 5, 5), qtrace.Step{Action: "withdraw", Invocation: true, Args: map[string]any{"amount": int64(3)}}, settled),
			"1 of 1 actor: 1 incomplete (pending calls)"},
		// a call -resolve-pending places was still never answered
		{"require-settle: resolved pending", "funds.binding.yaml", []string{"-require-settle", "-resolve-pending"},
			seq(acct("deposit", 5, 5), settled), append(pending(12), qtrace.Step{Action: "settled", Settled: true, Recorder: "c", Seq: 4, Time: ms(4)}),
			"1 of 1 actor: 1 incomplete (pending calls)"},
		// the checked invariants and declared losses are the manifest's too:
		// without them, a replay judges the passing runs otherwise
		{"invariants", "account.yaml", []string{"-invariants", "nonNegative"}, seq(acct("deposit", 150, 150)), seq(acct("deposit", 5, 6)), "conformance: FAIL"},
		{"lost suffix", "funds.binding.yaml", []string{"-lost-suffix", "r"}, seq(acct("deposit", 5, 5), acct("withdraw", 5, 0), settled),
			seq(acct("deposit", 5, 6)), "conformance: FAIL"},
		// -complete-through: a watermark past the window judges a run that
		// never settled, its trace ended before the window (without it the
		// failing run passes, the obligation open); one before the window
		// leaves a trace that went on past it no verdict (without it, a
		// FAIL): absent, uncollected
		{"complete-through: past the window", "within.yaml", []string{"-complete-through", "r=10000000000"}, seq(timed("crash", 0), timed("heal", 1), timed("elect", 2)),
			seq(timed("crash", 0), timed("heal", 1), timed("tick", 2)), "hasLeader: FAIL"},
		// (steps without seqs, converted spans: dense seqs past the watermark
		// would say the steps there arrived)
		{"complete-through: before the window", "within.yaml", []string{"-complete-through", "*=1970-01-01T00:00:03Z"}, seq(timed("crash", 0), timed("heal", 1), timed("elect", 2)),
			[]qtrace.Step{{Action: "crash", Recorder: "r", Time: at(0)}, {Action: "heal", Recorder: "r", Time: at(1)}, {Action: "tick", Recorder: "r", Time: at(6)}}, "1 no verdict (uncollected steps)"},
		// complete: true makes it the binding's default
		{"complete", "funds_complete.yaml", nil, seq(acct("deposit", 5, 5), settled),
			seq(acct("deposit", 5, 5)), "incomplete (no settle) (the binding is complete)"},
	} {
		t.Run(x.name, func(t *testing.T) {
			t.Parallel() // each row its own directory, its report its own buffer
			matrixRow(t, x.binding, x.flags, x.pass, x.fail, x.says)
		})
	}
}

// matrixRow is one row of the CLI's verdict matrix: its passing and
// failing run validated with a manifest, and each judged again from it.
// Under -short a passing run is replayed only where something shapes a
// pass besides the steps (the row's flags, a complete binding): the
// failing runs replay every kind of verdict.
func matrixRow(t *testing.T, bind string, flags []string, pass, fail []qtrace.Step, says string) {
	td := "../../validate/testdata/"
	dir := t.TempDir()
	for _, run := range []struct {
		steps []qtrace.Step
		code  int
	}{{pass, 0}, {fail, 1}} {
		log := filepath.Join(dir, fmt.Sprintf("run%d.jsonl", run.code))
		f, err := os.Create(log)
		if err != nil {
			t.Fatal(err)
		}
		if err := qtrace.WriteJSONL(f, run.steps); err != nil {
			t.Fatal(err)
		}
		f.Close()
		manifest := filepath.Join(dir, fmt.Sprintf("run%d.manifest.json", run.code))
		args := append(append([]string{"-binding", td + bind, "-dir", filepath.Join(dir, "out"), "-manifest", manifest}, flags...), log)
		code, out, err := validated(args)
		if err != nil || code != run.code || run.code == 1 && !strings.Contains(string(out), says) {
			t.Errorf("exit %d, %v, want %d (saying %q):\n%s", code, err, run.code, says, out)
		}
		if run.code == 0 && testing.Short() && len(flags) == 0 && !strings.Contains(bind, "complete") {
			continue
		}
		// the manifest's run judged again: the same verdict, said the same
		code, rout := vcapture(t, []string{"-replay", manifest, "-dir", filepath.Join(dir, "replay")})
		if code != run.code || !strings.Contains(rout, "the manifest's verdict, reproduced") || run.code == 1 && !strings.Contains(rout, says) {
			t.Errorf("replay: exit %d, want %d (saying %q):\n%s", code, run.code, says, rout)
		}
	}
}

// -lost-suffix and -lost-prefix declare losses a log cannot show: a
// recorder's tail lost after its last step (a later step of another that
// fails may depend on it: no verdict), a log without seqs whose head was
// lost (judged from its snapshot, else an error); a recorder the log
// lacks is an error naming those it has. A torn log says what its lost
// tail leaves unjudged; a gap's mismatch after it is no verdict.
func TestValidateLost(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	dir := t.TempDir()
	at := func(ms int) time.Time { return time.Unix(0, int64(ms)*int64(time.Millisecond)) }
	write := func(name string, steps []qtrace.Step, cut int) string {
		t.Helper()
		var b strings.Builder
		if err := qtrace.WriteJSONL(&b, steps); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(b.String()[:b.Len()-cut]), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	acct := func(rec string, seq uint64, ms int, action string, args, obs map[string]any) qtrace.Step {
		return qtrace.Step{Action: action, Recorder: rec, Process: "p", Seq: seq, Time: at(ms), Args: args, Obs: obs}
	}
	amount := func(a, balance int64) (map[string]any, map[string]any) {
		return map[string]any{"amount": a}, map[string]any{"balance": balance}
	}
	da, db := amount(5, 5)
	wa, wb := amount(10, 0)
	two := write("two.jsonl", []qtrace.Step{acct("a", 1, 10, "deposit", da, db), acct("b", 1, 30, "withdraw", wa, wb)}, 0)
	ra, rb := amount(5, 55)
	snap := write("snap.jsonl", []qtrace.Step{acct("c", 0, 10, "restore", map[string]any{"b": int64(50), "n": int64(1)}, nil), acct("c", 0, 20, "deposit", ra, rb)}, 0)
	torn := write("torn.jsonl", []qtrace.Step{acct("a", 1, 10, "deposit", da, db), acct("a", 2, 20, "deposit", da, db)}, 10)
	// a review's log: seq 3 dropped, the balance after it counting its
	// deposit: no verdict (dropped steps), once a FAIL (DESIGN §53)
	dep := func(seq uint64, balance int64) qtrace.Step {
		a, o := amount(5, balance)
		return acct("a", seq, int(seq)*10, "deposit", a, o)
	}
	gap := write("gap.jsonl", []qtrace.Step{dep(1, 5), dep(2, 10), dep(4, 20), {Action: "settled", Settled: true, Recorder: "a", Process: "p", Seq: 5, Time: at(50)}}, 0)
	for _, c := range []struct {
		name    string
		args    []string
		code    int
		want    string
		binding string
	}{
		{"as recorded", []string{two}, 1, "RESULT: FAIL (1 of 1 actor: 1 rejects withdraw)", "account.yaml"},
		{"a's tail lost", []string{"-lost-suffix", "a", two}, 1, "RESULT: NO VERDICT (1 of 1 actor: 1 no verdict (lost steps))", "account.yaml"},
		{"no such recorder", []string{"-lost-suffix", "z", two}, 2, `validate: -lost-suffix z: no step of recorder "z" in the input: name one of its recorders (a, b)`, "account.yaml"},
		{"no such recorder's watermark", []string{"-complete-through", "z=1", two}, 2, `validate -complete-through z=1: no step of recorder "z" in the input: name one of its recorders (a, b), or * for every one`, "account.yaml"},
		{"no time", []string{"-complete-through", "a=soon", two}, 2, `validate -complete-through a=soon: "soon" is no time: give RFC3339 (2026-10-26T12:00:00Z) or Unix nanoseconds`, "account.yaml"},
		{"no recorder", []string{"-complete-through", "1", two}, 2, `validate -complete-through "1": want recorder=T`, "account.yaml"},
		{"a snapshot's head lost", []string{"-lost-prefix", "c", snap}, 0, "judged from the state recorded by restore at seq 0 (recorder c lost its first steps)", "snapshot.yaml"},
		{"a head lost", []string{"-lost-prefix", "c", snap}, 1, "error lost-prefix: recorder c lost its first steps", "account.yaml"},
		{"a gap", []string{gap}, 1, "RESULT: NO VERDICT (1 of 1 actor: 1 no verdict (dropped steps))", "funds.binding.yaml"},
		{"torn", []string{torn}, 0, "its recorders' tails are lost after their last steps: eventual vals not judged", "account.yaml"},
	} {
		args := append([]string{"-binding", "../../validate/testdata/" + c.binding, "-dir", filepath.Join(dir, "out")}, c.args...)
		code, b, err := validated(args)
		out := string(b)
		if err != nil {
			out += err.Error()
		}
		if code != c.code || !strings.Contains(out, c.want) {
			t.Errorf("%s: exit %d, want %d and %q:\n%s", c.name, code, c.code, c.want, out)
		}
	}
}

// A log that lost its head, written as model-level ITF, starts at its
// snapshot's state (state 0 is the snapshot), so validating the ITF gives
// the steps' verdict: a deposit of 5 that saw 5 after restore(50) is no
// behaviour from there, whatever it is from init. The itf command says
// where the trace starts, not the lost prefix's error.
func TestITFSnapshotStart(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	dir := t.TempDir()
	steps := []qtrace.Step{
		{Action: "restore", Recorder: "r", Process: "p", Seq: 3, Time: time.Unix(1, 0), Args: map[string]any{"b": int64(50), "n": int64(3)}},
		{Action: "deposit", Recorder: "r", Process: "p", Seq: 4, Time: time.Unix(2, 0), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}},
	}
	log := filepath.Join(dir, "run.jsonl")
	var b strings.Builder
	if err := qtrace.WriteJSONL(&b, steps); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	bind := "../../validate/testdata/snapshot.yaml"
	var itfOut strings.Builder
	if err := itfTo(&itfOut, []string{"-binding", bind, "-out", dir, log}); err != nil {
		t.Fatal(err)
	}
	out := itfOut.String()
	if strings.Contains(out, "error lost-prefix") || !strings.Contains(out, "judged from the state recorded by restore at seq 3") {
		t.Errorf("itf: %s", out)
	}
	for _, in := range []string{log, filepath.Join(dir, "default.itf.json")} {
		code, out := vcapture(t, []string{"-binding", bind, "-dir", filepath.Join(dir, "out"), in})
		if code != 1 || !strings.Contains(out, "mismatch after deposit") {
			t.Errorf("validate %s: exit %d:\n%s", filepath.Base(in), code, out)
		}
	}
}

// A log that lost its head, checked against a binding with a resume,
// passes from a state the resume admits, named in the report; steps
// lists what each recorder lost.
func TestValidateResume(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	recorded := []qtrace.Step{
		{Action: "deposit", Recorder: "r", Process: "p", Seq: 3, Time: time.Unix(1, 0), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(8)}},
		{Action: "withdraw", Recorder: "r", Process: "p", Seq: 4, Time: time.Unix(2, 0), Args: map[string]any{"amount": int64(2)}, Obs: map[string]any{"balance": int64(6)}},
	}
	log := filepath.Join(dir, "run.jsonl")
	var b strings.Builder
	if err := qtrace.WriteJSONL(&b, recorded); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := vcapture(t, []string{"-binding", "../../validate/testdata/resume.yaml", "-dir", filepath.Join(dir, "out"), log})
	if code != 0 || !strings.Contains(out, "the steps pass from balance = 3, ops = 0, a state resumeAny admits (quint confirms)") {
		t.Errorf("validate: exit %d:\n%s", code, out)
	}
	_, out = capture(t, func() (int, error) { return 0, steps([]string{log}) })
	if !strings.Contains(out, "lost: recorder r lost seqs 1..2") {
		t.Errorf("steps:\n%s", out)
	}
}

// validate -manifest writes what the driver's -run-info says (name,
// driver, artifacts hashed); -replay refuses a changed input, flags the
// manifest decides, and a verdict that is not the manifest's (exit 1);
// gotogether evidence lists the manifest as an external run, which
// -expect counts.
func TestValidateManifest(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "run.jsonl")
	steps := []qtrace.Step{{Action: "deposit", Recorder: "r", Seq: 1, Time: time.Unix(1, 0), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(6)},
		Ref: "bombadil/trace.jsonl:42"}}
	var b strings.Builder
	if err := qtrace.WriteJSONL(&b, steps); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(log, []byte(b.String()), 0o644)
	os.WriteFile(filepath.Join(dir, "42.webp"), []byte("pixels"), 0o644)
	info := filepath.Join(dir, "info.json")
	os.WriteFile(info, []byte(`{"name": "nightly", "driver": {"name": "bombadil", "version": "0.3", "seed": "42", "origin": "ci", "args": ["--headless"]},
		"artifacts": [{"name": "screenshot", "path": "42.webp"}]}`), 0o644)
	manifest := filepath.Join(dir, "m.json")
	code, out := vcapture(t, []string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out"), "-manifest", manifest, "-run-info", info, log})
	if code != 1 || !strings.Contains(out, "ref:      bombadil/trace.jsonl:42") {
		t.Fatalf("validate: exit %d:\n%s", code, out)
	}
	m, err := validate.ReadManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "nightly" || m.Driver == nil || m.Driver.Origin != "ci" || len(m.Artifacts) != 1 || m.Artifacts[0].Path != filepath.Join(dir, "42.webp") || m.Artifacts[0].SHA256 == "" ||
		m.Verdict.Failing == nil || m.Verdict.Failing.Ref != "bombadil/trace.jsonl:42" || m.Tools.Quint == "" || m.Tools.Gotogether == "" {
		t.Errorf("manifest: %+v", m)
	}
	// a flag the manifest decides
	if _, _, err := validated([]string{"-replay", manifest, "-linearize"}); err == nil || !strings.Contains(err.Error(), "-linearize: the manifest says") {
		t.Errorf("-replay -linearize: %v", err)
	}
	// a verdict that is not the manifest's: exit 1, saying both
	m.Verdict.Verdict, m.Verdict.Reason, m.Verdict.Failing, m.Verdict.Actors = validate.Pass, "", nil, []validate.ActorVerdict{{Actor: "default", Verdict: validate.Pass}}
	edited := filepath.Join(dir, "edited.json")
	if err := m.Write(edited); err != nil {
		t.Fatal(err)
	}
	code, out = vcapture(t, []string{"-replay", edited, "-dir", filepath.Join(dir, "replay")})
	if code != 1 || !strings.Contains(out, "the verdict is not the manifest's") || !strings.Contains(out, "manifest: PASS") || !strings.Contains(out, "differs:  the verdict is PASS, now FAIL") {
		t.Errorf("an edited verdict: exit %d:\n%s", code, out)
	}
	// the suite's evidence lists it, an external run
	code, out = capture(t, func() (int, error) { return evidence([]string{dir}) })
	if code != 0 || !strings.Contains(out, "nightly: 1 runs, 0 conform, 1 mismatch after deposit (external: not counted below)") {
		t.Errorf("evidence: exit %d:\n%s", code, out)
	}
	// a later version's manifest is skipped, named, not the suite's end
	os.WriteFile(filepath.Join(dir, "future.json"), []byte(`{"format": "gotogether.run/9"}`), 0o644)
	if code, out = capture(t, func() (int, error) { return evidence([]string{dir}) }); code != 0 || !strings.Contains(out, `warning: `) || !strings.Contains(out, `"gotogether.run/9"`) {
		t.Errorf("evidence with a future manifest: exit %d:\n%s", code, out)
	}
	os.Remove(filepath.Join(dir, "future.json"))
	checks := filepath.Join(dir, "checks.txt")
	os.WriteFile(checks, []byte("nightly\n"), 0o644)
	if code, out = capture(t, func() (int, error) { return evidence([]string{"-expect", checks, manifest}) }); code != 0 {
		t.Errorf("evidence -expect: exit %d:\n%s", code, out)
	}
	// a changed log: refused, naming it, with the fix
	os.WriteFile(log, []byte(strings.Replace(b.String(), `"quint.obs.balance":6`, `"quint.obs.balance":5`, 1)), 0o644)
	if _, _, err := validated([]string{"-replay", manifest}); err == nil || !strings.Contains(err.Error(), "the input "+log+" changed since the run was judged") {
		t.Errorf("a changed log: %v", err)
	}
}

// A test's manifest (qtest under GOTOGETHER_EVIDENCE) is replayed by the
// CLI with the test's instance: its constants override the binding's
// (LIMIT 120 admits a deposit of 110, which LIMIT 100 does not).
func TestReplayTestManifest(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	t.Setenv("GOTOGETHER_EVIDENCE", dir)
	steps := []qtrace.Step{{Action: "deposit", Recorder: "r", Seq: 1, Args: map[string]any{"amount": int64(110)}, Obs: map[string]any{"balance": int64(110)}}}
	qtest.Validate(t, "../../validate/testdata/account.yaml", steps, qtest.Constants(map[string]string{"LIMIT": "120"})).Expect(t, qtest.Conforms())
	// a complete binding: Validate judges no finish check (a test claims
	// Finishes), and its manifest says so: the replay judges none either
	unsettled := []qtrace.Step{{Action: "deposit", Recorder: "r", Seq: 1, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}}
	qtest.Validate(t, "../../validate/testdata/funds_complete.yaml", unsettled).Expect(t, qtest.Conforms())
	// the evidence directory, moved elsewhere, replays from its own
	// files: they are bundled beside each manifest
	moved := filepath.Join(t.TempDir(), "ev")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	dir = moved
	for _, n := range []string{"run1.json", "run2.json"} {
		manifest := filepath.Join(dir, "cmd", "gotogether", t.Name(), n)
		code, out := vcapture(t, []string{"-replay", manifest, "-dir", filepath.Join(dir, "replay")})
		if code != 0 || !strings.Contains(out, "the manifest's verdict, reproduced: PASS") || strings.Contains(out, "validate/testdata") {
			t.Errorf("replay %s: exit %d:\n%s", n, code, out)
		}
	}
	// -expect names it as it names an exploration: by its package's
	// import path (or its end) and its test
	checks := filepath.Join(t.TempDir(), "checks.txt")
	os.WriteFile(checks, []byte("github.com/chucklehead-dev/gotogether/cmd/gotogether "+t.Name()+"\ncmd/gotogether "+t.Name()+"\n"), 0o644)
	if code, out := capture(t, func() (int, error) { return evidence([]string{"-expect", checks, dir}) }); code != 0 || !strings.Contains(out, "(external: not counted below)") {
		t.Errorf("evidence -expect: exit %d:\n%s", code, out)
	}
}

// An ITF input's manifest records the instance its trace was judged
// against (the spec it names, hashed with its imports; its constants and
// invariants): a changed spec is refused on replay. A replay judges on
// the run's backend: another is refused.
func TestValidateManifestITF(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	dir := t.TempDir()
	for _, f := range []string{"account.yaml", "account.qnt"} {
		b, err := os.ReadFile(filepath.Join("../../validate/testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, f), b, 0o644)
	}
	var b strings.Builder
	qtrace.WriteJSONL(&b, []qtrace.Step{{Action: "deposit", Recorder: "r", Seq: 1, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}})
	log := filepath.Join(dir, "run.jsonl")
	os.WriteFile(log, []byte(b.String()), 0o644)
	if err := itfTo(io.Discard, []string{"-binding", filepath.Join(dir, "account.yaml"), "-out", filepath.Join(dir, "itf"), log}); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "m.json")
	code, out := vcapture(t, []string{"-dir", filepath.Join(dir, "out"), "-manifest", manifest, filepath.Join(dir, "itf", "default.itf.json")})
	if code != 0 {
		t.Fatalf("validate: exit %d:\n%s", code, out)
	}
	m, err := validate.ReadManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	md := m.Inputs[0].Model
	if md == nil || md.Spec == nil || md.Spec.Path != filepath.Join(dir, "account.qnt") || md.Module != "account" || len(md.Invariants) != 2 || md.Constants["LIMIT"] != "100" {
		t.Fatalf("the ITF input's model: %+v", md)
	}
	// moved whole: the trace's own spec path is gone, the manifest's is judged
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	dir, manifest = moved, filepath.Join(moved, "m.json")
	code, out = vcapture(t, []string{"-replay", manifest, "-dir", filepath.Join(dir, "replay")})
	if code != 0 || !strings.Contains(out, "reproduced: PASS") {
		t.Errorf("replay: exit %d:\n%s", code, out)
	}
	if _, _, err := validated([]string{"-replay", manifest, "-backend", "typescript"}); err == nil || !strings.Contains(err.Error(), "verdicts depend on it: drop -backend") {
		t.Errorf("another backend: %v", err)
	}
	f, _ := os.OpenFile(filepath.Join(dir, "account.qnt"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("\n")
	f.Close()
	if _, _, err := validated([]string{"-replay", manifest}); err == nil || !strings.Contains(err.Error(), "is not the one the run was judged on") {
		t.Errorf("a changed spec: %v", err)
	}
}

// validated runs the validate command with its report in a buffer, so
// its tests run in parallel (capture swaps the process's stdout).
func validated(args []string) (int, []byte, error) {
	var b bytes.Buffer
	code, err := validateTo(&b, args)
	return code, b.Bytes(), err
}

// vcapture is validated for a run that must not fail with an error.
func vcapture(t *testing.T, args []string) (int, string) {
	t.Helper()
	code, out, err := validated(args)
	if err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	return code, string(out)
}

// Without -dir validate's temporary directory is made only when there is
// something to judge, removed when the run passes and kept, named, when
// it fails: one was left behind by every run, and made before a missing
// -binding was refused. (Not parallel: it sets TMPDIR.)
func TestValidateTempDir(t *testing.T) {
	skip.Quint(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	left := func() []string {
		m, _ := filepath.Glob(filepath.Join(tmp, "gotogether-validate-*"))
		return m
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "run.jsonl")
	write := func(steps ...qtrace.Step) {
		t.Helper()
		f, err := os.Create(log)
		if err != nil {
			t.Fatal(err)
		}
		if err := qtrace.WriteJSONL(f, steps); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	step := qtrace.Step{Action: "deposit", Actor: "a", Recorder: "a", Thread: "a", Seq: 1,
		Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}
	write(step)
	if code, out, err := validated([]string{log}); code != 2 || err == nil || !strings.Contains(err.Error(), "recorded steps need -binding") || strings.Contains(string(out), "artifacts") {
		t.Errorf("no -binding: exit %d, %v:\n%s", code, err, out)
	}
	if l := left(); len(l) > 0 {
		t.Errorf("a refused run left %v", l)
	}
	if code, out := vcapture(t, []string{"-binding", "../../validate/testdata/account.yaml", log}); code != 0 || !strings.Contains(out, "removed if the run passes") {
		t.Errorf("a passing run: exit %d:\n%s", code, out)
	}
	if l := left(); len(l) > 0 {
		t.Errorf("a passing run left %v", l)
	}
	// -itf keeps the ITF it names, a replay's too: a passing replay once
	// removed the directory its report had just printed
	manifest := filepath.Join(dir, "m.json")
	if code, out := vcapture(t, []string{"-binding", "../../validate/testdata/account.yaml", "-manifest", manifest, log}); code != 0 {
		t.Fatalf("a passing run's manifest: exit %d:\n%s", code, out)
	}
	for _, args := range [][]string{{"-binding", "../../validate/testdata/account.yaml", "-itf", log}, {"-replay", manifest, "-itf"}} {
		code, out := vcapture(t, args)
		l := left()
		itfs, _ := filepath.Glob(filepath.Join(tmp, "gotogether-validate-*", "*", "*.itf.json"))
		if code != 0 || len(l) != 1 || !strings.Contains(out, l[0]+" (kept: -itf)") || len(itfs) == 0 {
			t.Errorf("%v: exit %d, left %v, ITF %v:\n%s", args, code, l, itfs, out)
		}
		for _, d := range l {
			os.RemoveAll(d)
		}
	}
	if code, out := vcapture(t, []string{"-replay", manifest}); code != 0 || len(left()) > 0 {
		t.Errorf("a passing replay: exit %d, left %v:\n%s", code, left(), out)
	}
	step.Obs = map[string]any{"balance": int64(6)} // a mismatch
	write(step)
	code, out := vcapture(t, []string{"-binding", "../../validate/testdata/account.yaml", log})
	l := left()
	if code != 1 || len(l) != 1 || !strings.Contains(out, l[0]) {
		t.Errorf("a failing run: exit %d, left %v:\n%s", code, l, out)
	}
}
