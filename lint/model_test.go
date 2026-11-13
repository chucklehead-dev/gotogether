package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// The source checks find what golem's design models hit (#144) and
// nothing in correct Quint: the cases each say what they must report.
func TestModelSourceFindings(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string // code@line:col
	}{
		{"or in an assignment", "module m {\n  action mark(h: bool): bool = all { seen' = seen or h, n' = n + 1 }\n}", []string{"model.assignment.precedence@2:38"}},
		{"and on its own line", "module m {\n  action a = x' = y and\n    z\n}", []string{"model.assignment.precedence@2:14"}},
		{"parenthesized", "module m {\n  action a = all { seen' = (seen or h), n' = n }\n}", nil},
		{"inside if", "module m {\n  action a = x' = if (p or q) 1 else 2\n}", nil},
		{"method call", "module m {\n  action a = x' = s.or(t)\n}", nil},
		{"comparison", "module m {\n  val v = x' == y or z\n}", nil},
		{"next declaration", "module m {\n  action a = x' = 1\n  val v = p or q\n}", nil},
		{"a comment or string", "module m {\n  // x' = a or b\n  val s = \"x' = a or b\"\n}", nil},
		{"reserved var", "module m {\n  var next: int\n}", []string{"model.name.reserved@2:7"}},
		{"reserved parameter", "module m {\n  action bump(to: int, ok: bool): bool = ok\n}", []string{"model.name.reserved@2:15"}},
		{"reserved def", "module m {\n  pure def export(x: int): int = x\n}", []string{"model.name.reserved@2:12"}},
		{"names taken as they are", "module m {\n  var nexts: int\n  action bump(to_: int, record: bool): bool = record\n}", nil},
	} {
		var got []string
		for _, d := range modelSourceFindings("m.qnt", tc.src) {
			got = append(got, d.Code+"@"+itoa(d.Line)+":"+itoa(d.Column))
		}
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func itoa(n int) string {
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	if s == "" {
		return "0"
	}
	return s
}

// A model with no binding is linted end to end: its source checks, and
// quint's effect-unification dump for branches that assign different
// variables turned into the variables each assigns, at quint's position.
func TestInspectModelWithoutBinding(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	bad := filepath.Join(dir, "unassigned.qnt")
	if err := os.WriteFile(bad, []byte("module unassigned {\n  var a: int\n  var b: int\n  action init = all { a' = 0, b' = 0 }\n  action incA = a' = a + 1\n  action step = any { incA, all { a' = a, b' = b + 1 } }\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "good.qnt")
	if err := os.WriteFile(good, []byte("module good {\n  var seen: bool\n  action init = seen' = false\n  action step = { nondet h = Bool.oneOf() seen' = (seen or h) }\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Inspect(t.Context(), Options{Models: []string{bad, good}, Quint: "quint"})
	var mismatch *Diagnostic
	for i, d := range r.Findings {
		if d.Path == good {
			t.Errorf("a correct model has a finding: %+v", d)
		}
		if d.Code == "model.assignment.mismatch" {
			mismatch = &r.Findings[i]
		}
	}
	if mismatch == nil || mismatch.Line != 6 || !strings.Contains(mismatch.Message, "one assigns a, another a, b") {
		t.Fatalf("no readable mismatch for the branches of step: %+v", r.Findings)
	}
	statuses := map[string]CheckStatus{}
	for _, c := range r.Checks {
		statuses[c.Name+" "+filepath.Base(c.Target)] = c.Status
	}
	if statuses["model.typecheck unassigned.qnt"] != CheckFail || statuses["model.typecheck good.qnt"] != CheckPass || statuses["model.source good.qnt"] != CheckPass {
		t.Fatalf("checks: %v", statuses)
	}
	if r.ExitCode() == 0 {
		t.Fatal("a failing model lint exited 0")
	}
}

// Review of #195 (on #97): quint that cannot start judged nothing, so the
// check is blocked and the lint exits 2, with no finding against the model.
func TestInspectModelQuintUnavailable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.qnt")
	if err := os.WriteFile(p, []byte("module m {\n  var x: int\n  action init = x' = 0\n  action step = x' = x\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Inspect(t.Context(), Options{Models: []string{p}, Quint: filepath.Join(t.TempDir(), "no-such-quint")})
	for _, f := range r.Findings {
		if f.Code != "model.typecheck.unavailable" {
			t.Errorf("a missing quint produced a finding against the model: %+v", f)
		}
	}
	if r.ExitCode() != 2 {
		t.Fatalf("exit %d with quint unavailable; want 2: %+v", r.ExitCode(), r.Checks)
	}
}

// The mismatch is reported in the file quint names: an imported module's,
// not the root's. Combining actions that assign different variables with
// all { } is no mismatch.
func TestInspectModelMismatchInImport(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	child := "module child {\n  var a: int\n  var b: int\n  action incA = a' = a + 1\n  action init = all { a' = 0, b' = 0 }\n  action step = any { incA, all { a' = a, b' = b + 1 } }\n}\n"
	root := "module root {\n  import child.* from \"./child\"\n}\n"
	disjoint := "module disjoint {\n  var a: int\n  var b: int\n  action setA = a' = 1\n  action setB = b' = 2\n  action init = all { setA, setB }\n  action step = all { a' = a, b' = b }\n}\n"
	for name, src := range map[string]string{"child.qnt": child, "root.qnt": root, "disjoint.qnt": disjoint} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := Inspect(t.Context(), Options{Models: []string{filepath.Join(dir, "root.qnt"), filepath.Join(dir, "disjoint.qnt")}, Quint: "quint"})
	var mismatch []Diagnostic
	for _, f := range r.Findings {
		if filepath.Base(f.Path) == "disjoint.qnt" {
			t.Errorf("all { } of actions assigning different variables was flagged: %+v", f)
		}
		if f.Code == "model.assignment.mismatch" {
			mismatch = append(mismatch, f)
		}
	}
	if len(mismatch) != 1 || filepath.Base(mismatch[0].Path) != "child.qnt" || mismatch[0].Line != 6 ||
		!strings.Contains(mismatch[0].Message, "alternatives") || strings.Contains(mismatch[0].Fix, "all { } must") {
		t.Fatalf("mismatch not reported in child.qnt:6 as alternatives: %+v", mismatch)
	}
}
