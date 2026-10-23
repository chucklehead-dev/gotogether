package apisurface

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const root = "../.."

// Each published module's exported API is the recorded one. After a
// deliberate change: GOTOGETHER_UPDATE_API=1 go test ./internal/apisurface/,
// and review the diff of api/ with the change (and a CHANGELOG line).
func TestAPISurface(t *testing.T) {
	for _, m := range Modules {
		t.Run(m.Dir, func(t *testing.T) {
			got, err := Surface(root, m)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, m.File)
			if os.Getenv("GOTOGETHER_UPDATE_API") == "1" {
				if err := os.WriteFile(file, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v: write it with GOTOGETHER_UPDATE_API=1 go test ./internal/apisurface/", err)
			}
			if d := diff(string(want), string(got)); d != "" {
				t.Errorf("%s's exported API changed:\n%s\nIf deliberate: GOTOGETHER_UPDATE_API=1 go test ./internal/apisurface/, and commit %s with the change (and a CHANGELOG line)", m.Path, d, m.File)
			}
		})
	}
}

// diff lists the lines of got not in want (+) and of want not in got (-),
// each under its package.
func diff(want, got string) string {
	lines := func(s string) map[string]bool {
		m := map[string]bool{}
		pkg := ""
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(l, "== ") {
				pkg = l
			}
			m[pkg+"\x00"+l] = true
		}
		return m
	}
	w, g := lines(want), lines(got)
	var b bytes.Buffer
	for _, s := range []struct {
		from, other map[string]bool
		text        string
		sign        string
	}{{g, w, got, "+ "}, {w, g, want, "- "}} {
		pkg := ""
		for _, l := range strings.Split(s.text, "\n") {
			if strings.HasPrefix(l, "== ") {
				pkg = l
			}
			if !s.other[pkg+"\x00"+l] {
				b.WriteString(s.sign + strings.TrimPrefix(pkg, "== ") + ": " + l + "\n")
			}
		}
	}
	return b.String()
}

// The record sees what go doc -short did not: a method, a field, an
// interface's method; and not what is not API.
func TestSurfaceSeesMethodsAndFields(t *testing.T) {
	got, err := Surface(root, Modules[0])
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{
		"    method (*Network) PartitionNemesis(procs ...*Process)",
		"    field CutLength time.Duration",
		"type Rates = choice.Rates", // an alias of an internal type, and its API:
		"    method (Rates) Set(kind string, p float64) Rates",
		"func New(c Chooser) *Sim",
	} {
		if !strings.Contains(s, want+"\n") {
			t.Errorf("the record lacks %q", want)
		}
	}
	for _, not := range []string{"/internal/", "/cmd/", "healAll", "== github.com/chucklehead-dev/gotogether/qhegel"} {
		if strings.Contains(s, not) {
			t.Errorf("the core's record has %q, which is not its API", not)
		}
	}
}
