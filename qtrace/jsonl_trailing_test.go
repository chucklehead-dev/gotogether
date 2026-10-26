package qtrace

import (
	"errors"
	"strings"
	"testing"
)

// A line holding its object and more is an error naming the line, never
// a step with the rest dropped: on a middle line, on a last line with its
// newline, and on a last line without one (not a cut-off line, so no
// TornTail). Trailing whitespace stays allowed.
func TestReadJSONLRejectsTrailingData(t *testing.T) {
	first := `{"quint.action":"save","quint.seq":1,"quint.recorder":"r"}`
	second := `{"quint.action":"reopen","quint.seq":2,"quint.recorder":"r"}`
	for _, suffix := range []string{second, "garbage", ` {}`, `]`} {
		for name, log := range map[string]string{
			"middle":               first + suffix + "\n" + second + "\n",
			"last":                 second + "\n" + first + suffix + "\n",
			"last without newline": second + "\n" + first + suffix,
		} {
			steps, err := ReadJSONL(strings.NewReader(log))
			var torn *TornTail
			if err == nil || errors.As(err, &torn) {
				t.Errorf("%s, %q after the object: %d steps, error %v; want the line rejected", name, suffix, len(steps), err)
				continue
			}
			if !strings.Contains(err.Error(), "line ") || !strings.Contains(err.Error(), "one object per line") {
				t.Errorf("%s: error %q does not name the line and the fix", name, err)
			}
		}
	}
	steps, err := ReadJSONL(strings.NewReader(first + " \t\n" + second + "  \n"))
	if err != nil || len(steps) != 2 {
		t.Errorf("trailing whitespace: %d steps, %v", len(steps), err)
	}
}
