package binding

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
)

// collectionTemplateFinding checks recorded fields, not arbitrary Quint
// expressions. The renderer quotes collection JSON when printed as a field;
// a collection parameter needs its default or an explicit conversion instead.
func (b *Binding) collectionTemplateFinding(action string, p Param, tpl *template.Template) *LintFinding {
	switch p.Shape {
	case "list", "set", "map":
	default:
		return nil
	}
	field := plainRecordedField(tpl)
	if field == "" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(field, "."), ".")
	argument := parts[0] != "obs"
	name := parts[len(parts)-1]
	identity := argument && name == p.Name
	typ := p.Type
	if typ == "" {
		typ = p.Shape
	}
	message := fmt.Sprintf("action %s arg %s (%s): %s renders a recorded field as a scalar, not a collection", action, p.Name, typ, field)
	var fix string
	if _, interned := b.Intern[name]; argument && interned {
		message += "; use a collection expression or non-interned collection JSON"
		fix = fmt.Sprintf("Field %s is interned to a scalar ID. Build a collection expression explicitly, or record a non-interned JSON collection and convert it for actions.%s.args.%s.", field, action, p.Name)
	} else if identity {
		message += "; omit this argument mapping to use the default conversion"
		fix = fmt.Sprintf("Remove actions.%s.args.%s to use the model-aware default %s.", action, p.Name, p.DefaultTemplate())
	} else {
		message += "; convert the recorded collection JSON with toQuint"
		if p.Type == "" || strings.Contains(p.Type, "?") {
			fix = fmt.Sprintf("Use a typed toQuint conversion for the collection parameter actions.%s.args.%s, with %s holding collection JSON.", action, p.Name, field)
		} else {
			fix = fmt.Sprintf("Use {{ toQuint %s %s }} for actions.%s.args.%s, with the selected field holding collection JSON.", strconv.Quote(p.Type), field, action, p.Name)
		}
	}
	return &LintFinding{Code: "binding.parameter.collection_literal", Severity: "error", Message: message, Symbol: action + "." + p.Name, Fix: fix, Confidence: "model_metadata"}
}

func plainRecordedField(tpl *template.Template) string {
	if tpl.Tree == nil || tpl.Tree.Root == nil {
		return ""
	}
	var action *parse.ActionNode
	for _, node := range tpl.Tree.Root.Nodes {
		if text, ok := node.(*parse.TextNode); ok && strings.TrimSpace(string(text.Text)) == "" {
			continue
		}
		a, ok := node.(*parse.ActionNode)
		if !ok || action != nil {
			return ""
		}
		action = a
	}
	if action == nil || len(action.Pipe.Decl) != 0 || len(action.Pipe.Cmds) != 1 || len(action.Pipe.Cmds[0].Args) != 1 {
		return ""
	}
	field, ok := action.Pipe.Cmds[0].Args[0].(*parse.FieldNode)
	if !ok {
		return ""
	}
	switch len(field.Ident) {
	case 1:
		// In particular, .outcome is mapped Quint text and can legitimately
		// contain a collection. Other reserved names are step metadata/maps.
		if reserved[field.Ident[0]] || field.Ident[0] == "seq" || field.Ident[0] == "action" {
			return ""
		}
	case 2:
		if field.Ident[0] != "args" && field.Ident[0] != "obs" {
			return ""
		}
	default:
		return ""
	}
	return "." + strings.Join(field.Ident, ".")
}
