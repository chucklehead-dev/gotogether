package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A JSON reserialization must not turn a strict coverage failure into a pass
// merely by moving the discriminator after the payload.
func TestEvidenceDirectoryDiscoversReorderedDocuments(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		code             int
	}{
		{"fault-order-gap", `"role":"faithful","test":"TestOrders","runs":1,"conform":1,"faultOrders":[{"first":{"kind":"rpc.a","option":1},"second":{"kind":"rpc.b","option":1},"budget":3,"status":"unreached","attempts":[]}],"format":"gotogether.evidence/3"`, "bounded absence, not impossibility", 1},
		{"ambiguous-format", `"format":"gotogether.run/3","format":"gotogether.evidence/3"`, "conflicting top-level format", 2},
		{"ambiguous-reversed", `"format":"gotogether.evidence/3","format":"gotogether.run/3"`, "conflicting top-level format", 2},
		{"ambiguous-unsupported", `"format":"gotogether.run/999","format":"gotogether.evidence/2"`, "conflicting top-level format", 2},
		{"nested-manifest", `"metadata":{"format":"gotogether.run/3"},"role":"faithful","test":"Nested","runs":1,"conform":1,"format":"gotogether.evidence/3"`, "Nested", 0},
		{"failed-manifest", `"name":"FailedRun","verdict":{"verdict":"fail","reason":"violates inv"},"format":"gotogether.run/3"`, "violates inv", 0},
		{"old-evidence", `"runs":1,"format":"gotogether.evidence/2"`, "run the tests again", 2},
		{"future-evidence", `"runs":1,"format":"gotogether.evidence/999"`, "upgrade", 2},
		{"old-manifest", `"name":"OldRun","format":"gotogether.run/2"`, "skipped", 0},
		{"future-manifest", `"name":"FutureRun","format":"gotogether.run/999"`, "skipped", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, body string) string {
				t.Helper()
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				return path
			}
			// Without discovery, this healthy file can conceal the omitted goal.
			write("healthy.json", `{"format":"gotogether.evidence/3","session":"discovery","role":"faithful","test":"Healthy","runs":1,"conform":1}`)
			write("trace.itf.json", `{"states":[{"format":"gotogether.evidence/3"}]}`)
			write("corpus.json", `{"choices":[],"format":"gotogether.corpus/1"}`)
			write("summary.json", `{"runs":1}`)
			path := write("target.json", `{"session":"discovery",`+tc.body+`}`)
			for _, root := range []string{path, dir} {
				var readErr error
				code, out := capture(t, func() (int, error) {
					var code int
					code, readErr = evidence([]string{"-strict", "-session", "discovery", root})
					return code, nil
				})
				// An unsupported manifest is warned/skipped by existing policy.
				// Alone it yields no evidence; beside a healthy file it still warns.
				wantCode := tc.code
				if strings.HasSuffix(tc.name, "manifest") && strings.Contains(tc.want, "skipped") && root == path {
					wantCode = 2
				}
				if readErr != nil {
					out += readErr.Error()
				}
				if code != wantCode || !strings.Contains(out, tc.want) {
					t.Fatalf("root %s: exit %d want %d, error %v:\n%s", root, code, wantCode, readErr, out)
				}
			}
		})
	}
}

// A directory scan must not hide an unreadable file beside healthy evidence.
func TestEvidenceUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	healthy := `{"format":"gotogether.evidence/3","session":"readability","role":"faithful","test":"Healthy","runs":1,"conform":1}`
	for _, name := range []string{"healthy.json", "unreadable.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(healthy), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "unreadable.json")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0600) })
	if _, err := os.ReadFile(path); !errors.Is(err, os.ErrPermission) {
		t.Skip("filesystem/actor can still read mode-000 files")
	}
	for _, classify := range []func(string) (bool, error){qtest.IsEvidence, validate.IsManifest} {
		if found, err := classify(path); found || !errors.Is(err, os.ErrPermission) {
			t.Fatalf("file predicate discarded unreadable input: %t %v", found, err)
		}
	}
	code, err := evidence([]string{"-strict", "-session", "readability", dir})
	if code != 2 || !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "unreadable.json") {
		t.Fatalf("unreadable input hidden: exit %d err %v", code, err)
	}
}
