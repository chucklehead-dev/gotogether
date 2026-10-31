package receipt

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChoicesDoNotDisappearWithPassingPartitions(t *testing.T) {
	s := Summary{Status: Pass, Scope: "collected_history", Actors: []Actor{{Actor: "a", Partition: "key=x", Status: Pass, Ordering: "reconstructed", Choices: &ChoiceSearch{Status: "matched", Limit: 10, Explored: 2, Exhaustive: true, Confirmation: "path_confirmed"}}}}
	if got := s.String(); !strings.Contains(got, "model choices: matched") {
		t.Fatal(got)
	}
}

func TestInitializationWitnessIsSeparateFromObservedSteps(t *testing.T) {
	c := &ChoiceSearch{Status: "matched", Limit: 10, Explored: 2, Confirmation: "path_confirmed",
		Initialization: &ChoiceInitialization{Action: "init", Expression: "all {\n leader' = \"node  two\"\n}"}}
	cloned := c.Clone()
	cloned.Initialization.Expression = "different model witness"
	if c.Initialization.Expression == cloned.Initialization.Expression {
		t.Fatal("initialization witness aliases its clone")
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Initialization map[string]any `json:"initialization"`
		Witness        []ChoiceStep   `json:"witness"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Initialization) != 2 || out.Initialization["action"] != "init" || out.Initialization["expression"] != c.Initialization.Expression || len(out.Witness) != 0 {
		t.Fatalf("initializer confused with observed input: %s", b)
	}
	if text := c.String(); !strings.Contains(text, `initialization witness (init): all {\n leader' = "node  two"\n}`) || strings.Contains(text, "observed initialization") {
		t.Fatalf("initializer evidence is not identified as a model witness: %s", text)
	}
}

func TestInitializationWitnessPreviewDoesNotTruncateJSON(t *testing.T) {
	expression := `all { leader' = "` + strings.Repeat("node  α", 50) + `" }`
	c := &ChoiceSearch{Status: "matched", Limit: 10, Explored: 2, Confirmation: "path_confirmed",
		Initialization: &ChoiceInitialization{Action: "init", Expression: expression}}
	text := c.String()
	_, preview, ok := strings.Cut(text, "initialization witness (init): ")
	const notice = "… (full expression in JSON)"
	if !ok || !strings.HasSuffix(preview, notice) || utf8.RuneCountInString(preview) > 160+utf8.RuneCountInString(notice) || !utf8.ValidString(preview) {
		t.Fatalf("unbounded or unclear initialization preview: %s", text)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var out ChoiceSearch
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Initialization == nil || out.Initialization.Expression != expression {
		t.Fatalf("JSON lost full initialization expression: %s", b)
	}
}

func TestChoicesNegativeProofDoesNotClaimQuintExhaustiveness(t *testing.T) {
	c := &ChoiceSearch{Status: "rejected", Limit: 100, Explored: 8, Exhaustive: true, Confirmation: "not_performed"}
	if got := c.String(); !strings.Contains(got, "exhaustive Go search") || strings.Contains(got, "confirmed by Quint") {
		t.Fatal(got)
	}
}
