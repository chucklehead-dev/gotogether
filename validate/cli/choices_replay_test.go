package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestChoicesReplayPreservesRecordedPolicy(t *testing.T) {
	skip.Quint(t)
	binding, _ := filepath.Abs("../testdata/account.yaml")
	j, err := Check{Binding: binding, Files: []string{accountRun(t, t.TempDir(), 5)}, Options: validate.Options{ChoiceLimit: 7}}.Judge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	m, err := j.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.Check.ChoiceLimit != 7 {
		t.Fatalf("manifest limit: %d", m.Check.ChoiceLimit)
	}
	if _, err := Replay(context.Background(), m, validate.Options{ChoiceLimit: 8}); err == nil || !strings.Contains(err.Error(), "recorded limit") {
		t.Fatalf("policy override: %v", err)
	}
	r, err := Replay(context.Background(), m, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Got.Close()
	if !r.Same {
		t.Fatalf("replay differs: %s", r.Differs)
	}
	got, err := r.Got.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if got.Check.ChoiceLimit != 7 {
		t.Fatalf("replayed policy: %d", got.Check.ChoiceLimit)
	}
}
