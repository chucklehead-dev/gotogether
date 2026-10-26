package main

import (
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
		// complete: true makes it the binding's default
		{"complete", "funds_complete.yaml", nil, seq(acct("deposit", 5, 5), settled),
			seq(acct("deposit", 5, 5)), "incomplete (no settle) (the binding is complete)"},
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
			manifest := filepath.Join(dir, fmt.Sprintf("run%d.manifest.json", run.code))
			args := append(append([]string{"-binding", td + x.binding, "-dir", filepath.Join(dir, "out"), "-manifest", manifest}, x.flags...), log)
			code, err := runValidate(args)
			os.Stdout = stdout
			w.Close()
			out, _ := io.ReadAll(r)
			if err != nil || code != run.code || run.code == 1 && !strings.Contains(string(out), x.says) {
				t.Errorf("%s: exit %d, %v, want %d (saying %q):\n%s", x.name, code, err, run.code, x.says, out)
			}
			// the manifest's run judged again: the same verdict, said the same
			code, rout := capture(t, func() (int, error) {
				return runValidate([]string{"-replay", manifest, "-dir", filepath.Join(dir, "replay")})
			})
			if code != run.code || !strings.Contains(rout, "the manifest's verdict, reproduced") || run.code == 1 && !strings.Contains(rout, x.says) {
				t.Errorf("%s: replay: exit %d, want %d (saying %q):\n%s", x.name, code, run.code, x.says, rout)
			}
		}
	}
}

// -lost-suffix and -lost-prefix declare losses a log cannot show: a
// recorder's tail lost after its last step (a later step of another that
// fails may depend on it: no verdict), a log without seqs whose head was
// lost (judged from its snapshot, else an error); a recorder the log
// lacks is an error naming those it has. A torn log says what its lost
// tail leaves unjudged.
func TestValidateLost(t *testing.T) {
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
	for _, c := range []struct {
		name    string
		args    []string
		code    int
		want    string
		binding string
	}{
		{"as recorded", []string{two}, 1, "RESULT: FAIL (1 of 1 actor: 1 rejects withdraw)", "account.yaml"},
		{"a's tail lost", []string{"-lost-suffix", "a", two}, 1, "RESULT: FAIL (1 of 1 actor: 1 no verdict (lost steps))", "account.yaml"},
		{"no such recorder", []string{"-lost-suffix", "z", two}, 2, `validate: -lost-suffix z: no step of recorder "z" in the input: name one of its recorders (a, b)`, "account.yaml"},
		{"a snapshot's head lost", []string{"-lost-prefix", "c", snap}, 0, "judged from the state recorded by restore at seq 0 (recorder c lost its first steps)", "snapshot.yaml"},
		{"a head lost", []string{"-lost-prefix", "c", snap}, 1, "error lost-prefix: recorder c lost its first steps", "account.yaml"},
		{"torn", []string{torn}, 0, "its recorders' tails are lost after their last steps: eventual vals not judged", "account.yaml"},
	} {
		args := append([]string{"-binding", "../../validate/testdata/" + c.binding, "-dir", filepath.Join(dir, "out")}, c.args...)
		code, out := capture(t, func() (int, error) {
			code, err := runValidate(args)
			if err != nil {
				fmt.Println(err)
			}
			return code, nil
		})
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
	_, out := capture(t, func() (int, error) { return 0, toITF([]string{"-binding", bind, "-out", dir, log}) })
	if strings.Contains(out, "error lost-prefix") || !strings.Contains(out, "judged from the state recorded by restore at seq 3") {
		t.Errorf("itf: %s", out)
	}
	for _, in := range []string{log, filepath.Join(dir, "default.itf.json")} {
		code, out := capture(t, func() (int, error) {
			return runValidate([]string{"-binding", bind, "-dir", filepath.Join(dir, "out"), in})
		})
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
	code, out := capture(t, func() (int, error) {
		return runValidate([]string{"-binding", "../../validate/testdata/resume.yaml", "-dir", filepath.Join(dir, "out"), log})
	})
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
	code, out := capture(t, func() (int, error) {
		return runValidate([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out"), "-manifest", manifest, "-run-info", info, log})
	})
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
	if _, err := runValidate([]string{"-replay", manifest, "-linearize"}); err == nil || !strings.Contains(err.Error(), "-linearize: the manifest says") {
		t.Errorf("-replay -linearize: %v", err)
	}
	// a verdict that is not the manifest's: exit 1, saying both
	m.Verdict.Verdict, m.Verdict.Reason, m.Verdict.Failing, m.Verdict.Actors = validate.Pass, "", nil, []validate.ActorVerdict{{Actor: "default", Verdict: validate.Pass}}
	edited := filepath.Join(dir, "edited.json")
	if err := m.Write(edited); err != nil {
		t.Fatal(err)
	}
	code, out = capture(t, func() (int, error) {
		return runValidate([]string{"-replay", edited, "-dir", filepath.Join(dir, "replay")})
	})
	if code != 1 || !strings.Contains(out, "the verdict is not the manifest's") || !strings.Contains(out, "manifest: PASS") {
		t.Errorf("an edited verdict: exit %d:\n%s", code, out)
	}
	// the suite's evidence lists it, an external run
	code, out = capture(t, func() (int, error) { return evidence([]string{dir}) })
	if code != 0 || !strings.Contains(out, "nightly: 1 runs, 0 conform, 1 mismatch after deposit (external: not counted below)") {
		t.Errorf("evidence: exit %d:\n%s", code, out)
	}
	checks := filepath.Join(dir, "checks.txt")
	os.WriteFile(checks, []byte("nightly\n"), 0o644)
	if code, out = capture(t, func() (int, error) { return evidence([]string{"-expect", checks, manifest}) }); code != 0 {
		t.Errorf("evidence -expect: exit %d:\n%s", code, out)
	}
	// a changed log: refused, naming it, with the fix
	os.WriteFile(log, []byte(strings.Replace(b.String(), `"quint.obs.balance":6`, `"quint.obs.balance":5`, 1)), 0o644)
	if _, err := runValidate([]string{"-replay", manifest}); err == nil || !strings.Contains(err.Error(), "the input "+log+" changed since the run was judged") {
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
	manifest := filepath.Join(dir, "cmd", "gotogether", t.Name(), "run1.json")
	code, out := capture(t, func() (int, error) {
		return runValidate([]string{"-replay", manifest, "-dir", filepath.Join(dir, "replay")})
	})
	if code != 0 || !strings.Contains(out, "the manifest's verdict, reproduced: PASS") {
		t.Errorf("replay: exit %d:\n%s", code, out)
	}
	// -expect names it as it names an exploration: by its package's
	// import path (or its end) and its test
	checks := filepath.Join(t.TempDir(), "checks.txt")
	os.WriteFile(checks, []byte("github.com/chucklehead-dev/gotogether/cmd/gotogether "+t.Name()+"\ncmd/gotogether "+t.Name()+"\n"), 0o644)
	if code, out := capture(t, func() (int, error) { return evidence([]string{"-expect", checks, dir}) }); code != 0 || !strings.Contains(out, "(external: not counted below)") {
		t.Errorf("evidence -expect: exit %d:\n%s", code, out)
	}
}
