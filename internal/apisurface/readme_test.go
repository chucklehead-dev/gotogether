package apisurface

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/chucklehead-dev/gotogether/"

// The README's package table is the API as a user first meets it: a row
// for every package with an exported API (a separate module's row covers
// its packages), a package for every row, and every exported name a row
// cites one its package declares, as a function, type, constant,
// variable, method or field (connect's FaultScript stayed in the table
// after it moved to qtest).
func TestReadmePackageTable(t *testing.T) {
	names := map[string]map[string]bool{}   // package (module-relative) -> its names: top-level, methods, fields
	module := map[string]string{}           // package -> its module's directory
	members := map[string]map[string]bool{} // "package.Type" -> its methods and fields
	for _, m := range Modules {
		b, err := os.ReadFile(filepath.Join(root, m.File))
		if err != nil {
			t.Fatal(err)
		}
		var cur map[string]bool
		var rel, typ string // the package, and the type whose members follow
		for _, l := range strings.Split(string(b), "\n") {
			if p, ok := strings.CutPrefix(l, "== "); ok {
				p, _, _ = strings.Cut(p, " ") // "path [sim]": the same package under tags
				rel = strings.TrimPrefix(p, modulePath)
				if names[rel] == nil {
					names[rel] = map[string]bool{}
				}
				cur, module[rel] = names[rel], m.Dir
				continue
			}
			f := strings.Fields(l)
			if cur == nil || len(f) < 2 {
				continue
			}
			name := f[1]
			member := strings.HasPrefix(l, " ")
			switch f[0] {
			case "type":
				typ = rel + "." + base(name)
			case "func", "const", "var":
				typ = ""
			case "field":
			case "method": // method (T) Name(...)
				if len(f) < 3 {
					continue
				}
				name = f[2]
			default:
				continue
			}
			cur[base(name)] = true
			if member && typ != "" {
				if members[typ] == nil {
					members[typ] = map[string]bool{}
				}
				members[typ][base(name)] = true
			}
		}
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("^\\| `([a-z0-9/]+)`( \\(module\\))? \\| (.*) \\|$")
	cited := regexp.MustCompile("`([^`]+)`")
	ident := regexp.MustCompile(`^(?:([a-z][a-z0-9]*)\.)?([A-Z][A-Za-z0-9]*)(?:\.([A-Z][A-Za-z0-9]*))?`)
	rows := map[string]bool{}
	for _, l := range strings.Split(string(readme), "\n") {
		m := row.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		pkg := m[1]
		rows[pkg] = true
		own, ok := names[pkg]
		if !ok {
			if _, err := os.Stat(filepath.Join(root, pkg)); err != nil {
				t.Errorf("README's package table: `%s` is no package of the module", pkg)
			}
			continue // a package with no exported API (an aspect, a command)
		}
		for _, c := range cited.FindAllStringSubmatch(m[3], -1) {
			id := ident.FindStringSubmatch(c[1])
			if id == nil {
				continue // a directive, a file, a flag
			}
			in, where := own, pkg
			if q := id[1]; q != "" {
				if in = names[q]; in == nil {
					continue // not a package (nemesis.crash)
				}
				where = q
			}
			if !in[id[2]] {
				t.Errorf("README's package table: %s's row cites `%s`, which %s does not export (api/*.txt)", pkg, c[1], where)
				continue
			}
			if m := id[3]; m != "" { // Type.Member: the type's own method or field
				if ms, isType := members[where+"."+id[2]]; isType && !ms[m] {
					t.Errorf("README's package table: %s's row cites `%s`, but %s.%s has no method or field %s (api/*.txt)", pkg, c[1], where, id[2], m)
				}
			}
		}
	}
	var missing []string
	for p := range names {
		if !rows[p] && !rows[module[p]] && !strings.Contains(p, "internal") && !strings.HasPrefix(p, "cmd/") {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	for _, p := range missing {
		t.Errorf("README's package table has no row for %s", p)
	}
}

// base is a declared name without its parameters or signature.
func base(name string) string {
	return strings.FieldsFunc(name, func(r rune) bool { return r == '(' || r == '[' })[0]
}
