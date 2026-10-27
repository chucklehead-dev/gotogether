package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// gotogether validate reads a driver's request log (package qdriver) as
// it reads a step log, detected by its header: each request an
// invocation, completed by its own response with what the request sent
// (an answer naming another amount does not change the call's), its
// observations judged.
func TestValidateRequestLog(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	dir := t.TempDir()
	log := func(balance string) string {
		p := filepath.Join(dir, "requests-"+balance+".jsonl")
		data := `{"protocol":"gotogether.requests/1","recorder":"pw","actor":"acct"}
{"event":"request","req":"1","action":"deposit","time":"2026-10-27T09:00:00Z","args":{"amount":5},"corr":"t1"}
{"event":"response","req":"1","time":"2026-10-27T09:00:00.1Z","code":200,"args":{"amount":7},"obs":{"balance":` + balance + `}}
`
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	code, out := vcapture(t, []string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out"), log("5")})
	for _, want := range []string{"a driver's request log", "RESULT: PASS (1 actor conforms"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("exit %d, output lacks %q:\n%s", code, want, out)
		}
	}
	code, out = vcapture(t, []string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out2"), log("6")})
	for _, want := range []string{"RESULT: FAIL (1 of 1 actor: 1 mismatch after deposit)", "corr=t1"} {
		if code != 1 || !strings.Contains(out, want) {
			t.Errorf("exit %d, output lacks %q:\n%s", code, want, out)
		}
	}
}

// Two driver logs naming one recorder are refused: their records are
// numbered alike, and the second's were dropped as duplicates of the
// first's, a failing second run passing on the first's. Two step logs
// whose seqs collide with other contents are an error issue of the
// actor, never a pass.
func TestTwoLogsOneRecorder(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	dir := t.TempDir()
	write := func(name, data string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	req := func(balance string) string {
		return `{"protocol":"gotogether.requests/1","recorder":"ui","actor":"acct"}
{"event":"request","req":"1","action":"deposit","time":"2026-10-27T09:00:00Z","args":{"amount":5}}
{"event":"response","req":"1","time":"2026-10-27T09:00:00.1Z","code":200,"obs":{"balance":` + balance + `}}
`
	}
	first, second := write("first.jsonl", req("5")), write("second.jsonl", req("6"))
	code, out, err := validated([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out"), first, second})
	for _, want := range []string{"first.jsonl and " + second + ` are request logs of one recorder "ui"`, "its own recorder"} {
		if code != 2 || err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("exit %d, error %v lacks %q:\n%s", code, err, want, out)
		}
	}
	a := write("a.jsonl", `{"quint.action":"deposit","quint.seq":1,"quint.recorder":"r","quint.actor":"acct","quint.arg.amount":5,"quint.obs.balance":5}`+"\n")
	b := write("b.jsonl", `{"quint.action":"deposit","quint.seq":1,"quint.recorder":"r","quint.actor":"acct","quint.arg.amount":5,"quint.obs.balance":6}`+"\n")
	code, sout := vcapture(t, []string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out2"), a, b})
	for _, want := range []string{"recorder r seq 1 is two different records", "RESULT: FAIL"} {
		if code != 1 || !strings.Contains(sout, want) {
			t.Errorf("exit %d, output lacks %q:\n%s", code, want, sout)
		}
	}
	// the same log twice is a copy: it passes
	code, sout = vcapture(t, []string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out3"), a, write("a2.jsonl", `{"quint.action":"deposit","quint.seq":1,"quint.recorder":"r","quint.actor":"acct","quint.arg.amount":5,"quint.obs.balance":5}`+"\n")})
	if code != 0 {
		t.Errorf("a copy: exit %d:\n%s", code, sout)
	}
}
