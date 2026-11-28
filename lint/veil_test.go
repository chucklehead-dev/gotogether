package lint

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// lint -veil notes the Veil-blocking idioms of a model and, for a
// binding, the holds invariants a settle flag step never sets makes
// vacuous: notes that leave the exit at 0, errors under -veil-errors, and
// nothing without -veil.
func TestVeilNotes(t *testing.T) {
	skip.Quint(t)
	model, _ := filepath.Abs("../internal/veilfrag/testdata/settle.qnt")
	blocking := "../internal/veilfrag/testdata/blocking.qnt"
	b := filepath.Join(t.TempDir(), "settle.yaml")
	if err := os.WriteFile(b, []byte("spec: "+model+"\nmodule: settle\ninvariants: [settledCorrectly, doneCorrectly]\nsimulate: {holds: [settledCorrectly, doneCorrectly]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	veilCodes := func(r *Report) (codes []string, severities []Severity) {
		for _, f := range r.Findings {
			if len(f.Code) > 11 && f.Code[:11] == "model.veil." {
				codes = append(codes, f.Code+" "+f.Symbol)
				severities = append(severities, f.Severity)
			}
		}
		return
	}
	r := Inspect(t.Context(), Options{Bindings: []string{b}, Models: []string{blocking}, Quint: "quint", Veil: true})
	codes, sev := veilCodes(r)
	if !slices.Contains(codes, "model.veil.vacuous settledCorrectly") || slices.Contains(codes, "model.veil.vacuous doneCorrectly") || !slices.Contains(codes, "model.veil.size empty") {
		t.Errorf("codes %v", codes)
	}
	for _, s := range sev {
		if s != Info {
			t.Errorf("severity %s, want notes", s)
		}
	}
	if r.ExitCode() != 0 {
		t.Errorf("exit %d with notes: %+v", r.ExitCode(), r.Checks)
	}
	r = Inspect(t.Context(), Options{Models: []string{blocking}, Quint: "quint", VeilErrors: true})
	if r.ExitCode() != 1 {
		t.Errorf("-veil-errors: exit %d", r.ExitCode())
	}
	r = Inspect(t.Context(), Options{Models: []string{blocking}, Quint: "quint"})
	if codes, _ := veilCodes(r); len(codes) != 0 || r.ExitCode() != 0 {
		t.Errorf("without -veil: %v, exit %d", codes, r.ExitCode())
	}
}
