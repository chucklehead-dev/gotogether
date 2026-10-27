package binding_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
)

// A quint that cannot run is reported with its cause wrapped: a caller
// tells a missing CLI by errors.Is(err, exec.ErrNotFound).
func TestLoadModelWrapsQuintsError(t *testing.T) {
	spec, err := filepath.Abs("../examples/bank/model/bank.qnt")
	if err != nil {
		t.Fatal(err)
	}
	_, err = binding.LoadModel("quint-not-installed-anywhere", spec, "bank")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("%v, want a wrapped exec.ErrNotFound", err)
	}
}
