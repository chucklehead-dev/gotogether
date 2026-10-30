package binding

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/template"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// EvidenceRule identifies the observed action that originated a diagnostic item.
// Match maps model parameter names to diagnostic fields (intent or subject).
// These are equality selectors, not causal explanations or extra model checks.
type EvidenceRule struct {
	Action string            `yaml:"action"`
	Match  map[string]string `yaml:"match"`
	Origin *EvidenceOrigin   `yaml:"origin,omitempty"`
}

// EvidenceOrigin gives original identity templates for a projected record.
// Templates use raw step values: actor, recorder, seq, args and obs. All three
// fields are required together; absent Origin uses the captured step's identity.
type EvidenceOrigin struct {
	Actor    string `yaml:"actor"`
	Recorder string `yaml:"recorder"`
	Seq      string `yaml:"seq"`
}

func (b *Binding) checkEvidence(m *Model) error {
	for _, projection := range slices.Sorted(maps.Keys(b.DiagnosticEvidence)) {
		kinds := b.DiagnosticEvidence[projection]
		if strings.TrimSpace(projection) == "" || len(kinds) == 0 {
			return fmt.Errorf("diagnosticEvidence: give a projection name and at least one kind")
		}
		if m != nil {
			found := false
			for _, name := range m.Diagnostics {
				found = found || name == projection
			}
			if !found {
				return fmt.Errorf("diagnosticEvidence.%s: name a model diagnostic projection", projection)
			}
		}
		for _, kind := range slices.Sorted(maps.Keys(kinds)) {
			r := kinds[kind]
			at := "diagnosticEvidence." + projection + "." + kind
			if strings.TrimSpace(kind) == "" || strings.TrimSpace(r.Action) == "" {
				return fmt.Errorf("%s: give a nonempty kind and action", at)
			}
			intent := false
			for _, arg := range slices.Sorted(maps.Keys(r.Match)) {
				field := r.Match[arg]
				if arg == "" || (field != "intent" && field != "subject") {
					return fmt.Errorf("%s.match.%s: map a model parameter to intent or subject", at, arg)
				}
				intent = intent || field == "intent"
			}
			if !intent {
				return fmt.Errorf("%s.match: include an intent selector to identify the originating call", at)
			}
			if m != nil {
				ps, ok := m.Actions[r.Action]
				if !ok {
					return fmt.Errorf("%s.action: %s is not a model action", at, r.Action)
				}
				for _, arg := range slices.Sorted(maps.Keys(r.Match)) {
					found := false
					for _, p := range ps {
						if p.Name == arg {
							found = true
							resolved := p.typ.resolved()
							if resolved == nil && p.Type != "str" || resolved != nil && resolved.kind != "str" {
								return fmt.Errorf("%s.match.%s: evidence selectors require a string model parameter", at, arg)
							}
						}
					}
					if !found {
						return fmt.Errorf("%s.match.%s: name a parameter of %s", at, arg, r.Action)
					}
				}
			}
			if r.Origin != nil {
				for _, name := range []string{"actor", "recorder", "seq"} {
					src := map[string]string{"actor": r.Origin.Actor, "recorder": r.Origin.Recorder, "seq": r.Origin.Seq}[name]
					if strings.TrimSpace(src) == "" {
						return fmt.Errorf("%s.origin.%s: provide actor, recorder and seq together", at, name)
					}
					if _, err := template.New("origin").Option("missingkey=error").Parse(src); err != nil {
						return fmt.Errorf("%s.origin.%s: %w", at, name, err)
					}
				}
			}
		}
	}
	return nil
}

// Render renders one original identity template against raw captured step data.
// This intentionally does not evaluate model expressions or guess identity fields.
func (o *EvidenceOrigin) Render(src string, s *qtrace.Step) (string, error) {
	t, err := template.New("origin").Option("missingkey=error").Parse(src)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	err = t.Execute(&out, map[string]any{"actor": s.Actor, "recorder": s.Recorder, "seq": s.Seq, "args": s.Args, "obs": s.Obs})
	return out.String(), err
}
