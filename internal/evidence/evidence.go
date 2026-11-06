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
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

// Format is the format qtest.Evidence.WriteJSON writes and Read reads.
// Bump it when a field's name or meaning changes (qenv.Coverage's and
// qcover.Summary's, which Evidence embeds, too), so old files are
// refused, not misread.
const Format = "gotogether.evidence/3" // 3: requested directed-fault coverage participates in completeness

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
	FaultOrders []FaultOrder           `json:"faultOrders,omitempty"`
	Profiles    []runprofile.Profile   `json:"profiles,omitempty"`
	Executions  []runprofile.Execution `json:"executions,omitempty"`
	Session     string                 `json:"session,omitempty"`
	Format      string                 `json:"format"`
	Role        string                 `json:"role"`
	Package     string                 `json:"package,omitempty"`
	Import      string                 `json:"import,omitempty"`
	Test        string                 `json:"test,omitempty"`
	Spec        string                 `json:"spec,omitempty"`
	Runs        int                    `json:"runs"`
	Conform     int                    `json:"conform"`
	Unjudged    int                    `json:"unjudged,omitempty"`
	RunErrors   map[string]string      `json:"runErrors,omitempty"`
	FailingBy   map[string]int         `json:"failing"`
	Faults      *struct {
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

// FaultSpec identifies one requested scripted fault. The directed order owns
// dependencies, so Follows is represented by First and Second rather than copied.
type FaultSpec struct {
	Kind   string `json:"kind"`
	Option int    `json:"option,omitempty"`
	Name   string `json:"name,omitempty"`
	After  int    `json:"after,omitempty"`
}

// FaultOrderAttempt identifies an actual run. Seed is absent for an origin
// that did not come from a seeded attempt; it is never reconstructed from text.
type FaultOrderAttempt struct {
	Run  string  `json:"run"`
	Seed *uint64 `json:"seed,omitempty"`
}

// FaultOrder is bounded directed fault coverage, independent of whether its
// attempted runs conform. Unreached does not establish impossibility.
type FaultOrder struct {
	First    FaultSpec           `json:"first"`
	Second   FaultSpec           `json:"second"`
	Budget   int                 `json:"budget"`
	Attempts []FaultOrderAttempt `json:"attempts"`
	Status   string              `json:"status"`
	Witness  string              `json:"witness,omitempty"`
}

func (f FaultSpec) String() string {
	if strings.HasPrefix(f.Kind, "nemesis.") {
		return fmt.Sprintf("%s after %d steps", f.Kind, f.After)
	}
	if f.Name != "" {
		return fmt.Sprintf("%s=%s after %d steps", f.Kind, f.Name, f.After)
	}
	return fmt.Sprintf("%s=%d after %d steps", f.Kind, f.Option, f.After)
}

// ValidateFaultOrders refuses contradictory new coverage metadata rather than
// treating a malformed witnessed status as successful requested coverage.
func ValidateFaultOrders(orders []FaultOrder) error {
	for i, o := range orders {
		fail := func(why string) error { return fmt.Errorf("faultOrders[%d]: %s", i, why) }
		if o.First.Kind == "" || o.Second.Kind == "" || o.First.Kind == o.Second.Kind {
			return fail("requires two distinct nonempty kinds")
		}
		for _, f := range []FaultSpec{o.First, o.Second} {
			if f.After < 0 || !strings.HasPrefix(f.Kind, "nemesis.") && f.Name == "" && f.Option <= 0 {
				return fail("requires nonnominal faults and nonnegative step gates")
			}
		}
		if o.Budget <= 0 || len(o.Attempts) > o.Budget {
			return fail("invalid seed budget or attempt count")
		}
		seen := map[string]bool{}
		for _, a := range o.Attempts {
			if a.Run == "" || seen[a.Run] {
				return fail("attempt identities must be nonempty and unique")
			}
			seen[a.Run] = true
		}
		switch o.Status {
		case "witnessed":
			if o.Witness == "" || !seen[o.Witness] {
				return fail("witness must name an actual attempt")
			}
		case "unreached":
			if o.Witness != "" {
				return fail("unreached order cannot name a witness")
			}
		default:
			return fail("status must be witnessed or unreached")
		}
	}
	return nil
}

// FaultOrdersString reports requested orders and their bounded execution extent.
func FaultOrdersString(orders []FaultOrder) string {
	var b strings.Builder
	for _, order := range orders {
		fmt.Fprintf(&b, "  fault order %s then %s: %s (%d attempts, seed budget %d)", order.First, order.Second, order.Status, len(order.Attempts), order.Budget)
		if order.Witness != "" {
			fmt.Fprintf(&b, "; witness %s", order.Witness)
		}
		if order.Status == "unreached" {
			b.WriteString("; bounded absence, not impossibility")
		}
		b.WriteByte('\n')
	}
	return b.String()
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
		Format      string          `json:"format"`
		FaultOrders json.RawMessage `json:"faultOrders"`
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
	var orders []FaultOrder
	if len(head.FaultOrders) > 0 {
		if err := json.Unmarshal(head.FaultOrders, &orders); err != nil {
			return fmt.Errorf("%s: faultOrders: %w", path, err)
		}
	}
	if err := ValidateFaultOrders(orders); err != nil {
		return fmt.Errorf("%s: %w", path, err)
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
// Encountered I/O errors are returned; classification does not validate the file.
func Is(path string) (bool, error) {
	return formats.Evidence.IsFile(path)
}

// OfManifest is a run's manifest as evidence's view (RoleExternal): one
// run, its verdict, its test (or the manifest's name) and package.
func OfManifest(m *validate.Manifest) *View {
	e := &View{Session: m.Session, Format: Format, Role: RoleExternal, Package: m.Package, Import: m.Import, Test: m.Name, Runs: 1, FailingBy: map[string]int{}}
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
