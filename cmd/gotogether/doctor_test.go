package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A build of a commit is the pseudo-version naming it; a tagged build is
// its tag; a replaced module agrees with anything doctor cannot read.
func TestDoctorVersionsAgree(t *testing.T) {
	commit := build{version: "(devel)", revision: "cf7c600884a7d523fa898e69dcc4d5b8b3db6977"}
	for _, c := range []struct {
		b    build
		v    string
		same bool
	}{
		{commit, "v0.0.0-20261028134256-cf7c600884a7", true},
		{commit, "v0.0.0-20261028121845-4703fc88a314", false},
		{commit, "replaced by ../..", true},
		{build{version: "v0.1.0"}, "v0.1.0", true},
		{build{version: "v0.1.0"}, "v0.2.0", false},
	} {
		if got := c.b.same(c.v); got != c.same {
			t.Errorf("%v same %s: %v, want %v", c.b, c.v, got, c.same)
		}
	}
	if got := installable("v0.0.0-20261028134256-cf7c600884a7"); got != "cf7c600884a7" {
		t.Errorf("installable: %s", got)
	}
}

// fakeTool writes an executable script named name into dir.
func fakeTool(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// doctor names a qweave whose woven ABI is not the core's, and one too
// old to say, each with the fix; quint's evaluator is read from its
// package and found missing.
func TestDoctorNamesSkews(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	t.Setenv("QUINT_HOME", t.TempDir())
	fakeTool(t, bin, "qweave", `echo "qweave v0.0.9 woven ABI0"`)
	p := qweavePiece(moduleInfo{version: "v0.0.0-20261028134256-cf7c600884a7"})
	if !strings.Contains(p.skew, "writes woven ABI0") || !strings.Contains(p.skew, "qweave@cf7c600884a7") {
		t.Errorf("another ABI: %+v", p)
	}
	fakeTool(t, bin, "qweave", `exit 2`)
	if p := qweavePiece(moduleInfo{}); !strings.Contains(p.skew, "predates -version") {
		t.Errorf("an old qweave: %+v", p)
	}
	fakeTool(t, bin, "qweave", `echo "qweave v0.1.0 woven ABI1"`)
	if p := qweavePiece(moduleInfo{}); p.skew != "" || p.version != "v0.1.0" {
		t.Errorf("a matching qweave: %+v", p)
	}
	// quint as npm installs it: bin/quint a link to the package's cli.js
	pkg := filepath.Join(t.TempDir(), "quint")
	os.MkdirAll(filepath.Join(pkg, "dist", "src", "rust"), 0o755)
	os.WriteFile(filepath.Join(pkg, "dist", "src", "rust", "binaryManager.js"), []byte("exports.QUINT_EVALUATOR_VERSION = 'v9.9.9';\n"), 0o644)
	fakeTool(t, filepath.Join(pkg, "dist", "src"), "cli.js", `echo 0.99.0`)
	if err := os.Symlink(filepath.Join(pkg, "dist", "src", "cli.js"), filepath.Join(bin, "quint")); err != nil {
		t.Fatal(err)
	}
	ps := quintPieces()
	if ps[0].version != "0.99.0" || ps[1].version != "v9.9.9" || !strings.Contains(ps[1].skew, "install-quint.sh") {
		t.Errorf("quint: %+v", ps)
	}
	if ps[2].version != "not installed" {
		t.Errorf("node: %+v", ps[2])
	}
}

// The report lists the formats this version reads and says whether it
// found a skew; -strict turns a skew into exit 1.
func TestDoctorReport(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	fakeTool(t, bin, "qweave", `echo "qweave v0.0.9 woven ABI0"`)
	var out bytes.Buffer
	code, err := doctor(&out, []string{"-C", t.TempDir(), "-strict"})
	if err != nil || code != 1 {
		t.Errorf("doctor -strict with a skew: %d %v", code, err)
	}
	for _, want := range []string{"gotogether.steps/1", "gotogether.binding/2", "gotogether.nav/1", "woven ABI1", "skew:", "the module requires no gotogether"} {
		if !strings.Contains(out.String(), want) && !(want == "the module requires no gotogether" && strings.Contains(out.String(), "not in a module")) {
			t.Errorf("no %q in:\n%s", want, out.String())
		}
	}
}
