package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestChoicesValidateFlagRecordsPolicy(t *testing.T) {
	skip.Quint(t)
	manifest := filepath.Join(t.TempDir(), "run.json")
	steps := []qtrace.Step{{Action: "deposit", Recorder: "r", Seq: 1, Time: time.Unix(1, 0), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}}
	code, err, out := validateSteps(t, steps, "-binding", "../../validate/testdata/account.yaml", "-choice-limit", "7", "-manifest", manifest)
	if err != nil || code != 0 {
		t.Fatalf("validate: code %d, %v\n%s", code, err, out)
	}
	m, err := validate.ReadManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if m.Check.ChoiceLimit != 7 {
		t.Fatalf("recorded CLI limit: %d", m.Check.ChoiceLimit)
	}
	if _, _, err := validated([]string{"-replay", manifest, "-choice-limit", "7"}); err == nil || !strings.Contains(err.Error(), "-choice-limit: the manifest says") {
		t.Fatalf("CLI replay policy override: %v", err)
	}
}
