package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/formats"
)

// An older strict reader must refuse directed coverage rather than ignore its
// unmet obligations. The current reader likewise has no legacy adapter.
func TestDirectedFaultCoverageRequiresCurrentEvidence(t *testing.T) {
	old := formats.Format{Thing: "evidence", Major: 2, What: "exploration evidence"}
	if _, err := old.Check(Format); err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Fatalf("format2 reader could accept current directed coverage: %v", err)
	}
	path := filepath.Join(t.TempDir(), "old.json")
	if err := os.WriteFile(path, []byte(`{"format":"gotogether.evidence/2","runs":1,"conform":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := Read(path); err == nil || got != nil || !strings.Contains(err.Error(), "run the tests again") {
		t.Fatalf("legacy evidence was adapted: %+v %v", got, err)
	}
	if !Is(path) {
		t.Fatal("older evidence must still be discovered so its refusal is visible")
	}
}
