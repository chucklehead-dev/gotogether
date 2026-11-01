package membership

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// This checks representation, not the independent temporal/ownership oracle.
func TestNativeProjectionAndRoundTrip(t *testing.T) {
	cases, err := LoadCases("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	steps, observations, err := Observe(t.Context(), cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != len(cases) {
		t.Fatal("lost native operation")
	}
	byName := map[string]Observation{}
	for _, o := range observations {
		byName[o.Case.Name] = o
	}
	for _, step := range steps {
		o, ok := byName[step.Ref[len("case:"):]]
		if !ok {
			t.Fatal("missing native response")
		}
		switch step.Action {
		case "issue":
			if o.Capability == nil || o.Input == nil {
				t.Fatal("missing actual input/capability")
			}
			if step.Obs["expires"] != relative(o.Capability.ExpiresAt) || step.Obs["basis"] != o.Capability.Basis {
				t.Fatal("capability projection changed")
			}
			if step.Args["members"] != list(inputMembers(*o.Input)) {
				t.Fatal("trusted input projection changed")
			}
		case "resolve":
			if step.Obs["hasDraft"] != (o.Draft != nil) || step.Obs["members"] != list(draftMembers(o.Draft)) {
				t.Fatal("draft projection changed")
			}
		}
	}

	for _, o := range observations {
		if o.Input != nil {
			for i, m := range inputMembers(*o.Input) {
				body, err := base64.StdEncoding.DecodeString(m.Body)
				if err != nil || !bytes.Equal(body, o.Input.Rows[i].Record) {
					t.Fatal("input body projection lost raw bytes", err)
				}
			}
		}
		if o.Draft != nil {
			for _, m := range draftMembers(o.Draft) {
				body, err := base64.StdEncoding.DecodeString(m.Body)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, actual := range o.Draft.Members {
					if m.Key == actual.Ref.Key && bytes.Equal(body, actual.Record) {
						found = true
					}
				}
				if !found {
					t.Fatal("returned body projection lost raw bytes")
				}
			}
		}
	}
	var native bytes.Buffer
	if err = WriteTrace(&native, steps); err != nil {
		t.Fatal(err)
	}
	decoded, err := qtrace.ReadJSONL(bytes.NewReader(native.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(steps) {
		t.Fatal("native trace lost steps")
	}
	for i, s := range decoded {
		if s.Action != steps[i].Action || list(s.Obs) != list(steps[i].Obs) {
			t.Fatalf("native trace changed observation at%d", i)
		}
	}
	path := filepath.Join(t.TempDir(), "steps.jsonl")
	if err = os.WriteFile(path, native.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	read, err := input.Read(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Steps) != len(steps) {
		t.Fatal("ordinary input reader lost steps")
	}
	// Returned native evidence was copied before the authored mutation operations.
	for _, name := range []string{"before-mutation", "after-input-mutation", "after-draft-mutation"} {
		raw, err := json.Marshal(byName[name])
		if err != nil || !json.Valid(raw) {
			t.Fatalf("%s retained invalid aliased JSON: %v", name, err)
		}
	}
}
