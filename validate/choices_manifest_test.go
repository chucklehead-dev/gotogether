package validate

import (
	"path/filepath"
	"testing"
)

func TestChoicesManifestRecordsEffectivePolicy(t *testing.T) {
	for _, limit := range []int{0, 7} {
		m, err := NewManifest(RunRecord{Options: Options{Quint: "no-such-quint", ChoiceLimit: limit}})
		if err != nil {
			t.Fatal(err)
		}
		want := limit
		if want == 0 {
			want = DefaultChoiceLimit
		}
		if m.Check.ChoiceLimit != want {
			t.Fatalf("recorded limit %d, want %d", m.Check.ChoiceLimit, want)
		}
		path := filepath.Join(t.TempDir(), "run.json")
		if err := m.Write(path); err != nil {
			t.Fatal(err)
		}
		got, err := ReadManifest(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.Check.ChoiceLimit != want {
			t.Fatalf("round-trip limit %d, want %d", got.Check.ChoiceLimit, want)
		}
	}
}
