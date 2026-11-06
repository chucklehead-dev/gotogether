package formats

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, json string
		want       bool
	}{
		{"first", `{"format":"gotogether.evidence/3","runs":1}`, true},
		{"folded-header", `{"runs":1,"FORMAT":"gotogether.evidence/3"}`, true},
		{"last", `{"runs":1,"format":"gotogether.evidence/3"}`, true},
		{"nested-before", `{"states":[{"format":"other","n":1e9999},null,[],{}],"format":"gotogether.evidence/3"}`, true},
		{"nested-only", `{"states":[{"format":"gotogether.evidence/3"}]}`, false},
		{"old", `{"runs":1,"format":"gotogether.evidence/2"}`, true},
		{"future", `{"runs":1,"format":"gotogether.evidence/999"}`, true},
		{"other-family", `{"format":"gotogether.corpus/1"}`, false},
		{"array", `[{"format":"gotogether.evidence/3"}]`, false},
		{"nonstring", `{"format":{"format":"gotogether.evidence/3"}}`, false},
		{"malformed-before", `{"states":[},"format":"gotogether.evidence/3"}`, false},
		{"malformed-after", `{"format":"gotogether.evidence/3","runs":`, true},
		// Discovery must not conceal a family the reader will later refuse.
		{"duplicate-other-first", `{"format":"other","format":"gotogether.evidence/3"}`, true},
		{"duplicate-other-last", `{"format":"gotogether.evidence/3","format":"other"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.json")
			if err := os.WriteFile(path, []byte(tc.json), 0600); err != nil {
				t.Fatal(err)
			}
			if got := Evidence.IsFile(path); got != tc.want {
				t.Fatalf("IsFile = %v, want %v", got, tc.want)
			}
		})
	}
}
