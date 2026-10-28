// Package schemas holds the JSON Schemas of the toolkit's YAML files, one
// per format at its current major (internal/formats' table): a binding
// file (gotogether.binding/2) and a span conversion rule file
// (gotogether.convert/1); and of the report validate -json and simulate
// -json print (gotogether.report/1, validate.Document), for a consumer
// that checks what it reads. Editors validate a file against its schema as
// it is written, given its URL (BindingURL, ConvertURL) in a first-line
// comment the YAML language server reads:
//
//	# yaml-language-server: $schema=https://raw.githubusercontent.com/chucklehead-dev/gotogether/main/schemas/gotogether.binding-2.json
//
// or in the editor's settings (VS Code's yaml.schemas, mapping
// "*.binding.yaml" to the URL). Check validates a file against one from
// Go, every problem at once with its line, as `gotogether lint` does
// before it loads a binding (whose strict decoder stops at the first).
//
// The schemas are generated from the Go types that read the files
// (binding.Binding, convert.Rules), their doc comments as descriptions:
// `GOTOGETHER_UPDATE_SCHEMA=1 go test ./schemas/` writes them anew, and
// the test fails while a checked-in one differs. A new major of a format
// is a new file beside the old one, which stays for the files written in
// it.
package schemas

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The URLs editors fetch the schemas from: the files of this directory
// on the repository's main branch.
const (
	BindingURL = "https://raw.githubusercontent.com/chucklehead-dev/gotogether/main/schemas/gotogether.binding-2.json"
	ConvertURL = "https://raw.githubusercontent.com/chucklehead-dev/gotogether/main/schemas/gotogether.convert-1.json"
	ReportURL  = "https://raw.githubusercontent.com/chucklehead-dev/gotogether/main/schemas/gotogether.report-1.json"
)

// binding, convert and report are the schemas' bytes. Read-only.
var (
	//go:embed gotogether.binding-2.json
	binding []byte
	//go:embed gotogether.convert-1.json
	convert []byte
	//go:embed gotogether.report-1.json
	report []byte
)

// Binding is the binding file's schema (gotogether.binding/2), as JSON.
func Binding() []byte { return slices.Clone(binding) }

// Convert is the span conversion rule file's schema (gotogether.convert/1),
// as JSON.
func Convert() []byte { return slices.Clone(convert) }

// Report is a command's machine-readable report's schema
// (gotogether.report/1: validate -json, simulate -json), as JSON.
func Report() []byte { return slices.Clone(report) }

// A Problem is one place where a file breaks its schema.
type Problem struct {
	// Line and Column are the place in the file (1-based).
	Line, Column int
	// Path is the place as keys and indices from the root ("actions.reserve.args").
	Path string
	// Message says what is wrong there, and what the schema allows.
	Message string
}

func (p Problem) String() string {
	at := p.Path
	if at == "" {
		at = "the document"
	}
	return fmt.Sprintf("line %d: %s: %s", p.Line, at, p.Message)
}

// Check validates YAML data against schema (one of this package's: it
// reads the subset of JSON Schema they use, and fails on a keyword
// outside it), returning every problem in the file's order. An error is
// data that is no YAML, or a schema it cannot read.
func Check(schema, data []byte) ([]Problem, error) {
	var s *node
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil, fmt.Errorf("schemas: the schema: %w", err)
	}
	if err := s.known(""); err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		if err.Error() == "EOF" {
			return []Problem{{Line: 1, Column: 1, Message: "an empty file: " + s.want()}}, nil
		}
		return nil, err
	}
	n := &doc
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		n = n.Content[0]
	}
	var ps []Problem
	s.check(n, "", &ps)
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Line != ps[j].Line {
			return ps[i].Line < ps[j].Line
		}
		return ps[i].Column < ps[j].Column
	})
	return ps, nil
}

// node is a schema, in the subset of JSON Schema the generator writes.
type node struct {
	Schema      string           `json:"$schema,omitempty"`
	ID          string           `json:"$id,omitempty"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Type        types            `json:"type,omitempty"`
	Const       any              `json:"const,omitempty"`
	Enum        []any            `json:"enum,omitempty"`
	Properties  map[string]*node `json:"properties,omitempty"`
	// Additional is additionalProperties: false, or the schema of the
	// values of keys not in Properties (a map's).
	Additional *additional `json:"additionalProperties,omitempty"`
	Required   []string    `json:"required,omitempty"`
	Items      *node       `json:"items,omitempty"`
	AnyOf      []*node     `json:"anyOf,omitempty"`

	extra map[string]json.RawMessage
}

// UnmarshalJSON reads a schema, keeping the keywords it does not know so
// that known can refuse them.
func (s *node) UnmarshalJSON(b []byte) error {
	type plain node
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	for _, k := range []string{"$schema", "$id", "title", "description", "type", "const", "enum", "properties", "additionalProperties", "required", "items", "anyOf"} {
		delete(all, k)
	}
	*s = node(p)
	s.extra = all
	return nil
}

// known fails on a keyword outside the subset Check reads: a schema
// using one would be checked for less than it says.
func (s *node) known(at string) error {
	if s == nil {
		return nil
	}
	for k := range s.extra {
		return fmt.Errorf("schemas: the schema uses %s at %q, which Check does not read", k, at)
	}
	for k, p := range s.Properties {
		if err := p.known(at + "/properties/" + k); err != nil {
			return err
		}
	}
	if s.Additional != nil {
		if err := s.Additional.Schema.known(at + "/additionalProperties"); err != nil {
			return err
		}
	}
	for i, a := range s.AnyOf {
		if err := a.known(at + "/anyOf/" + strconv.Itoa(i)); err != nil {
			return err
		}
	}
	return s.Items.known(at + "/items")
}

// types is a schema's type: one name, or several.
type types []string

func (t *types) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*t = types{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*t = many
	return nil
}

// additional is additionalProperties: a bool, or a schema.
type additional struct {
	Allowed bool
	Schema  *node
}

func (a *additional) UnmarshalJSON(b []byte) error {
	if json.Unmarshal(b, &a.Allowed) == nil {
		return nil
	}
	a.Allowed = true
	return json.Unmarshal(b, &a.Schema)
}

// kind is the JSON type of a YAML node, as a schema names it.
func kind(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "object"
	case yaml.SequenceNode:
		return "array"
	}
	switch n.ShortTag() {
	case "!!null":
		return "null"
	case "!!bool":
		return "boolean"
	case "!!int":
		return "integer"
	case "!!float":
		return "number"
	}
	return "string"
}

// value is a scalar node's value, as JSON would decode it, for const and
// enum.
func value(n *yaml.Node) any {
	if n.Kind != yaml.ScalarNode {
		return nil
	}
	var v any
	if n.Decode(&v) != nil {
		return n.Value
	}
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	}
	return v
}

func (s *node) typeOK(k string) bool {
	if len(s.Type) == 0 {
		return true
	}
	for _, t := range s.Type {
		if t == k || t == "number" && k == "integer" {
			return true
		}
	}
	return false
}

// want says what s accepts, for a problem's message.
func (s *node) want() string {
	switch {
	case s.Const != nil:
		return fmt.Sprintf("want %v", s.Const)
	case len(s.Enum) > 0:
		var vs []string
		for _, e := range s.Enum {
			vs = append(vs, fmt.Sprint(e))
		}
		return "want one of " + strings.Join(vs, ", ")
	case len(s.Type) > 0:
		return "want " + strings.Join(s.Type, " or ")
	}
	return "want what its description says"
}

// check appends n's problems against s to ps.
func (s *node) check(n *yaml.Node, at string, ps *[]Problem) {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	add := func(n *yaml.Node, at, msg string) {
		*ps = append(*ps, Problem{Line: n.Line, Column: n.Column, Path: at, Message: msg})
	}
	k := kind(n)
	if len(s.AnyOf) > 0 {
		// none fits: the alternative of the node's type that fits best
		// says why (a mapping's unknown key, not "want string")
		var best []Problem
		bestTyped := false
		for _, a := range s.AnyOf {
			var sub []Problem
			a.check(n, at, &sub)
			if len(sub) == 0 {
				best = nil
				break
			}
			typed := a.typeOK(k)
			if best == nil || typed && !bestTyped || typed == bestTyped && len(sub) < len(best) {
				best, bestTyped = sub, typed
			}
		}
		if best != nil {
			*ps = append(*ps, best...)
			return
		}
	}
	if !s.typeOK(k) {
		article := "a"
		if k == "object" || k == "array" || k == "integer" {
			article = "an"
		}
		add(n, at, fmt.Sprintf("%s %s, %s", article, k, s.want()))
		return
	}
	if s.Const != nil && fmt.Sprint(value(n)) != fmt.Sprint(s.Const) {
		add(n, at, fmt.Sprintf("%q, %s", n.Value, s.want()))
	}
	if len(s.Enum) > 0 && !slices.ContainsFunc(s.Enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(value(n)) }) {
		add(n, at, fmt.Sprintf("%q, %s", n.Value, s.want()))
	}
	switch n.Kind {
	case yaml.MappingNode:
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			seen[key.Value] = true
			p := join(at, key.Value)
			if sub, ok := s.Properties[key.Value]; ok {
				sub.check(val, p, ps)
				continue
			}
			switch {
			case s.Additional == nil || s.Additional.Allowed && s.Additional.Schema == nil:
			case s.Additional.Schema != nil:
				s.Additional.Schema.check(val, p, ps)
			default:
				add(key, p, "unknown key"+s.keysHint(key.Value))
			}
		}
		for _, r := range s.Required {
			if !seen[r] {
				add(n, at, "missing "+r+" (required)")
			}
		}
	case yaml.SequenceNode:
		if s.Items != nil {
			for i, c := range n.Content {
				s.Items.check(c, at+"["+strconv.Itoa(i)+"]", ps)
			}
		}
	}
}

// keysHint names the keys s has, the closest first, for an unknown one.
func (s *node) keysHint(key string) string {
	var keys []string
	for k := range s.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	best, bestD := "", 3
	for _, k := range keys {
		if d := distance(strings.ToLower(key), strings.ToLower(k)); d < bestD {
			best, bestD = k, d
		}
	}
	hint := ""
	if best != "" {
		hint = " (did you mean " + best + "?)"
	}
	return hint + "; the keys here: " + strings.Join(keys, ", ")
}

// distance is the edit distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(b)]
}

func join(at, key string) string {
	if at == "" {
		return key
	}
	return at + "." + key
}
