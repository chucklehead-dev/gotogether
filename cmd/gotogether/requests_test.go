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
