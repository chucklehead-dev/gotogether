package qlint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

type runtimeImporter map[string]*types.Package

func (i runtimeImporter) Import(path string) (*types.Package, error) {
	if p := i[path]; p != nil {
		return p, nil
	}
	return nil, fmt.Errorf("unexpected import %s", path)
}

func runtimeFixture(t *testing.T, sources map[string]string) (*token.FileSet, []*ast.File, *types.Info) {
	t.Helper()
	fset := token.NewFileSet()
	imports := runtimeImporter{}
	load := func(path, source string) *types.Package {
		t.Helper()
		f, err := parser.ParseFile(fset, path+".go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		conf := types.Config{Importer: imports}
		pkg, err := conf.Check(path, fset, []*ast.File{f}, nil)
		if err != nil {
			t.Fatal(err)
		}
		imports[path] = pkg
		return pkg
	}
	load(qenvPath, `package qenv
type Sim struct{}
func (*Sim) Run(int) error { return nil }
func (*Sim) Settle(int) error { return nil }
func (*Sim) SettleCore(int, ...*Sim) error { return nil }
func (*Sim) RunUntil(int, func() bool) (bool, error) { return false, nil }
type Other struct{}
func (*Other) Run(int) error { return nil }
`)
	load(gristPath, `package grist
type Tier struct{}
func (*Tier) Settle(int) {}
`)
	var files []*ast.File
	for name, source := range sources {
		f, err := parser.ParseFile(fset, name, source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{},
		Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: imports}
	if _, err := conf.Check("example.com/app", fset, files, info); err != nil {
		t.Fatal(err)
	}
	return fset, files, info
}

func TestRuntimeResultsAreTypedAndLocateDiscardedError(t *testing.T) {
	source := `package app
import env "github.com/chucklehead-dev/gotogether/qenv"
import "github.com/chucklehead-dev/gotogether/grist"
type Alias = env.Sim
type Embedded struct { *env.Sim }
type Custom struct{}
func (*Custom) Run(int) error { return nil }
func (*Custom) Settle(int) {}
func drive(s *Alias, embedded *Embedded, other *env.Other, custom *Custom, tier *grist.Tier) error {
 s.Run(0) //want Run
 (s.Settle)(0) //want Settle
 (*env.Sim).SettleCore(s, 0, s) //want SettleCore
 embedded.Run(0) //want Run
 _ = s.Run(0) //want Run
 _, _ = s.RunUntil(0, nil) //want RunUntil
 kept, _ := s.RunUntil(0, nil) //want RunUntil
 _ = kept
 var _ = s.Settle(0) //want Settle
 var _, _ = s.RunUntil(0, nil) //want RunUntil
 _, _ = 1, s.Run(0) //want Run
 var _, _ = 1, s.Run(0) //want Run
 if _ = s.Run(0); true {} //want Run
 go s.Run(0) //want Run
 defer s.Settle(0) //want Settle
 s.RunUntil(0, nil) //want RunUntil
 s.Run(0) //qenv:ok deliberately observe only this intermediate phase
 if err := s.Run(0); err != nil { return err }
 if _, err := s.RunUntil(0, nil); err != nil { return err }
 _, err := s.RunUntil(0, nil)
 if err != nil { return err }
 other.Run(0)
 custom.Run(0)
 custom.Settle(0)
 tier.Settle(0)
 methodValue := s.Run
 methodValue(0) // Method-value dataflow is outside this bounded rule.
 return s.SettleCore(0, s)
}
`
	fset, files, info := runtimeFixture(t, map[string]string{"app.go": source})
	wanted := map[int]string{}
	for line, text := range strings.Split(source, "\n") {
		if _, method, ok := strings.Cut(text, "//want "); ok {
			wanted[line+1] = method
		}
	}
	findings := SimFileTyped(fset, files[0], info)
	seen := 0
	for _, finding := range findings {
		if finding.Code != "sim-runtime-result" {
			continue
		}
		seen++
		method, ok := wanted[finding.Line]
		if !ok || finding.What != "discarded qenv.Sim."+method+" runtime error" {
			t.Errorf("unexpected runtime finding: %+v", finding)
		}
		if finding.At == token.NoPos || finding.Column == 0 || finding.Path != "app.go" || !strings.Contains(finding.Fix, "qtest.System.Run") {
			t.Errorf("finding lost native location or action: %+v", finding)
		}
		delete(wanted, finding.Line)
	}
	if len(wanted) != 0 || seen != 15 {
		t.Fatalf("missing runtime findings: %v (found %d)", wanted, seen)
	}
	for _, finding := range SimFile(fset, files[0]) {
		if finding.Code == "sim-runtime-result" {
			t.Fatal("syntax-only lint guessed a receiver type")
		}
	}
}

func TestRuntimeResultDoesNotNeedFileLocalQenvImport(t *testing.T) {
	fset, files, info := runtimeFixture(t, map[string]string{
		"types.go": `package app
import "github.com/chucklehead-dev/gotogether/qenv"
type Sim = qenv.Sim
`,
		"run.go": `package app
func run(s *Sim) { s.Settle(0) }
`,
	})
	for _, file := range files {
		if fset.Position(file.Pos()).Filename != "run.go" {
			continue
		}
		findings := SimFileTyped(fset, file, info)
		if len(findings) != 1 || findings[0].Code != "sim-runtime-result" || findings[0].Path != "run.go" {
			t.Fatalf("file-local import gating hid typed runtime call: %+v", findings)
		}
		if findings := SimFile(fset, file); len(findings) != 0 {
			t.Fatalf("syntax-only path inferred imported alias: %+v", findings)
		}
	}
}
