package ciconfig

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fragmentProblem says what is wrong with a changes/ fragment's text, or
// "": bullets at column 0, continuation lines indented two spaces, no
// headings, at least one bullet (changes/README.md).
func fragmentProblem(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	bullets := 0
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "- "):
			bullets++
		case l == "":
		case strings.HasPrefix(l, "  ") && bullets > 0:
		case strings.HasPrefix(l, "#"):
			return fmt.Sprintf("line %d is a heading: a fragment holds bullets only", i+1)
		default:
			return fmt.Sprintf("line %d is neither a bullet (\"- \") nor a continuation indented two spaces: %q", i+1, l)
		}
	}
	if bullets == 0 {
		return "no bullet: a fragment holds its CHANGELOG entry as \"- ...\""
	}
	return ""
}

// Every fragment of changes/ is a CHANGELOG entry ci/changelog.sh can move
// as it is.
func TestChangeFragments(t *testing.T) {
	files, err := filepath.Glob("../../changes/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no changes/README.md: the directory documents the convention")
	}
	for _, f := range files {
		if filepath.Base(f) == "README.md" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if p := fragmentProblem(string(b)); p != "" {
			t.Errorf("%s: %s (changes/README.md)", f, p)
		}
	}
	for text, want := range map[string]string{
		"- a change\n  continued\n- another\n": "",
		"":                                     "no bullet",
		"## Added\n- a change\n":               "heading",
		"- a change\ncontinued flush\n":        "neither a bullet",
		"  indented before any bullet\n":       "neither a bullet",
	} {
		if got := fragmentProblem(text); want == "" && got != "" || want != "" && !strings.Contains(got, want) {
			t.Errorf("fragmentProblem(%q) = %q, want %q", text, got, want)
		}
	}
}

// ci/changelog.sh -write moves the fragments under Unreleased, ahead of
// the entries already there, and deletes them; README.md stays.
func TestChangelogScriptAssembles(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	changes := filepath.Join(dir, "changes")
	if err := os.Mkdir(changes, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(changes, "README.md"), "# how\n")
	write(filepath.Join(changes, "alpha.md"), "- alpha\n  more alpha\n")
	write(filepath.Join(changes, "beta.md"), "- beta") // no final newline
	log := filepath.Join(dir, "CHANGELOG.md")
	write(log, "# Changelog\n\nintro\n\n## Unreleased\n\n- older\n\n## v0.0.1\n\n- first\n")
	cmd := exec.CommandContext(t.Context(), "bash", "../../ci/changelog.sh", "-write")
	cmd.Env = append(os.Environ(), "CHANGES_DIR="+changes, "CHANGELOG="+log)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "# Changelog\n\nintro\n\n## Unreleased\n\n- alpha\n  more alpha\n- beta\n- older\n\n## v0.0.1\n\n- first\n"
	if string(got) != want {
		t.Errorf("CHANGELOG:\n%s\nwant:\n%s", got, want)
	}
	left, _ := filepath.Glob(filepath.Join(changes, "*.md"))
	if len(left) != 1 || filepath.Base(left[0]) != "README.md" {
		t.Errorf("left in changes/: %v, want README.md alone", left)
	}
}
