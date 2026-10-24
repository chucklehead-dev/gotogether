package choice

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qrun"
)

// A declaration outside any run is static, known to every run; one in a
// run goes to that run's names, and one in a run without names (Hegel's,
// a load experiment's) to neither: another test's runs must not know it.
func TestDeclaredWhere(t *testing.T) {
	Declare("test.where.static", "lost")
	if !KnownStatic("test.where.static.lost") {
		t.Errorf("a declaration outside any run is not static")
	}
	names := &hooks.FaultNames{}
	end := qrun.Start(hooks.WithFaultNames(context.Background(), names))
	Declare("test.where.named", "lost")
	end()
	if KnownStatic("test.where.named") || !names.Covers("test.where.named.lost") {
		t.Errorf("a declaration in a run: static %v, the run's %v", KnownStatic("test.where.named"), names.Covers("test.where.named.lost"))
	}
	end = qrun.Start(context.Background())
	Declare("test.where.unnamed", "lost")
	end()
	if KnownStatic("test.where.unnamed") {
		t.Errorf("a declaration in a run without names is static")
	}
}
