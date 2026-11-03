package skills

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Check the authored bundle as well as an actual installed copy. A relative
// repository link is already broken in a consumer project, even when it
// happens to resolve from this checkout's skills directory.
func TestSkillLinksResolve(t *testing.T) {
	checkSkillLinks(t, FS(), "..")
}

func TestInstalledSkillsPortableCLI(t *testing.T) {
	cli := filepath.Join(t.TempDir(), "gotogether")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", cli, "../cmd/gotogether")
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	project := t.TempDir() // no repository files or symlinks accompany the binary
	run := func(wantOK bool, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), cli, append([]string{"skills", "install"}, args...)...)
		cmd.Dir = project
		out, err := cmd.CombinedOutput()
		if (err == nil) != wantOK {
			t.Fatalf("skills install %q: %v\n%s", args, err, out)
		}
	}
	run(true, "-target", "all")
	dirs, err := TargetDirs(project, []string{"all"})
	if err != nil || len(dirs) != 4 {
		t.Fatalf("all targets: %v, %v", dirs, err)
	}
	for _, dir := range dirs {
		checkSkillLinks(t, os.DirFS(dir), "..")
	}

	// Existing installations can be refreshed with the existing force flag.
	// Refusing overwrite must preserve the old file; refreshing must replace
	// its broken repository link without removing unrelated consumer material.
	old := filepath.Join(dirs[0], "gotogether-model", "references", "conventions.md")
	broken := []byte("[walkthrough](../../../docs/lint-onboarding.md)\n")
	if err := os.WriteFile(old, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(dirs[0], "consumer-notes.txt")
	if err := os.WriteFile(custom, []byte("keep my notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(false, "-target", "all")
	if b, err := os.ReadFile(old); err != nil || !bytes.Equal(b, broken) {
		t.Fatalf("refused overwrite changed the old file: %q, %v", b, err)
	}
	run(true, "-target", "all", "-force")
	for _, dir := range dirs {
		checkSkillLinks(t, os.DirFS(dir), "..")
	}
	if b, err := os.ReadFile(custom); err != nil || string(b) != "keep my notes" {
		t.Fatalf("refresh changed consumer notes: %q, %v", b, err)
	}
}

func checkSkillLinks(t *testing.T, bundle fs.FS, repo string) {
	t.Helper()
	local, repository := 0, 0
	err := fs.WalkDir(bundle, ".", func(file string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(file, ".md") {
			return err
		}
		b, err := fs.ReadFile(bundle, file)
		if err != nil {
			return err
		}
		for _, destination := range skillMarkdownLinks(string(b)) {
			kind, err := skillLinkTarget(bundle, repo, file, destination)
			if err != nil {
				t.Errorf("%s links to %s: %v", file, destination, err)
			}
			switch kind {
			case "local":
				local++
			case "repository":
				repository++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if local < 50 || repository < 15 {
		t.Fatalf("only checked %d local and %d repository links; expected the full installed guidance", local, repository)
	}
}

// This checker covers the inline Markdown links used by the shipped skills.
// Code is masked first: a Go generic call such as connect.Pick[T](s, name)
// is not a link. Fragments and queries do not change the target file's path;
// heading-anchor spelling and external-site availability are not checked here.
func skillMarkdownLinks(doc string) []string {
	b := []byte(doc)
	mask := func(start, end int) {
		for i := start; i < end; i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	var fence byte
	fenceLen := 0
	for start := 0; start < len(b); {
		end := bytes.IndexByte(b[start:], '\n')
		if end < 0 {
			end = len(b)
		} else {
			end += start + 1
		}
		line := bytes.TrimLeft(b[start:end], " ")
		n := 0
		if len(line) > 0 && (line[0] == '`' || line[0] == '~') {
			for n < len(line) && line[n] == line[0] {
				n++
			}
		}
		if fence != 0 {
			if n >= fenceLen && line[0] == fence && len(bytes.TrimSpace(line[n:])) == 0 {
				fence = 0
			}
			mask(start, end)
		} else if n >= 3 {
			fence, fenceLen = line[0], n
			mask(start, end)
		}
		start = end
	}
	for i := 0; i < len(b); i++ {
		if b[i] != '`' {
			continue
		}
		n := 1
		for i+n < len(b) && b[i+n] == '`' {
			n++
		}
		for j := i + n; j < len(b); j++ {
			if b[j] != '`' {
				continue
			}
			m := 1
			for j+m < len(b) && b[j+m] == '`' {
				m++
			}
			if m == n {
				mask(i, j+m)
				i = j + m - 1
				break
			}
			j += m - 1
		}
	}
	link := regexp.MustCompile(`\[[^\]\n]*\]\((<[^>\n]+>|[^\s)]+)\)`)
	var out []string
	for _, m := range link.FindAllSubmatch(b, -1) {
		out = append(out, strings.Trim(string(m[1]), "<>"))
	}
	return out
}

func skillLinkTarget(bundle fs.FS, repo, file, destination string) (string, error) {
	u, err := url.Parse(destination)
	if err != nil {
		return "", err
	}
	if u.Scheme != "" || u.Host != "" {
		for _, own := range []struct {
			prefix string
			dir    bool
		}{
			{"https://github.com/chucklehead-dev/gotogether/blob/main/", false},
			{"https://github.com/chucklehead-dev/gotogether/tree/main/", true},
		} {
			if strings.HasPrefix(destination, own.prefix) {
				target, err := url.PathUnescape(strings.TrimPrefix(strings.SplitN(strings.SplitN(destination, "#", 2)[0], "?", 2)[0], own.prefix))
				if err != nil {
					return "repository", err
				}
				if !fs.ValidPath(target) {
					return "repository", fmt.Errorf("noncanonical repository path %q", target)
				}
				info, err := os.Stat(filepath.Join(repo, filepath.FromSlash(target)))
				if err == nil && info.IsDir() != own.dir {
					err = fmt.Errorf("repository URL uses the wrong blob/tree kind for %q", target)
				}
				return "repository", err
			}
		}
		return "", nil
	}
	if u.Path == "" { // a link to a heading in this file
		return "", nil
	}
	target := path.Clean(path.Join(path.Dir(file), u.Path))
	if path.IsAbs(u.Path) || !fs.ValidPath(target) {
		return "local", fmt.Errorf("relative link escapes the installed skills; use the repository's GitHub URL")
	}
	_, err = fs.Stat(bundle, target)
	return "local", err
}

func TestSkillMarkdownLinkCoverage(t *testing.T) {
	doc := "[directory](../other/references/) [fragment](thing.md#section) [query](thing.md?raw=1#section)\n" +
		"[`label`](thing.md) `connect.Pick[T](s, name)` ``grist.NewSite[T](App, name)``\n" +
		"```go\n[not a link](../../../docs/missing.md)\n```\n" +
		"~~~text\n[not a link](../../../docs/missing.md)\n~~~\n"
	want := []string{"../other/references/", "thing.md#section", "thing.md?raw=1#section", "thing.md"}
	if got := skillMarkdownLinks(doc); !slices.Equal(got, want) {
		t.Fatalf("links %q, want %q", got, want)
	}
	fixture := os.DirFS(t.TempDir())
	for _, destination := range []string{"../../../docs/missing.md", "../../../docs/missing.md#heading", "/docs/missing.md", "missing.md#heading"} {
		if _, err := skillLinkTarget(fixture, "..", "skill/references/page.md", destination); err == nil {
			t.Errorf("accepted unavailable installed target %q", destination)
		}
	}
	for _, tc := range []struct {
		path string
		ok   bool
	}{
		{"blob/main/README.md", true},
		{"tree/main/docs", true},
		{"blob/main/docs", false},
		{"tree/main/README.md", false},
		{"blob/main/does-not-exist.md", false},
		{"tree/main/does-not-exist", false},
	} {
		destination := "https://github.com/chucklehead-dev/gotogether/" + tc.path
		kind, err := skillLinkTarget(fixture, "..", "skill/SKILL.md", destination)
		if kind != "repository" || (err == nil) != tc.ok {
			t.Errorf("repository target %s: %s, %v; want valid=%v", tc.path, kind, err, tc.ok)
		}
	}
}
