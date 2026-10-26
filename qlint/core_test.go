package qlint

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The core's own code that runs inside a simulation (the scheduler, the
// doubles, the load clients and servers, recording) escapes it no more
// than a user's may: a goroutine or a wall-clock call there is marked
// //qenv:ok with why it runs outside the simulation on purpose, the
// reason required.
func TestCoreStaysInTheSimulation(t *testing.T) {
	for _, dir := range []string{"qenv", "qenv/internal/choice", "qenv/internal/history", "qenv/internal/profile", "qenv/weave", "qenv/fault", "qenv/qhttp", "qenv/qsql", "qenv/internal/keyed", "qload", "qrole", "qobs", "qrun", "qcover", "qmut"} {
		paths, err := filepath.Glob(filepath.Join("..", dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range paths {
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, p, src, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(src), "\n") {
				if _, why, ok := strings.Cut(line, "//qenv:ok"); ok && strings.TrimSpace(why) == "" {
					t.Errorf("%s:%d: //qenv:ok without its reason", p, i+1)
				}
			}
			for _, fd := range simFile(fset, f, true) {
				t.Errorf("%s (or mark it //qenv:ok with the reason it runs outside the simulation)", fd)
			}
		}
	}
}
