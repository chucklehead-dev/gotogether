package choice

import (
	"fmt"
	"math"
	"testing"
)

// Naming is one-based for declared fault options; nominal, unsupported and
// out-of-range values have no name, including at the integer boundaries.
func TestOptionNameContract(t *testing.T) {
	kind := "test.contract." + t.Name()
	Declare(kind, "lost", "duplicate")
	for _, name := range []string{kind, "fault." + kind} {
		for _, tc := range []struct {
			value int
			want  string
		}{
			{math.MinInt, ""}, {-1, ""}, {0, ""},
			{1, "lost"}, {2, "duplicate"}, {3, ""}, {math.MaxInt, ""},
		} {
			t.Run(fmt.Sprintf("%s/%d", name, tc.value), func(t *testing.T) {
				if got := OptionName(name, tc.value); got != tc.want {
					t.Errorf("OptionName(%q, %d) = %q, want %q", name, tc.value, got, tc.want)
				}
			})
		}
	}
	for _, value := range []int{math.MinInt, 0, 1, 2, math.MaxInt} {
		if got := OptionName(kind+".unknown", value); got != "" {
			t.Errorf("unknown kind option %d named %q", value, got)
		}
	}
}

// Logs attach names only to declared fault options and keep the underlying
// choice unchanged. A named schedule kind must still be recorded by index.
func TestChoiceLogFaultOptionNames(t *testing.T) {
	kind := "test.contract." + t.Name()
	Declare(kind, "lost", "duplicate")
	for _, tc := range []struct {
		name  string
		value int
		want  string
	}{
		{"fault." + kind, 0, ""},
		{"fault." + kind, 1, "lost"},
		{"fault." + kind, 2, "duplicate"},
		{kind, 1, ""},
		{"fault." + kind + ".unknown", 1, ""},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.name, tc.value), func(t *testing.T) {
			log := &ChoiceLog{C: fixed{tc.name: tc.value}}
			if got := log.Choose(tc.name, 3); got != tc.value {
				t.Fatalf("Choose returned %d, want %d", got, tc.value)
			}
			if len(log.Choices) != 1 {
				t.Fatalf("recorded %d choices, want one", len(log.Choices))
			}
			got := log.Choices[0]
			if got.Kind != tc.name || got.N != 3 || got.Value != tc.value || got.Name != tc.want {
				t.Errorf("recorded choice = %+v, want kind=%q options=3 value=%d name=%q", got, tc.name, tc.value, tc.want)
			}
		})
	}
}
