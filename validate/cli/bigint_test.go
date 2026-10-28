package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/validate"
)

// An ITF input of quint's Rust backend with integers of 16 characters
// and more (bignumber.js objects: itf's testdata) is read by its model's
// types and replays, its states equal to the model's; without a model to
// type it, the read is refused, naming the fix.
func TestITFInputReadByTypes(t *testing.T) {
	skip.Quint(t)
	const trace = "../../itf/testdata/bignumber.rust.itf.json"
	spec, err := filepath.Abs("../../itf/testdata/bignumber.qnt")
	if err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(t.TempDir(), "bignumber.yaml")
	if err := os.WriteFile(b, []byte("spec: "+spec+"\nmodule: bignumber\nactions:\n  step: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// replayed on TypeScript: Rust refuses the literal -9223372036854775808
	// (QNT600: its 9223372036854775808 is past i64), which the replay writes
	j, err := Check{Binding: b, Files: []string{trace}, Options: validate.Options{Dir: t.TempDir(), Backend: "typescript"}}.Judge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !j.OK || len(j.ITF) != 1 {
		t.Fatalf("the Rust trace, by its model's types: %d results\n%s", len(j.ITF), j.ITF[0].Summary())
	}
	_, err = Check{Files: []string{trace}, Options: validate.Options{Dir: t.TempDir()}}.Judge(context.Background())
	if _, ok := errors.AsType[*itf.AmbiguousError](err); !ok {
		t.Errorf("no model: %v", err)
	}
}
