package qoracle

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func requireQuint(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint is not installed")
	}
}

// repoRoot is the repository's root, from this package's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	r, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The push tier's fixed batch: quint against quint shifted, on a model of
// instances, aliases and sum types compiled, and on programs quint
// refuses (a parse error, and a type error on the first line, where the
// shift's columns are taken back and quint doubles the error). The
// nightly runs the corpus.
func TestHarnessPushTier(t *testing.T) {
	requireQuint(t)
	root := repoRoot(t)
	progs := []Program{
		{ID: "canon-m", File: filepath.Join(root, "internal/qoracle/canon/testdata/m.qnt"), Main: "m"},
		{ID: "parse-error", File: filepath.Join(t.TempDir(), "p.qnt"), Text: "module p {\n  val a = 1 +\n}\n"},
		{ID: "first-line", File: filepath.Join(t.TempDir(), "f.qnt"), Text: "module f { val bad = 1 + true }\n"},
	}
	for _, p := range progs {
		t.Run(p.ID, func(t *testing.T) {
			t.Parallel()
			d, err := Compare(context.Background(), p, Quint{}, Shifted{}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if d != nil {
				t.Fatalf("quint and quint shifted disagree on %s: %v", p.ID, d)
			}
		})
	}
}

// A front end that differs is found, at the tier and path it differs.
func TestHarnessFindsADifference(t *testing.T) {
	requireQuint(t)
	dir := t.TempDir()
	p := Program{ID: "x", File: filepath.Join(dir, "x.qnt"), Text: "module x {\n  val a = 1 + 2\n  val b = a\n}\n"}
	d, err := Compare(context.Background(), p, Quint{}, edited{Quint{}, "1 + 2", "1 - 2"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if d == nil || d.Tier != TierParse || !strings.Contains(d.Path(), "x/a") {
		t.Fatalf("want a parse disagreement at x/a, got %v", d)
	}
	// the same error, a column later: the first error differs, nothing else
	p.Text = strings.Replace(p.Text, "val b = a", "val b = c", 1)
	d, err = Compare(context.Background(), p, Quint{}, laterError{Quint{}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if d == nil || d.Tier != TierErrors || !d.First {
		t.Fatalf("want the first error to differ, got %v", d)
	}
}

// edited is a front end that runs another on the program's text edited:
// a stand-in for a front end that gets one thing wrong.
type edited struct {
	Quint
	old, new string
}

func (e edited) Name() string { return "edited" }

func (e edited) Run(ctx context.Context, p Program, dir string) (*Output, error) {
	p.Text = strings.Replace(p.Text, e.old, e.new, 1)
	return e.Quint.Run(ctx, p, dir)
}

// laterError is quint with its typecheck's first error a column later.
type laterError struct{ Quint }

func (laterError) Name() string { return "later-error" }

func (l laterError) Run(ctx context.Context, p Program, dir string) (*Output, error) {
	out, err := l.Quint.Run(ctx, p, dir)
	if err != nil {
		return nil, err
	}
	v, err := decodeJSON(out.Typecheck)
	if err != nil {
		return nil, err
	}
	es, _ := v.(map[string]any)["errors"].([]any)
	if len(es) > 0 {
		loc := es[0].(map[string]any)["locs"].([]any)[0].(map[string]any)
		start := loc["start"].(map[string]any)
		c, _ := start["col"].(json.Number).Int64()
		start["col"] = json.Number(strconv.FormatInt(c+1, 10))
	}
	out.Typecheck, err = json.Marshal(v)
	return out, err
}
