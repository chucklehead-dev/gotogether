package receipt

import (
	"fmt"
	"slices"
)

// ChoiceSearch describes finite internal model-choice exploration, independently
// of history ordering and of any one selected witness's backend confirmation.
type ChoiceSearch struct {
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Limit      int    `json:"limit"`
	Explored   int    `json:"explored"`
	Exhaustive bool   `json:"exhaustive"`
	// Confirmation is path_confirmed or not_performed. A confirmed path does
	// not independently establish that no other admissible path exists.
	Confirmation   string                `json:"confirmation"`
	Initialization *ChoiceInitialization `json:"initialization,omitempty"`
	Witness        []ChoiceStep          `json:"witness,omitempty"`
}

// ChoiceInitialization pins the original model initializer to a legal starting
// state. It is model witness evidence, not an observed runtime initialization.
type ChoiceInitialization struct {
	Action     string `json:"action"`
	Expression string `json:"expression"`
}

// ChoiceStep pins an original model action to a legal finite choice.
// Recorder and Seq identify the observed input, not an inferred runtime choice.
type ChoiceStep struct {
	Index      int    `json:"index"`
	Action     string `json:"action"`
	Expression string `json:"expression"`
	Recorder   string `json:"recorder,omitempty"`
	Seq        uint64 `json:"seq,omitempty"`
}

// Clone makes an independent snapshot, including witness storage.
func (c *ChoiceSearch) Clone() *ChoiceSearch {
	if c == nil {
		return nil
	}
	copy := *c
	if c.Initialization != nil {
		initialization := *c.Initialization
		copy.Initialization = &initialization
	}
	copy.Witness = slices.Clone(c.Witness)
	return &copy
}

// String renders search scope and confirmation without claiming a sampled or
// individually failing path proves a complete model judgment.
func (c *ChoiceSearch) String() string {
	if c == nil {
		return ""
	}
	completeness := "incomplete"
	if c.Exhaustive {
		completeness = "exhaustive Go search"
	}
	confirmation := "selected path not confirmed"
	if c.Confirmation == "path_confirmed" {
		confirmation = "selected path confirmed by Quint"
	}
	s := fmt.Sprintf("model choices: %s; %d/%d expanded states; %s; %s", c.Status, c.Explored, c.Limit, completeness, confirmation)
	if c.Initialization != nil {
		s += fmt.Sprintf("; initialization witness (%s): %s", c.Initialization.Action, initializationPreview(c.Initialization.Expression))
	}
	if c.Reason != "" {
		s += "; " + c.Reason
	}
	return s
}

// Receipts appear at every navigation cursor. Keep the initializer discoverable
// without flooding that summary; JSON retains the complete original expression.
// Escaping line breaks avoids changing significant whitespace inside literals.
func initializationPreview(expression string) string {
	preview := make([]rune, 0, 160)
	for _, r := range expression {
		part := string(r)
		switch r {
		case '\r':
			part = `\r`
		case '\n':
			part = `\n`
		case '\t':
			part = `\t`
		}
		for _, c := range part {
			if len(preview) == 160 {
				return string(preview) + "… (full expression in JSON)"
			}
			preview = append(preview, c)
		}
	}
	return string(preview)
}
