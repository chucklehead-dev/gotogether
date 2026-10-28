package qanalysis

import (
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"

	"github.com/chucklehead-dev/gotogether/binding"
)

// The fixes of quintweave and quintaction: each a rewrite with one
// meaning, offered only where it is certain to compile and to say what
// the finding asked for.

// weaveFixes is the fix for quintweave's finding at at:
//   - an unmarked `go func() { ... }()` gets //qenv:go on the line above
//     (weaving simulates a marked function literal's goroutine; a go
//     statement of another call would need its arguments evaluated
//     first, which is not mechanical);
//   - a sync method value (`return s.mu.RUnlock`) of a method that takes
//     and returns nothing becomes the function literal that calls it,
//     which weaving rewrites.
//
// None for an embedded sync type: naming its field changes every call
// promoted through it.
func weaveFixes(pass *analysis.Pass, f *ast.File, at token.Pos) []analysis.SuggestedFix {
	path, _ := astutil.PathEnclosingInterval(f, at, at)
	for _, n := range path {
		switch x := n.(type) {
		case *ast.GoStmt:
			if x.Pos() != at {
				return nil
			}
			if _, lit := x.Call.Fun.(*ast.FuncLit); !lit {
				return nil
			}
			return []analysis.SuggestedFix{{
				Message:   "mark it //qenv:go",
				TextEdits: []analysis.TextEdit{{Pos: x.Pos(), End: x.Pos(), NewText: []byte("//qenv:go\n" + indentOf(pass, x.Pos()))}},
			}}
		case *ast.SelectorExpr:
			if x.Pos() != at && x.Sel.Pos() != at {
				continue
			}
			sel := pass.TypesInfo.Selections[x]
			if sel == nil || sel.Kind() != types.MethodVal {
				continue // s.mu of s.mu.RUnlock: the finding is the outer selector
			}
			sig, ok := sel.Type().(*types.Signature)
			if !ok || sig.Params().Len() != 0 || sig.Results().Len() != 0 {
				return nil
			}
			text := "func() { " + source(pass.Fset, x) + "() }"
			return []analysis.SuggestedFix{{
				Message:   "call it from a function literal",
				TextEdits: []analysis.TextEdit{{Pos: x.Pos(), End: x.End(), NewText: []byte(text)}},
			}}
		}
	}
	return nil
}

// indentOf is the leading white space of pos's line.
func indentOf(pass *analysis.Pass, pos token.Pos) string {
	tf := pass.Fset.File(pos)
	if tf == nil {
		return ""
	}
	src, err := pass.ReadFile(tf.Name())
	if err != nil {
		return ""
	}
	start := tf.Offset(tf.LineStart(tf.Line(pos)))
	end := start
	for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
		end++
	}
	return string(src[start:end])
}

// directive is the //quint:action comment of a, from f's comments.
func directive(f *ast.File, a binding.Annotation) *ast.Comment {
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if c.Pos() == a.At {
				return c
			}
		}
	}
	return nil
}

// renameAction is the fix that names best for the directive's action.
func renameAction(c *ast.Comment, action, best string) []analysis.SuggestedFix {
	if c == nil || best == "" {
		return nil
	}
	i := strings.Index(c.Text+" ", " action:"+action+" ")
	if i < 0 {
		return nil
	}
	pos := c.Pos() + token.Pos(i+len(" action:"))
	return []analysis.SuggestedFix{{
		Message:   "name the action " + best,
		TextEdits: []analysis.TextEdit{{Pos: pos, End: pos + token.Pos(len(action)), NewText: []byte(best)}},
	}}
}

// unrecorded is the field of the binding's lint "the binding for a uses
// .f, which this annotation does not record".
var unrecorded = regexp.MustCompile(`uses \.(\S+), which this annotation does not record`)

// recordField is the fix for a field the binding uses and the
// annotation does not record: where the function has a parameter, the
// receiver or a named result of the field's name (an observation's
// without its obs. prefix), the directive records it.
func recordField(pass *analysis.Pass, c *ast.Comment, a binding.Annotation, field string) []analysis.SuggestedFix {
	name := strings.TrimPrefix(field, "obs.")
	if c == nil || !inSignature(a.Decl, name) {
		return nil
	}
	return []analysis.SuggestedFix{{
		Message:   "record " + field + " as " + name,
		TextEdits: []analysis.TextEdit{{Pos: c.End(), End: c.End(), NewText: []byte(" " + field + ":" + name)}},
	}}
}

// inSignature reports whether name is a parameter, the receiver or a
// named result of fn.
func inSignature(fn *ast.FuncDecl, name string) bool {
	for _, fl := range []*ast.FieldList{fn.Recv, fn.Type.Params, fn.Type.Results} {
		if fl == nil {
			continue
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				if n.Name == name {
					return true
				}
			}
		}
	}
	return false
}

// signatureNames are fn's parameters', receiver's and named results'
// names.
func signatureNames(fn *ast.FuncDecl) []string {
	var out []string
	for _, fl := range []*ast.FieldList{fn.Recv, fn.Type.Params, fn.Type.Results} {
		if fl == nil {
			continue
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				if n.Name != "_" {
					out = append(out, n.Name)
				}
			}
		}
	}
	return out
}

// scopeFix is the fix for an annotation's expression that uses name,
// which is not in scope on return:
//   - name a local the function returns: a variable declared at the
//     body's top level (`id := newID()`) that every return returning it
//     by name returns in one place, whose result is unnamed and of the
//     local's type, becomes that named result (the other results named
//     _), its declaration an assignment;
//   - else a misspelling of a parameter, the receiver or a named result
//     (one edit or two away) is spelled as it.
func scopeFix(pass *analysis.Pass, c *ast.Comment, a binding.Annotation, key, name string) []analysis.SuggestedFix {
	if fix := namedResult(pass, a.Decl, name); fix != nil {
		return fix
	}
	best, bestD := "", 3
	for _, n := range signatureNames(a.Decl) {
		// a misspelling: an edit or two, in a name longer than them
		if d := distance(name, n); d < bestD && d < len(name)-1 {
			best, bestD = n, d
		}
	}
	if best == "" || c == nil {
		return nil
	}
	// the key's value, and name in it as a whole identifier
	i := strings.Index(c.Text, " "+key+":")
	if i < 0 {
		return nil
	}
	start := i + len(" "+key+":")
	end := start
	for depth, quote := 0, false; end < len(c.Text); end++ {
		ch := c.Text[end]
		if ch == '"' {
			quote = !quote
		}
		if ch == '(' || ch == '[' || ch == '{' {
			depth++
		}
		if ch == ')' || ch == ']' || ch == '}' {
			depth--
		}
		if ch == ' ' && depth == 0 && !quote {
			break
		}
	}
	m := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).FindStringIndex(c.Text[start:end])
	if m == nil {
		return nil
	}
	pos := c.Pos() + token.Pos(start+m[0])
	return []analysis.SuggestedFix{{
		Message:   "use " + best,
		TextEdits: []analysis.TextEdit{{Pos: pos, End: pos + token.Pos(len(name)), NewText: []byte(best)}},
	}}
}

// namedResult is scopeFix's first fix, or nil where it does not apply.
func namedResult(pass *analysis.Pass, fn *ast.FuncDecl, name string) []analysis.SuggestedFix {
	res := fn.Type.Results
	if res == nil || fn.Body == nil {
		return nil
	}
	var results []ast.Expr
	for _, f := range res.List {
		if len(f.Names) > 0 {
			return nil // named already: the local is another variable
		}
		results = append(results, f.Type)
	}
	// the local's declaration at the body's top level
	var obj types.Object
	var edit *analysis.TextEdit
	for _, st := range fn.Body.List {
		switch s := st.(type) {
		case *ast.AssignStmt:
			if s.Tok != token.DEFINE {
				continue
			}
			for _, l := range s.Lhs {
				if id, ok := l.(*ast.Ident); ok && id.Name == name && pass.TypesInfo.Defs[id] != nil {
					obj = pass.TypesInfo.Defs[id]
					if len(s.Lhs) == 1 { // the only new variable: := would declare nothing
						edit = &analysis.TextEdit{Pos: s.TokPos, End: s.TokPos + 2, NewText: []byte("=")}
					}
				}
			}
		case *ast.DeclStmt:
			gd, ok := s.Decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR || len(gd.Specs) != 1 {
				continue
			}
			vs := gd.Specs[0].(*ast.ValueSpec)
			if len(vs.Names) != 1 || vs.Names[0].Name != name {
				continue
			}
			obj = pass.TypesInfo.Defs[vs.Names[0]]
			text := "" // var x T: the named result is its zero value already
			if len(vs.Values) == 1 {
				text = name + " = " + source(pass.Fset, vs.Values[0])
			}
			edit = &analysis.TextEdit{Pos: s.Pos(), End: s.End(), NewText: []byte(text)}
		}
		if obj != nil {
			break
		}
	}
	if obj == nil {
		return nil
	}
	// the place every return returning it by name returns it in
	place := -1
	ok := true
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch r := n.(type) {
		case *ast.FuncLit:
			return false // its returns are its own
		case *ast.ReturnStmt:
			if len(r.Results) != len(results) {
				return true
			}
			for i, e := range r.Results {
				if id, isID := e.(*ast.Ident); isID && pass.TypesInfo.Uses[id] == obj {
					if place >= 0 && place != i {
						ok = false
					}
					place = i
				}
			}
		}
		return true
	})
	if !ok || place < 0 || !types.Identical(obj.Type(), pass.TypesInfo.TypeOf(results[place])) {
		return nil
	}
	var b strings.Builder
	b.WriteString("(")
	for i, t := range results {
		if i > 0 {
			b.WriteString(", ")
		}
		n := "_"
		if i == place {
			n = name
		}
		b.WriteString(n + " " + source(pass.Fset, t))
	}
	b.WriteString(")")
	edits := []analysis.TextEdit{{Pos: res.Pos(), End: res.End(), NewText: []byte(b.String())}}
	if edit != nil {
		edits = append(edits, *edit)
	}
	return []analysis.SuggestedFix{{Message: "name the result " + name, TextEdits: edits}}
}

// recordFix is the fix for a literal Record action the model lacks: its
// nearest action, where one is near.
func recordFix(m *binding.Model, r binding.RecordCall) []analysis.SuggestedFix {
	best := m.NearestAction(r.Action)
	if best == "" || len(r.Call.Args) < 2 {
		return nil
	}
	lit, ok := r.Call.Args[1].(*ast.BasicLit)
	if !ok {
		return nil
	}
	return []analysis.SuggestedFix{{
		Message:   "record " + best,
		TextEdits: []analysis.TextEdit{{Pos: lit.Pos(), End: lit.End(), NewText: []byte(strconv.Quote(best))}},
	}}
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
