package binding

import "text/template/parse"

// collectKeptFields finds raw argument/observation dependencies. Evidence-origin
// templates use a smaller root than action templates, but both expose args/obs.
// A map consumed as a whole or through a dynamic index must remain intact.
func collectKeptFields(src string, origin bool, set map[string]bool) {
	t, err := parseTpl(src)
	if err != nil {
		set["args.*"], set["obs.*"] = true, true
		return
	}
	field := func(ids []string) {
		if len(ids) == 0 {
			return
		}
		switch ids[0] {
		case "obs", "args":
			prefix := ""
			if ids[0] == "obs" {
				prefix = "obs."
			}
			if len(ids) == 1 {
				set[ids[0]+".*"] = true
			} else {
				set[prefix+ids[1]] = true
			}
		case "step", "process", "outcome":
			if origin {
				set["args.*"], set["obs.*"] = true, true
			}
		default:
			if !origin {
				set[ids[0]] = true
			}
		}
	}
	var walk func(parse.Node)
	walk = func(n parse.Node) {
		switch x := n.(type) {
		case *parse.ListNode:
			if x != nil {
				for _, n := range x.Nodes {
					walk(n)
				}
			}
		case *parse.ActionNode:
			walk(x.Pipe)
		case *parse.PipeNode:
			if x != nil {
				for _, n := range x.Cmds {
					walk(n)
				}
			}
		case *parse.CommandNode:
			// index .obs "native.recorder" reads one flat observed key.
			// Other index expressions visit the map, retaining it whole.
			if len(x.Args) >= 3 {
				fn, ok := x.Args[0].(*parse.IdentifierNode)
				base, bok := x.Args[1].(*parse.FieldNode)
				key, kok := x.Args[2].(*parse.StringNode)
				if ok && fn.Ident == "index" && bok && len(base.Ident) == 1 && (base.Ident[0] == "obs" || base.Ident[0] == "args") && kok {
					field([]string{base.Ident[0], key.Text})
					for _, a := range x.Args[3:] {
						walk(a)
					}
					return
				}
			}
			for _, a := range x.Args {
				walk(a)
			}
		case *parse.FieldNode:
			field(x.Ident)
		case *parse.DotNode:
			set["args.*"], set["obs.*"] = true, true
		case *parse.ChainNode:
			walk(x.Node)
		case *parse.IfNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.WithNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.RangeNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.TemplateNode:
			walk(x.Pipe)
		}
	}
	for _, tpl := range t.Templates() {
		if tpl.Tree != nil {
			walk(tpl.Tree.Root)
		}
	}
}
