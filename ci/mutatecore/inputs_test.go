package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func inputFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":                    "module fixture.test/root\n\ngo 1.27.3\n",
		"subject/subject.go":        "package subject\nimport \"embed\"\n//go:embed payload/*\nvar Payload embed.FS\nfunc Value() int { return 1 }\n",
		"subject/payload/one.txt":   "first payload",
		"subject/subject_test.go":   "package subject_test\nimport (\"testing\"; \"fixture.test/root/subject\")\nfunc TestValue(t *testing.T) { if subject.Value()!=1 { t.Fatal(\"value\") } }\n",
		"qweave/go.mod":             "module fixture.test/qweave\n\ngo 1.27.3\n",
		"qweave/cmd/qweave/main.go": "package main\nfunc main() {}\n",
		".gitignore":                "subject/ignored.go\nsubject/payload/ignored.txt\n.gotogether/\n",
	} {
		writeInputFixture(t, root, name, body)
	}
	return root
}
func writeInputFixture(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func mustCollectInputs(t *testing.T, root string) inputManifest {
	t.Helper()
	m, err := collectInputs(t.Context(), root, "subject")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCollectInputsUsesActualBuildClosures(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOCACHEPROG", "")
	root := inputFixture(t)
	before := mustCollectInputs(t, root)
	if before.Eligible || len(before.Limitations) == 0 {
		t.Fatal("static input collection must disclose runtime limitations")
	}
	if len(before.Discoveries) != 3 || len(before.TargetDiscovery) == 0 || len(before.Tools) < 4 {
		t.Fatal("missing discovery/tool evidence")
	}
	same := mustCollectInputs(t, root)
	if same.Digest != before.Digest {

		t.Fatal("unchanged inputs changed digest")
	}
	writeInputFixture(t, root, "README.md", "an unrelated typo fix")
	writeInputFixture(t, root, ".gotogether/mutations/output.jsonl", "runtime output")
	if got := mustCollectInputs(t, root); got.Digest != before.Digest {
		t.Fatal("unrelated documentation/output changed build identity")
	}
	cases := []struct{ name, path, body string }{
		{"dirty source", "subject/subject.go", "package subject\nfunc Value() int { return 2 }\n"},
		{"ignored compiled source", "subject/ignored.go", "package subject\nfunc Added() int { return 1 }\n"},
		{"embed membership", "subject/payload/ignored.txt", "new ignored payload"},
		{"external test", "subject/extra_test.go", "package subject_test\nimport \"testing\"\nfunc TestMore(t *testing.T) {}\n"},
		{"generator module", "qweave/cmd/qweave/main.go", "package main\nfunc main() { println(\"changed\") }\n"},
		{"new caller in actual generator graph", "caller/caller.go", "package caller\nimport \"fixture.test/root/subject\"\nvar Answer=subject.Value()\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			full := filepath.Join(root, tc.path)
			original, readErr := os.ReadFile(full)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			t.Cleanup(func() {
				if os.IsNotExist(readErr) {
					if err := os.Remove(full); err != nil {
						t.Error(err)
					}
				} else if err := os.WriteFile(full, original, 0600); err != nil {
					t.Error(err)
				}
			})
			writeInputFixture(t, root, tc.path, tc.body)
			changed := mustCollectInputs(t, root)
			if changed.Digest == before.Digest {
				t.Fatalf("%s escaped context identity", tc.path)
			}
		})
	}
}

func TestCollectInputsAccountsExternalReplacementBytes(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOCACHEPROG", "")
	root := inputFixture(t)
	dependency := t.TempDir()
	writeInputFixture(t, dependency, "go.mod", "module fixture.test/dependency\n\ngo 1.27.3\n")
	writeInputFixture(t, dependency, "dep.go", "package dependency\nconst Answer=1\n")
	writeInputFixture(t, root, "go.mod", "module fixture.test/root\n\ngo 1.27.3\nrequire fixture.test/dependency v0.0.0\nreplace fixture.test/dependency => "+filepath.ToSlash(dependency)+"\n")
	writeInputFixture(t, root, "subject/dependency.go", "package subject\nimport \"fixture.test/dependency\"\nvar Dep=dependency.Answer\n")
	before := mustCollectInputs(t, root)
	found := false
	for _, file := range before.Files {
		if file.Path == filepath.Join(dependency, "dep.go") {
			found = true
		}
	}
	if !found {
		t.Fatal("external replacement source omitted")
	}
	writeInputFixture(t, dependency, "dep.go", "package dependency\nconst Answer=2\n")
	if after := mustCollectInputs(t, root); after.Digest == before.Digest {
		t.Fatal("external replacement drift escaped identity")
	}
}

func TestCollectInputsUnsupportedSettingsAreExplicit(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOFLAGS", "-overlay=unaccounted.json")
	root := inputFixture(t)
	_, err := collectInputs(context.Background(), root, "subject")
	var unsupported *unsupportedInputsError
	if !errors.As(err, &unsupported) || !strings.Contains(err.Error(), "overlay") {
		t.Fatalf("unaccounted overlay: %v", err)
	}
	t.Setenv("GOFLAGS", "")
	writeInputFixture(t, root, "orchestrion.tool.go", "// aspect inputs not yet collected")
	_, err = collectInputs(context.Background(), root, "subject")
	if !errors.As(err, &unsupported) || !strings.Contains(err.Error(), "orchestrion.tool.go") {
		t.Fatalf("unaccounted aspects: %v", err)
	}
}

func TestFingerprintInputLinksAndAbsentMembership(t *testing.T) {
	root := t.TempDir()
	writeInputFixture(t, root, "target with space\nand newline", "first")
	link := filepath.Join(root, "link")
	if err := os.Symlink("target with space\nand newline", link); err != nil {
		t.Fatal(err)
	}
	before, err := fingerprintInput(link, false)
	if err != nil {
		t.Fatal(err)
	}
	if before.Link == "" || before.Resolved == "" {
		t.Fatal("link identity omitted")
	}
	writeInputFixture(t, root, "target with space\nand newline", "second")
	after, err := fingerprintInput(link, false)
	if err != nil {
		t.Fatal(err)
	}
	if before.SHA256 == after.SHA256 {
		t.Fatal("resolved link bytes omitted")
	}
	cycle := filepath.Join(root, "cycle")
	if err := os.Symlink("cycle", cycle); err != nil {
		t.Fatal(err)
	}
	if _, err := fingerprintInput(cycle, false); err == nil {
		t.Fatal("link cycle silently accepted")
	}
	absent, err := fingerprintInput(filepath.Join(root, "optional"), true)
	if err != nil || !absent.Absent {
		t.Fatalf("optional absence: %+v %v", absent, err)
	}
}

func TestCollectInputsNativeSourcesRemainExplicitlyIneligible(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc unavailable for cgo input discovery")
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("CGO_ENABLED", "1")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOCACHEPROG", "")
	root := inputFixture(t)
	writeInputFixture(t, root, "subject/native.go", "package subject\n/* static int value(void) { return 1; } */\nimport \"C\"\nvar Native=int(C.value())\n")
	m := mustCollectInputs(t, root)
	found := false
	for _, f := range m.Files {
		if f.Path == filepath.Join(root, "subject", "native.go") {
			found = true
		}
	}
	if !found {
		t.Fatal("cgo source omitted")
	}
	if m.Eligible || !strings.Contains(strings.Join(m.Limitations, "\n"), "system headers") {
		t.Fatal("unaccounted native inputs mislabeled eligible")
	}
}
