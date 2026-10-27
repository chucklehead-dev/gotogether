package itf

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

func TestParseQuintRoundTrip(t *testing.T) {
	for _, src := range []string{
		`{ bk: { batch: 3, epoch: 1 }, gk: { epoch: 1, gen: 2 }, payload: 7, phase: Encoded }`,
		`Set(1, 2, 3)`, `Set()`, `Map(1 -> "a", 2 -> "b")`, `(1, true)`, `[1, 2]`, `Some(-4)`, `Ok`, `"x\y"`, `"a\tb"`, "\"tab\there\"", "\"two\nlines\"",
	} {
		v, err := ParseQuint(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := Parse([]byte(`{"vars":["v"],"states":[{"v":` + string(b) + `}]}`))
		if err != nil {
			t.Fatalf("%s: %v (%s)", src, err, b)
		}
		got := tr.States[0].Vars["v"]
		if got.Quint() != v.Quint() {
			t.Fatalf("%s: round trip %s -> %s", src, v.Quint(), got.Quint())
		}
		if again, err := ParseQuint(got.Quint()); err != nil || again.Quint() != v.Quint() {
			t.Fatalf("%s: reparse %v", src, err)
		}
	}
	if _, err := ParseQuint(`px(1, 2)`); err == nil {
		t.Fatal("an operator call must not parse as a literal")
	}
}

// A Quint string literal has no escapes: its characters are the string's
// ("a\tb" is a backslash and a t; a tab or a newline is written as
// itself). String wrote Go's escapes (%q), which Quint reads as another
// string, and a quote broke the module. quint itself, given each
// literal as String writes it, says it is the same string (its ITF), and
// Literal refuses a double quote, naming where.
func TestQuintStrings(t *testing.T) {
	strs := []string{"a\tb", "back\\slash", "tab\there", "two\nlines", "cr\rlf", "é ü"}
	for _, s := range strs {
		v := StrOf(s)
		back, err := ParseQuint(v.String())
		if err != nil || back.S != s {
			t.Errorf("%q: written %s, parsed back %q (%v)", s, v.String(), back.S, err)
		}
		if lit, err := v.Literal("x"); err != nil || lit != v.String() {
			t.Errorf("%q: Literal %s, %v", s, lit, err)
		}
	}
	var unq *UnquotableError
	if _, err := RecordOf("a", ListOf(StrOf("ok"), StrOf(`say "hi"`))).Literal("obs.msg"); !errors.As(err, &unq) || unq.At != "obs.msg.a[1]" {
		t.Errorf("a quote: %v, want an UnquotableError at obs.msg.a[1]", err)
	}
	skip.Quint(t)
	dir := t.TempDir()
	vals := ListOf()
	for _, s := range strs {
		vals.Elems = append(vals.Elems, StrOf(s))
	}
	spec := filepath.Join(dir, "strs.qnt")
	src := "module strs {\n  var v: List[str]\n  action init = v' = " + vals.String() + "\n  action step = v' = v\n}\n"
	if err := os.WriteFile(spec, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.itf.json")
	cmd := exec.Command("quint", "run", "--max-samples=1", "--max-steps=0", "--out-itf="+out, spec)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("quint run: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	got := tr.States[0].Vars["v"]
	for i, s := range strs {
		if i >= len(got.Elems) || got.Elems[i].S != s {
			t.Errorf("quint read %s as %v, want %q", vals.Elems[i].String(), got.Elems, s)
		}
	}
}
