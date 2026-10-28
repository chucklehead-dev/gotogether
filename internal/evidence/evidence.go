// Package evidence reads an exploration's evidence (qtest.Evidence's
// JSON) for qtest and the CLI alike: its format, its roles, the check
// that a file is evidence, and View, what `gotogether evidence` reads of
// it. qtest writes the whole Evidence, with qenv's and qcover's types in
// it; the CLI imports neither qtest nor qenv, and reads the view
// (qtest's tests pin it to Evidence's JSON).
package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/formats"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Format is the format qtest.Evidence.WriteJSON writes and Read reads.
// Bump it when a field's name or meaning changes (qenv.Coverage's and
// qcover.Summary's, which Evidence embeds, too), so old files are
// refused, not misread.
const Format = "gotogether.evidence/2" // 2: qenv.Coverage's keys in lower case, Package

// Evidence roles (qtest.Evidence.Role).
const (
	RoleFaithful = "faithful"
	RoleMutant   = "mutant"
	RoleReplay   = "replay"
	RoleExternal = "external"
)

// View is what the evidence command reads of an exploration's evidence:
// the fields of qtest.Evidence's JSON it reports on.
type View struct {
	Format    string         `json:"format"`
	Role      string         `json:"role"`
	Package   string         `json:"package,omitempty"`
	Import    string         `json:"import,omitempty"`
	Test      string         `json:"test,omitempty"`
	Spec      string         `json:"spec,omitempty"`
	Runs      int            `json:"runs"`
	Conform   int            `json:"conform"`
	FailingBy map[string]int `json:"failing"`
	Faults    *struct {
		Points []struct {
			Kind     string   `json:"kind"`
			Sites    []string `json:"sites,omitempty"`
			Runs     int      `json:"runs"`
			Injected []int    `json:"injected"`
		} `json:"points"`
	} `json:"faults,omitempty"`
	Model *struct {
		Taken map[string]int `json:"taken"`
		Never []string       `json:"never,omitempty"`
	} `json:"model,omitempty"`
	Exhaust *struct {
		Delays   int  `json:"delays"`
		Faults   int  `json:"faults"`
		Complete bool `json:"complete"`
	} `json:"search,omitempty"`
	Sample *struct {
		Runs  int     `json:"runs"`
		Rate  float64 `json:"rate"`
		OneIn int     `json:"oneIn"`
	} `json:"sample,omitempty"`
	Judges *struct {
		Evaluator int `json:"evaluator"`
		Confirmed int `json:"confirmed"`
	} `json:"judges,omitempty"`
}

// Faulted counts the faults of a point's options taken, nominal (0)
// aside (qenv.PointCoverage.Faulted).
func Faulted(injected []int) int {
	n := 0
	for i, c := range injected {
		if i > 0 {
			n += c
		}
	}
	return n
}

// Decode reads the evidence at path into v (a *qtest.Evidence, a
// *View): a file of another format, or none, is refused with the fix.
func Decode(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var head struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return fmt.Errorf("%s: %w (want the JSON of a qtest.Evidence)", path, err)
	}
	if _, err := formats.Evidence.Check(head.Format); err != nil {
		if formats.Evidence.Is(head.Format) {
			return fmt.Errorf("%s: evidence %w", path, err)
		}
		return fmt.Errorf("%s: evidence of format %q, not %q: run the tests again to write it anew", path, head.Format, Format)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w (want the JSON of a qtest.Evidence)", path, err)
	}
	return nil
}

// Read reads the view of the evidence at path (Decode).
func Read(path string) (*View, error) {
	var v View
	if err := Decode(path, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Is reports whether the JSON file at path is evidence of any format
// version: what a tool walking a directory reads, and skips the rest
// (ITF traces, corpus entries, summaries).
func Is(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	// the format is WriteJSON's first key: read it alone, not a whole ITF
	// trace beside it
	d := json.NewDecoder(f)
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	key, err := d.Token()
	if err != nil || key != "format" {
		return false
	}
	var format string
	return d.Decode(&format) == nil && formats.Evidence.Is(format)
}

// OfManifest is a run's manifest as evidence's view (RoleExternal): one
// run, its verdict, its test (or the manifest's name) and package.
func OfManifest(m *validate.Manifest) *View {
	e := &View{Format: Format, Role: RoleExternal, Package: m.Package, Import: m.Import, Test: m.Name, Runs: 1, FailingBy: map[string]int{}}
	if m.Model.Spec != nil {
		e.Spec = m.Model.Spec.Path + ":" + m.Model.Module
	}
	switch r := m.Verdict.Reason; {
	case m.Verdict.Verdict == validate.Pass:
		e.Conform = 1
	case m.Verdict.Verdict == validate.NotEvaluated && !strings.HasPrefix(r, "no verdict"):
		e.FailingBy["no verdict ("+r+")"]++ // no verdict, as an exploration's run of none counts
	default:
		e.FailingBy[r]++
	}
	return e
}
