package schemas_test

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/convert"
	"github.com/chucklehead-dev/gotogether/schemas"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// docs are the doc comments of a package's struct fields, by type and
// field name, from its source: the schemas' descriptions.
func docs(t *testing.T, dir string) map[string]map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	out := map[string]map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			m := map[string]string{}
			for _, fl := range st.Fields.List {
				text := fl.Doc.Text()
				if text == "" {
					text = fl.Comment.Text()
				}
				for _, name := range fl.Names {
					m[name.Name] = strings.Join(strings.Fields(text), " ")
				}
			}
			out[ts.Name.Name] = m
			return true
		})
	}
	return out
}

// generator writes a schema of a Go type as the YAML decoder reads it:
// each struct an object of its yaml keys and no others, a map an object
// of its values, a slice an array; overrides replace a type's schema (a
// custom UnmarshalYAML's), or add to a field's.
type generator struct {
	docs map[string]map[string]string
	// types replace the schema of a named type
	types map[reflect.Type]map[string]any
	// fields add keywords to Type.Field's schema
	fields map[string]map[string]any
	// structs add keywords to a struct type's schema
	structs map[string]map[string]any
	// tag is the struct tag naming the keys: "yaml" (a file's, the
	// default) or "json" (a report's, whose omitempty fields are absent
	// rather than null)
	tag string
}

func (g *generator) schema(t reflect.Type) map[string]any {
	if s, ok := g.types[t]; ok {
		return s
	}
	switch t.Kind() {
	case reflect.Pointer:
		return g.schema(t.Elem())
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Interface: // a step's argument: any scalar, or a collection's JSON text
		return map[string]any{"type": []string{"string", "number", "boolean", "null"}}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": g.schema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": []string{"object", "null"}, "additionalProperties": g.schema(t.Elem())}
	case reflect.Struct:
		props := map[string]any{}
		key := g.tag
		if key == "" {
			key = "yaml"
		}
		for f := range t.Fields() {
			tag := f.Tag.Get(key)
			name, _, _ := strings.Cut(tag, ",")
			if name == "-" || !f.IsExported() {
				continue
			}
			if f.Anonymous && name == "" && key == "json" {
				embedded := g.schema(f.Type)
				for name, property := range embedded["properties"].(map[string]any) {
					props[name] = property
				}
				continue
			}
			if name == "" {
				name = strings.ToLower(f.Name)
			}
			s := map[string]any{}
			for k, v := range g.schema(f.Type) {
				s[k] = v
			}
			if d := g.docs[t.Name()][f.Name]; d != "" {
				s["description"] = d
			}
			for k, v := range g.fields[t.Name()+"."+f.Name] {
				s[k] = v
			}
			props[name] = s
		}
		s := map[string]any{"type": []string{"object", "null"}, "properties": props, "additionalProperties": false}
		for k, v := range g.structs[t.Name()] {
			s[k] = v
		}
		return s
	}
	panic("schemas: no schema for " + t.String())
}

// root is the schema of a file of format f read into t.
func (g *generator) root(t reflect.Type, f, id, title, what string) []byte {
	s := g.schema(t)
	s["type"] = "object" // a file is a mapping, not null
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	s["$id"] = id
	s["title"] = title
	s["description"] = what + " Generated from the Go type that reads it (GOTOGETHER_UPDATE_SCHEMA=1 go test ./schemas/)."
	props := s["properties"].(map[string]any)
	props["format"].(map[string]any)["const"] = f
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // descriptions keep their => and <name>
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func bindingSchema(t *testing.T) []byte {
	g := &generator{docs: docs(t, "../binding"),
		types: map[reflect.Type]map[string]any{
			// a possible is a string (a predicate, an action, a call) or
			// a mapping of name with when or steps (Possible.UnmarshalYAML)
			reflect.TypeFor[binding.Possible](): {"anyOf": []any{
				map[string]any{"type": "string", "description": "a Quint predicate over the state, an action's name, or a call naming some of its parameters' values: reserve(result = \"refused\")"},
				map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
					"name":  map[string]any{"type": "string", "description": "how reports name it"},
					"when":  map[string]any{"type": "string", "description": "a Quint predicate over the model's state"},
					"steps": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "model actions some trace takes in this order, each a name or a call"},
				}},
			}},
		},
		fields: map[string]map[string]any{
			"Binding.Intern": {"additionalProperties": map[string]any{"enum": []any{"actor", "process"}}},
		},
		structs: map[string]map[string]any{
			"Binding": {"required": []string{"spec", "module"}},
			"Within":  {"required": []string{"after", "holds", "bound"}},
		},
	}
	name, url := binding.FormatName, schemas.BindingURL
	return g.root(reflect.TypeFor[binding.Binding](), name, url, "gotogether binding",
		"A gotogether binding file ("+name+"): how a Go program's recorded steps become calls of a Quint model's actions.")
}

func reportSchema(t *testing.T) []byte {
	g := &generator{docs: docs(t, "../validate"), tag: "json",
		structs: map[string]map[string]any{
			"Document": {"required": []string{"format", "command", "verdict", "exit"}},
		},
		fields: map[string]map[string]any{
			"Document.Verdict":   {"enum": []any{"pass", "fail", "no verdict", "error"}},
			"Document.Command":   {"enum": []any{"validate", "simulate"}},
			"DocGraph.Scope":     {"enum": []any{"model_state_graph"}},
			"DocFailure.Verdict": {"enum": []any{"fail", "n/a"}},
			"Change.Kind":        {"enum": []any{"changed", "added", "removed"}},
		},
	}
	return g.root(reflect.TypeFor[validate.Document](), validate.ReportFormat, schemas.ReportURL, "gotogether report",
		"A gotogether command's machine-readable report ("+validate.ReportFormat+"): validate -json's and simulate -json's.")
}

func convertSchema(t *testing.T) []byte {
	g := &generator{docs: docs(t, "../convert"),
		structs: map[string]map[string]any{
			// a rule maps spans to an action, or drops them
			"Rule": {"required": []string{"span"}, "anyOf": []any{
				map[string]any{"required": []string{"action"}},
				map[string]any{"required": []string{"drop"}, "properties": map[string]any{"drop": map[string]any{"const": true}}},
			}},
		},
	}
	return g.root(reflect.TypeFor[convert.Rules](), convert.FormatName, schemas.ConvertURL, "gotogether span conversion rules",
		"A gotogether span conversion rule file ("+convert.FormatName+"): which OpenTelemetry spans or span events become which model steps.")
}

// The checked-in schemas are the generated ones: a field added to the
// binding or the rules without regenerating fails here.
func TestSchemasAreGenerated(t *testing.T) {
	for _, c := range []struct {
		file string
		gen  []byte
		have []byte
	}{
		{"gotogether.binding-2.json", bindingSchema(t), schemas.Binding()},
		{"gotogether.convert-1.json", convertSchema(t), schemas.Convert()},
		{"gotogether.report-1.json", reportSchema(t), schemas.Report()},
		{"gotogether.lint-1.json", lintSchema(t), schemas.Lint()},
	} {
		if os.Getenv("GOTOGETHER_UPDATE_SCHEMA") == "1" {
			if err := os.WriteFile(c.file, c.gen, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if !bytes.Equal(c.gen, c.have) {
			t.Errorf("%s differs from the schema generated from the Go types: GOTOGETHER_UPDATE_SCHEMA=1 go test ./schemas/", c.file)
		}
	}
}

// files are the repository's files whose name ends in suffix, testdata
// and generated directories left out.
func files(t *testing.T, suffix string) []string {
	var out []string
	err := filepath.WalkDir("..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") && d.Name() != "..") {
			return filepath.SkipDir
		}
		if strings.HasSuffix(p, suffix) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Every binding and rule file of the repository (the examples', the
// skills', the core's) validates against its schema, as it loads.
func TestFilesValidate(t *testing.T) {
	for _, c := range []struct {
		suffix string
		schema []byte
		load   func(string) error
	}{
		{".binding.yaml", schemas.Binding(), func(p string) error { _, err := binding.Load(p); return err }},
		{".convert.yaml", schemas.Convert(), func(p string) error { _, err := convert.Load(p); return err }},
	} {
		fs := files(t, c.suffix)
		if len(fs) < 3 {
			t.Fatalf("only %d %s files found", len(fs), c.suffix)
		}
		for _, f := range fs {
			if err := c.load(f); err != nil {
				continue // a file meant to fail to load (a test's), whatever its schema says
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			ps, err := schemas.Check(c.schema, b)
			if err != nil {
				t.Errorf("%s: %v", f, err)
			}
			for _, p := range ps {
				t.Errorf("%s: %s", f, p)
			}
		}
	}
}

// Check finds every problem at once, each at its line with what the
// schema allows: an unknown key (with the nearest), a wrong type, a
// missing required key, a format of another major, an intern scope, a
// possible's key, a rule with neither action nor drop.
func TestCheckFindsProblems(t *testing.T) {
	bad := `format: gotogether.binding/3
spec: model/stock.qnt
constants:
  INITIAL: 'Map()'
intern: {id: thread}
actions:
  reserve:
    arg: {sku: '{{.sku}}'}
invariants: neverOversold
simulate:
  possible:
    - {name: x, steps: [reserve], when2: 'true'}
  holds: [a]
  stepz: 3
`
	ps, err := schemas.Check(schemas.Binding(), []byte(bad))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range ps {
		got = append(got, p.String())
	}
	want := []string{
		`line 1: the document: missing module (required)`,
		`line 1: format: "gotogether.binding/3", want gotogether.binding/2`,
		`line 5: intern.id: "thread", want one of actor, process`,
		`line 8: actions.reserve.arg: unknown key (did you mean args?)`,
		`line 9: invariants: a string, want array`,
		`line 12: simulate.possible[0].when2: unknown key (did you mean when?)`,
		`line 14: simulate.stepz: unknown key (did you mean steps?)`,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d problems, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("problem %d:\n got %s\nwant %s", i, got[i], want[i])
		}
	}
	ps, err = schemas.Check(schemas.Convert(), []byte("rules:\n  - span: GET /x\n  - span: POST /y\n    drop: true\n  - {action: put}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Line != 2 || !strings.Contains(ps[0].Message, "missing action") || ps[1].Line != 5 || !strings.Contains(ps[1].Message, "missing span") {
		t.Errorf("rules: %v, want rule 0 without action or drop, rule 2 without span", ps)
	}
}

// A schema using a keyword Check does not read is refused, not checked
// for less than it says.
func TestCheckRefusesUnknownKeywords(t *testing.T) {
	if _, err := schemas.Check([]byte(`{"type": "object", "properties": {"a": {"pattern": "x"}}}`), []byte("a: b\n")); err == nil || !strings.Contains(err.Error(), "pattern") {
		t.Errorf("a schema with pattern: %v", err)
	}
}

// A report validate writes validates against the report's schema: its
// document, every field filled, read as the YAML Check reads.
func TestReportValidates(t *testing.T) {
	d := validate.NewDocument("validate")
	d.Verdict, d.Exit, d.Reason, d.Binding = "fail", 1, "violates r", "b.yaml"
	d.Inputs = []validate.DocInput{{Path: "day.jsonl", Format: "steps", Steps: 15, Warnings: []string{"w"}}}
	d.Partitions = []validate.DocPartition{{Actor: "a", Key: "id", Pass: 2, Of: 3, Steps: 9}}
	d.Actors = []validate.DocActor{{Actor: "a", Steps: 15, Verdict: "fail", Conformance: "fail",
		Invariants: []validate.DocInvariant{{Name: "r", Kind: "invariant", Verdict: "n/a", Cause: "lost steps"}},
		Failures: []validate.DocFailure{{Check: "conformance", Kind: "disabled", Verdict: "fail", At: "record day.jsonl:12", Brief: "b", Text: "t",
			Record: &validate.DocRecord{ID: "day.jsonl:12", Seq: 11, Step: 11, Action: "refund", Args: map[string]any{"amount": int64(500), "id": "ch_3", "ok": true},
				Time: "2026-10-02T09:00:31Z", TraceID: "cf2a", Text: "refund(amount=500, id=ch_3)"},
			Why: &validate.Explanation{Action: "refund(...)", Conditions: []validate.Condition{{In: "refund", Expr: "x", Values: []validate.NamedValue{{Name: "x", Value: "1"}}}},
				Expected: "refund(...)", Changes: []validate.Change{{Path: "charges", Kind: "added", After: "1"}}, Enabled: []string{"e"}, More: 1},
			State: map[string]string{"charges": "Map()"}}}}}
	d.Graph = &validate.DocGraph{Scope: "model_state_graph", States: 2, Starts: 1, Transitions: 2, Depth: 1, Complete: true, Bound: validate.DocGraphBound{States: 100, Depth: 0}, Unfair: []string{"read"}}
	d.Checks = []validate.DocCheck{{Name: "i", Kind: "invariant", Verdict: "holds", Detail: "d", Bound: &validate.DocBound{Kind: "complete", States: 2, Proves: true}}, {Name: "p", Kind: "possible", Verdict: "reached", Possible: &validate.DocPossible{Found: true, Search: &validate.DocPossibleSearch{Found: true, Complete: false, Nodes: 2, Depth: 1, Path: []string{"write()"}}}}}
	ps, err := schemas.Check(schemas.Report(), d.JSON())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		t.Errorf("%s", p)
	}
	d.Verdict = "maybe"
	if ps, _ := schemas.Check(schemas.Report(), d.JSON()); len(ps) != 1 {
		t.Errorf("a verdict outside the enum: %v", ps)
	}
}

// JSON's embedded protocol component is flat, and model judgments do not
// recursively contain composition results. Validate the emitted shape itself.
func TestCompositionReportValidates(t *testing.T) {
	report := &validate.Report{Actors: []*validate.Result{{Actor: "shop/t1", Conforms: validate.Pass}}, Composition: &receipt.Composition{History: "sha256:history", Peers: []receipt.Component{}, Local: []receipt.Component{{Target: "shop", Status: receipt.Pass, Applicable: true, Judgment: &receipt.History{Status: receipt.Pass, Scope: "collected_history", OtherExecutions: receipt.NotChecked, Actors: []receipt.Actor{}}}}, Protocol: receipt.Protocol{Component: receipt.Component{Target: "protocol", Status: receipt.Pass, Applicable: true}, Configured: true, Quantifier: "all_causal_orders", Checked: 2, Limit: 4, Exhaustive: true}}}
	d := validate.NewDocument("validate")
	d.Verdict = "pass"
	d.AddReport(report)
	problems, err := schemas.Check(schemas.Report(), d.JSON())
	if err != nil || len(problems) != 0 {
		t.Fatalf("composition schema: %v %v\n%s", err, problems, d.JSON())
	}
}
