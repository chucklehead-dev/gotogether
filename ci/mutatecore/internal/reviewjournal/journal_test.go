package reviewjournal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (Manifest, [][]byte) {
	t.Helper()
	a := Registration{"qenv/sim.go", 0, "qenv/sim.go:10 replace value", 0}
	b := Registration{"qenv/sub/log.go", 0, "qenv/sub/log.go:20 drop call", 1}
	m := Manifest{Start: Start{1, "/repo", "qenv", 2, 3072, 300, 0}, Suites: []Suite{
		{0, module + "/qenv", "/repo/qenv", "/tmp/root", []Registration{a, b}},
		{1, module + "/qenv/sub", "/repo/qenv/sub", "/tmp/sub", []Registration{{b.File, b.Ordinal, b.Descriptor, 0}}},
		{2, module + "/qenv/unlinked", "/repo/qenv/unlinked", "/tmp/unlinked", nil},
	}}
	sources, err := inventory(m)
	if err != nil {
		t.Fatal(err)
	}
	var records [][]byte
	add := func(event string, data any) {
		b, err := json.Marshal(envelope{event, time.Date(2026, 11, 8, 1, 2, 3, 4, time.UTC), mustJSON(t, data)})
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, append(b, '\n'))
	}
	add("start", m.Start)
	for _, s := range m.Suites {
		add("suite", s)
	}
	for _, s := range m.Suites {
		add("baseline", baseline{Package: s.Package, Passed: true})
	}
	add("admitted", admitted{sources, len(m.Suites)})
	// Reverse source completion order; each source's target attempts stay ordered.
	add("outcome", outcome{Source: sources[1], Outcome: "killed", Attempts: []attempt{{1, 0, "passed", ""}, {0, 1, "failed", ""}}})
	add("outcome", outcome{Source: sources[0], Outcome: "survived", Attempts: []attempt{{0, 0, "passed", ""}}})
	add("complete", complete{2, 1, false})
	return m, records
}
func mustJSON(t *testing.T, x any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(x)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func joined(records [][]byte) []byte { return bytes.Join(records, nil) }
func changed(t *testing.T, records [][]byte, index int, edit func(map[string]any)) [][]byte {
	t.Helper()
	result := slices.Clone(records)
	var e map[string]any
	if err := json.Unmarshal(result[index], &e); err != nil {
		t.Fatal(err)
	}
	edit(e["data"].(map[string]any))
	result[index] = append(mustJSON(t, e), '\n')
	return result
}
func TestCompleteRedEvidence(t *testing.T) {
	m, records := fixture(t)
	r, err := Validate(bytes.NewReader(joined(records)), m)
	if err != nil {
		t.Fatal(err)
	}
	if !r.StructurallyComplete() || r.CountGatePassed() {
		t.Fatalf("red complete campaign lost: %+v", r)
	}
	got := r.Outcomes()
	if len(got) != 2 || !got[0].Survived || got[1].KillingSuite != module+"/qenv" {
		t.Fatalf("outcomes: %+v", got)
	}
	got[0].Survived = false
	if !r.Outcomes()[0].Survived {
		t.Fatal("result aliases caller mutation")
	}
	if (Result{}).StructurallyComplete() || (Result{}).CountGatePassed() {
		t.Fatal("zero result passed")
	}
	// Wall-clock steps backwards do not invalidate structurally complete evidence.
	b := bytes.Replace(joined(records), []byte(`"at":"2026-11-08T01:02:03.000000004Z"`), []byte(`"at":"2025-01-01T00:00:00Z"`), 1)
	if _, err := Validate(bytes.NewReader(b), m); err != nil {
		t.Fatal(err)
	}
}
func TestRejectIncompleteOrReorderedEvents(t *testing.T) {
	m, records := fixture(t)
	for i := range records {
		t.Run(fmt.Sprintf("missing-%d", i), func(t *testing.T) {
			x := append(slices.Clone(records[:i]), records[i+1:]...)
			if _, err := Validate(bytes.NewReader(joined(x)), m); err == nil {
				t.Fatal("accepted missing event")
			}
		})
	}
	for i := range records {
		t.Run(fmt.Sprintf("duplicate-%d", i), func(t *testing.T) {
			x := append(slices.Clone(records[:i]), records[i])
			x = append(x, records[i:]...)
			if _, err := Validate(bytes.NewReader(joined(x)), m); err == nil {
				t.Fatal("accepted duplicate event")
			}
		})
	}
	for _, pair := range [][2]int{{0, 1}, {1, 2}, {3, 4}, {4, 5}, {6, 7}, {7, 8}, {9, 10}} {
		x := slices.Clone(records)
		x[pair[0]], x[pair[1]] = x[pair[1]], x[pair[0]]
		if _, err := Validate(bytes.NewReader(joined(x)), m); err == nil {
			t.Fatalf("accepted reorder %v", pair)
		}
	}
	// Source outcomes alone may swap: worker completion order is not admission order.
	x := slices.Clone(records)
	x[8], x[9] = x[9], x[8]
	if _, err := Validate(bytes.NewReader(joined(x)), m); err != nil {
		t.Fatal(err)
	}
}
func TestRejectContradictions(t *testing.T) {
	m, r := fixture(t)
	tests := []struct {
		name  string
		index int
		edit  func(map[string]any)
	}{
		{"start-settings", 0, func(m map[string]any) { m["jobs"] = 3 }},
		{"failed-baseline", 4, func(m map[string]any) { m["passed"] = false }},
		{"baseline-error", 4, func(m map[string]any) { m["error"] = "interrupted" }},
		{"admitted-missing-source", 7, func(m map[string]any) { m["sources"] = m["sources"].([]any)[:1] }},
		{"changed-source-target", 8, func(m map[string]any) { m["source"].(map[string]any)["targets"].([]any)[0].(map[string]any)["id"] = 1 }},
		{"changed-descriptor", 8, func(m map[string]any) { m["source"].(map[string]any)["descriptor"] = "other" }},
		{"unknown-source", 8, func(m map[string]any) { m["source"].(map[string]any)["file"] = "qenv/other.go" }},
		{"attempt-order", 8, func(m map[string]any) { a := m["attempts"].([]any); a[0], a[1] = a[1], a[0] }},
		{"unknown-attempt", 8, func(m map[string]any) { m["attempts"].([]any)[0].(map[string]any)["suite"] = 99 }},
		{"passed-kill", 8, func(m map[string]any) { m["attempts"].([]any)[1].(map[string]any)["outcome"] = "passed" }},
		{"kill-after-failure", 8, func(m map[string]any) { m["attempts"].([]any)[0].(map[string]any)["outcome"] = "failed" }},
		{"attempt-error", 8, func(m map[string]any) { m["attempts"].([]any)[0].(map[string]any)["error"] = "cancelled" }},
		{"source-error", 8, func(m map[string]any) { m["error"] = "cancelled" }},
		{"error-verdict", 8, func(m map[string]any) { m["outcome"] = "error" }},
		{"survivor-prefix-only", 8, func(m map[string]any) { m["outcome"] = "survived"; m["attempts"] = m["attempts"].([]any)[:1] }},
		{"survivor-failed-attempt", 8, func(m map[string]any) { m["outcome"] = "survived" }},
		{"empty-attempts", 9, func(m map[string]any) { m["attempts"] = []any{} }},
		{"total", 10, func(m map[string]any) { m["total"] = 1 }},
		{"survivor-count", 10, func(m map[string]any) { m["survivors"] = 0 }},
		{"false-green", 10, func(m map[string]any) { m["gate_passed"] = true }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			x := changed(t, r, tc.index, tc.edit)
			result, err := Validate(bytes.NewReader(joined(x)), m)
			if err == nil || result.StructurallyComplete() {
				t.Fatalf("accepted contradiction: %v", result)
			}
		})
	}
}
func TestStrictFramingAndJSON(t *testing.T) {
	m, r := fixture(t)
	good := joined(r)
	for name, b := range map[string][]byte{
		"torn-tail": good[:len(good)-1], "truncated-object": good[:len(good)-10], "blank-tail": append(slices.Clone(good), '\n'), "trailing-object": append(slices.Clone(good), []byte("{}\n")...),
		"duplicate-envelope-key": bytes.Replace(good, []byte(`"event":"start"`), []byte(`"event":"start","event":"start"`), 1),
		"duplicate-nested-key":   bytes.Replace(good, []byte(`"ordinal":0`), []byte(`"ordinal":0,"ordinal":0`), 1),
		"case-alias":             bytes.Replace(good, []byte(`"gate_passed":false`), []byte(`"Gate_Passed":false`), 1),
		"case-alias-nested":      bytes.Replace(good, []byte(`"ordinal":0`), []byte(`"Ordinal":0`), 1),
		"missing-zero":           bytes.Replace(good, []byte(`"ordinal":0,`), nil, 1),
		"null-zero":              bytes.Replace(good, []byte(`"ordinal":0`), []byte(`"ordinal":null`), 1),
		"fractional-zero":        bytes.Replace(good, []byte(`"ordinal":0`), []byte(`"ordinal":0.0`), 1),
		"missing-false":          bytes.Replace(good, []byte(`,"gate_passed":false`), nil, 1),
		"null-false":             bytes.Replace(good, []byte(`"gate_passed":false`), []byte(`"gate_passed":null`), 1),
		"null-error":             bytes.Replace(good, []byte(`"passed":true`), []byte(`"passed":true,"error":null`), 1),
		"zero-timestamp":         bytes.Replace(good, []byte(`2026-11-08T01:02:03.000000004Z`), []byte(`0001-01-01T00:00:00Z`), 1),
		"invalid-timestamp":      bytes.Replace(good, []byte(`2026-11-08T01:02:03.000000004Z`), []byte(`yesterday`), 1),
		"oversized":              []byte(strings.Repeat(" ", 16<<20) + "\n"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Validate(bytes.NewReader(b), m); err == nil {
				t.Fatal("accepted malformed journal")
			}
		})
	}
}
func TestExpectedManifestMustBeConsistent(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Manifest)
	}{
		{"empty-suites", func(m *Manifest) { m.Suites = nil }},
		{"empty-union", func(m *Manifest) {
			for i := range m.Suites {
				m.Suites[i].Inventory = nil
			}
		}},
		{"duplicate-suite", func(m *Manifest) { m.Suites[1].Package = m.Suites[0].Package }},
		{"suite-index", func(m *Manifest) { m.Suites[0].Index = 1 }},
		{"prefix-boundary", func(m *Manifest) { m.Suites[0].Inventory[0].File = "qenvx/sim.go" }},
		{"path-traversal", func(m *Manifest) { m.Suites[0].Inventory[0].File = "qenv/../sim.go" }},
		{"noncontiguous-id", func(m *Manifest) { m.Suites[0].Inventory[0].ID = 1 }},
		{"noncontiguous-ordinal", func(m *Manifest) { m.Suites[0].Inventory[0].Ordinal = 1 }},
		{"changed-linked-descriptor", func(m *Manifest) { m.Suites[1].Inventory[0].Descriptor = "other" }},
		{"changed-linked-count", func(m *Manifest) {
			m.Suites[1].Inventory = append(m.Suites[1].Inventory, Registration{"qenv/sub/log.go", 1, "second mutation", 1})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, r := fixture(t)
			tc.edit(&m)
			if _, err := Validate(bytes.NewReader(joined(r)), m); err == nil {
				t.Fatal("accepted invalid expected manifest")
			}
		})
	}
}
func TestDiagnosticUnlimitedCountIsNotPolicyAdmission(t *testing.T) {
	m, r := fixture(t)
	m.Start.MaxSurvivors = -1
	r = changed(t, r, 0, func(x map[string]any) { x["max_survivors"] = -1 })
	r = changed(t, r, 10, func(x map[string]any) { x["gate_passed"] = true })
	result, err := Validate(bytes.NewReader(joined(r)), m)
	if err != nil || !result.StructurallyComplete() || !result.CountGatePassed() {
		t.Fatalf("diagnostic completion: %v %v", result, err)
	}
}

// Optional retained-campaign consistency control: this reconstructs the expected
// manifest from that SAME journal, so it is explicitly not independent admission.
func TestRetainedCampaignConsistencyOnly(t *testing.T) {
	name := os.Getenv("GOTOGETHER_REVIEW_JOURNAL_FIXTURE")
	if name == "" {
		t.Skip("set explicit retained journal path for read-only consistency check")
	}
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var e envelope
		if err := decode(line, &e); err != nil {
			t.Fatal(err)
		}
		switch e.Event {
		case "start":
			if err := decode(e.Data, &m.Start); err != nil {
				t.Fatal(err)
			}
		case "suite":
			var s Suite
			if err := decode(e.Data, &s); err != nil {
				t.Fatal(err)
			}
			m.Suites = append(m.Suites, s)
		}
	}
	result, err := Validate(bytes.NewReader(b), m)
	if err != nil {
		t.Fatal(err)
	}
	if !result.StructurallyComplete() || result.CountGatePassed() || len(result.Outcomes()) != 1748 {
		t.Fatalf("retained campaign structure: %+v", result)
	}
	survivors := 0
	for _, o := range result.Outcomes() {
		if o.Survived {
			survivors++
		}
	}
	if survivors != 120 {
		t.Fatalf("survivors %d", survivors)
	}
	t.Log("1748 resolved outcomes/120 survivors/complete count gate red; same-log manifest is consistency evidence only")
}

func TestEveryRequiredFieldIsExplicit(t *testing.T) {
	m, records := fixture(t)
	// Exercise the zero/false hazard at every record depth, rather than assuming
	// encoding/json's zero values distinguish omission from an explicit value.
	for index, line := range records {
		var original any
		if err := json.Unmarshal(line, &original); err != nil {
			t.Fatal(err)
		}
		var walk func(any, []any)
		walk = func(value any, parents []any) {
			switch x := value.(type) {
			case map[string]any:
				for key, child := range x {
					if key == "error" {
						continue
					}
					location := append(slices.Clone(parents), key)
					for _, null := range []bool{false, true} {
						if null && child == nil {
							continue
						} // Current producer emits null empty suite inventory.
						t.Run(fmt.Sprintf("record%d-%v-null%v", index, location, null), func(t *testing.T) {
							var copyValue any
							if err := json.Unmarshal(line, &copyValue); err != nil {
								t.Fatal(err)
							}
							parent := copyValue
							for _, part := range parents {
								switch p := part.(type) {
								case string:
									parent = parent.(map[string]any)[p]
								case int:
									parent = parent.([]any)[p]
								}
							}
							if null {
								parent.(map[string]any)[key] = nil
							} else {
								delete(parent.(map[string]any), key)
							}
							changed := slices.Clone(records)
							changed[index] = append(mustJSON(t, copyValue), '\n')
							if _, err := Validate(bytes.NewReader(joined(changed)), m); err == nil {
								t.Fatal("accepted absent/null required field")
							}
						})
					}
					walk(child, location)
				}
			case []any:
				for i, child := range x {
					walk(child, append(slices.Clone(parents), i))
				}
			}
		}
		walk(original, nil)
	}
}
