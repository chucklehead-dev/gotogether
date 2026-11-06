package formats

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
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
			if got, err := Evidence.IsFile(path); err != nil || got != tc.want {
				t.Fatalf("IsFile = %v (%v), want %v", got, err, tc.want)
			}
		})
	}
}

// The classifier preserves every relevant header it saw before malformed data;
// only the selected authoritative reader validates the entire document.
func TestDiscoverEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body            string
		exploration, manifest bool
	}{
		{"reordered", `{"payload":[{"format":"gotogether.run/3","x":1e9999}],"f\u006frmat":"gotogether.evidence/3"}`, true, false},
		{"mixed", `{"format":"gotogether.evidence/3","payload":[1,2,3],"FORMAT":"gotogether.run/3"}`, true, true},
		{"mixed-reverse", `{"FORMAT":"gotogether.run/999","format":"gotogether.evidence/2"}`, true, true},
		{"same-family", `{"format":"gotogether.evidence/3","format":"gotogether.evidence/999"}`, true, false},
		{"wrong-type-before", `{"format":{"nested":"gotogether.run/3"},"format":"gotogether.evidence/3"}`, true, false},
		{"malformed-tail", `{"payload":[],"format":"gotogether.evidence/3","bad":[}`, true, false},
		{"malformed-prefix", `{"payload":[},"format":"gotogether.evidence/3"}`, false, false},
		{"trailing-junk", `{"format":"gotogether.run/3"} trailing`, false, true},
		{"lone-surrogate", `{"payload":"\ud800","format":"gotogether.evidence/3"}`, true, false},
		{"invalid-surrogate", `{"payload":"\ud800\u0041","format":"gotogether.evidence/3"}`, true, false},
		{"invalid-utf8", "{\"payload\":\"" + string([]byte{0xff}) + "\",\"format\":\"gotogether.evidence/3\"}", true, false},
		{"nested-duplicate", `{"payload":{"x":1,"x":2},"format":"gotogether.run/3"}`, false, true},
		{"array", `[{"format":"gotogether.evidence/3"}]`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.json")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			exploration, manifest, err := DiscoverEvidence(path)
			if err != nil || exploration != tc.exploration || manifest != tc.manifest {
				t.Fatalf("flags %t/%t err %v, want %t/%t", exploration, manifest, err, tc.exploration, tc.manifest)
			}
		})
	}
}

func TestDiscoveryReadErrors(t *testing.T) {
	fault := errors.New("fixture read failure")
	for _, prefix := range []string{"", `{"format":"gotogether.evidence/3","payload":`} {
		found := false
		err := visitFormats(io.MultiReader(strings.NewReader(prefix), iotest.ErrReader(fault)), func(name string) bool { found = found || Evidence.Is(name); return false })
		if !errors.Is(err, fault) || found != (prefix != "") {
			t.Fatalf("prefix %q: found %t err %v", prefix, found, err)
		}
	}
	if _, _, err := DiscoverEvidence(filepath.Join(t.TempDir(), "absent.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing input: %v", err)
	}
}

func TestFileDiscoveryReturnsReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	if found, err := Evidence.IsFile(path); found || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file became absence: %t %v", found, err)
	}
}
