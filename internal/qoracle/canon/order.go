package canon

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// quint orders a module's declarations by its toposort (parsePhase4toposort,
// static/toposort.js): Kahn's algorithm layer by layer over the call graph
// (static/callgraph.js), and within a layer by `newLayer.sort()`, which in
// JavaScript compares the ids as strings. The order within a layer is
// therefore a function of the ids alone ("10" before "9"): shifting the
// ids reorders independent declarations, and no front end that numbers
// nodes otherwise reproduces it. The form keeps what the order means, the
// layer, as `$layer` on each declaration, and orders a layer's
// declarations by their keys.
//
// layers ports the call graph: every definition (a top-level def, a let's,
// an instance) on the walk's stack uses each id its names, applications
// and constant types resolve to through the table, and the imports that
// bring a definition of another module; an import uses the module it
// imports, an export the imports of its module. A node's layer is 0 when
// it uses nothing, else one more than the greatest layer it uses.
func layers(top map[string]any) map[string]int {
	table, _ := top["table"].(map[string]any)
	resolve := func(id string) (string, bool) {
		e, ok := table[id].(map[string]any)
		if !ok {
			return "", false
		}
		return idText(e["id"])
	}
	mods, _ := top["modules"].([]any)
	modID := map[string]string{}     // module name → its id
	definedAt := map[string]string{} // a top-level declaration's id → its module's name
	// imports[module name][imported name] = the import and instance ids
	imports := map[string]map[string][]string{}
	for _, m := range mods {
		mm, _ := m.(map[string]any)
		if mm == nil {
			continue
		}
		name, _ := mm["name"].(string)
		if id, ok := idText(mm["id"]); ok {
			modID[name] = id
		}
		imports[name] = map[string][]string{}
		decls, _ := mm["declarations"].([]any)
		for _, d := range decls {
			dm, _ := d.(map[string]any)
			if dm == nil {
				continue
			}
			id, ok := idText(dm["id"])
			if !ok {
				continue
			}
			definedAt[id] = name
			if k := dm["kind"]; k == "import" || k == "instance" {
				for _, f := range []string{"protoName", "qualifiedName"} {
					if s, ok := dm[f].(string); ok && s != "" {
						imports[name][s] = append(imports[name][s], id)
					}
				}
			}
		}
	}
	out := map[string]int{}
	// each module's graph is its own (a visitor per module): another
	// module's definition is a node that uses nothing there
	for _, m := range mods {
		mm, _ := m.(map[string]any)
		if mm == nil {
			continue
		}
		edges := map[string]map[string]bool{}
		add := func(from string, to ...string) {
			if edges[from] == nil {
				edges[from] = map[string]bool{}
			}
			for _, t := range to {
				edges[from][t] = true
			}
		}
		cur, _ := mm["name"].(string)
		importsOf := func(id string) []string {
			if host, ok := definedAt[id]; ok && host != cur {
				return imports[cur][host]
			}
			return nil
		}
		var stack []string
		use := func(u string) {
			for _, d := range stack {
				add(d, u)
				add(d, importsOf(u)...)
			}
		}
		var walk func(n any, decl bool)
		walk = func(n any, decl bool) {
			switch x := n.(type) {
			case map[string]any:
				id, hasID := idText(x["id"])
				kind, _ := x["kind"].(string)
				pushed := false
				switch {
				case kind == "def" && hasID:
					stack = append(stack, id)
					pushed = true
					add(id) // a node, though it may use nothing
					add(id, importsOf(id)...)
				case decl && kind == "instance" && hasID:
					stack = append(stack, id)
					pushed = true
					add(id)
					if p, ok := x["protoName"].(string); ok {
						if mid, ok := modID[p]; ok {
							add(id, mid)
						}
					}
				case decl && kind == "import" && hasID:
					if p, ok := x["protoName"].(string); ok {
						if mid, ok := modID[p]; ok {
							add(id, mid)
						}
					}
				case decl && kind == "export" && hasID:
					if p, ok := x["protoName"].(string); ok {
						add(id, imports[cur][p]...)
					}
				}
				if hasID && (kind == "name" || kind == "app" || kind == "const") {
					if u, ok := resolve(id); ok {
						use(u)
					}
				}
				for _, k := range sortedKeys(x) {
					walk(x[k], false)
				}
				if pushed {
					stack = stack[:len(stack)-1]
				}
			case []any:
				for _, e := range x {
					walk(e, false)
				}
			}
		}
		decls, _ := mm["declarations"].([]any)
		for _, d := range decls {
			walk(d, true)
		}
		layer := map[string]int{}
		const visiting = -1
		var of func(id string) int
		of = func(id string) int {
			if l, ok := layer[id]; ok {
				if l == visiting {
					return 0 // a cycle: quint reports it (QNT099) and drops its nodes
				}
				return l
			}
			layer[id] = visiting
			l := 0
			for u := range edges[id] {
				l = max(l, of(u)+1)
			}
			layer[id] = l
			return l
		}
		for _, d := range decls {
			dm, _ := d.(map[string]any)
			if dm == nil {
				continue
			}
			id, ok := idText(dm["id"])
			if !ok {
				continue
			}
			if l, ok := dm["$layer"]; ok { // a form's own
				if n, err := strconv.Atoi(stringOf(l)); err == nil {
					out[id] = n
					continue
				}
			}
			out[id] = of(id)
		}
	}
	return out
}

// moduleLayers is each module's layer in quint's toposort of modules
// (parsePhase3importAndNameResolution's import graph): a module uses the
// modules its imports and instances name, not its exports'. As with
// declarations, quint orders a layer's modules by their ids.
func moduleLayers(mods []any) map[string]int {
	uses := map[string][]string{}
	for _, m := range mods {
		mm, _ := m.(map[string]any)
		if mm == nil {
			continue
		}
		name, _ := mm["name"].(string)
		decls, _ := mm["declarations"].([]any)
		for _, d := range decls {
			dm, _ := d.(map[string]any)
			if dm == nil {
				continue
			}
			if k := dm["kind"]; k == "import" || k == "instance" {
				if p, ok := dm["protoName"].(string); ok {
					uses[name] = append(uses[name], p)
				}
			}
		}
	}
	layer := map[string]int{}
	var of func(n string) int
	of = func(n string) int {
		if l, ok := layer[n]; ok {
			return max(l, 0)
		}
		layer[n] = -1 // a cycle (QNT098) ends here
		l := 0
		for _, u := range uses[n] {
			l = max(l, of(u)+1)
		}
		layer[n] = l
		return l
	}
	out := map[string]int{}
	for _, m := range mods {
		if mm, ok := m.(map[string]any); ok {
			name, _ := mm["name"].(string)
			out[name] = of(name)
			if l, ok := mm["$layer"]; ok {
				if n, err := strconv.Atoi(stringOf(l)); err == nil {
					out[name] = n
				}
			}
		}
	}
	return out
}

// declOrder is a module's declarations' indices in the canonical order.
func (c *canonicalizer) declOrder(decls []any, keys []string) []int {
	return canonicalOrder(len(decls), func(i int) int {
		dm, _ := decls[i].(map[string]any)
		id, _ := idText(dm["id"])
		return c.layer[id]
	}, func(i int) string { return keys[i] })
}

// canonicalOrder is the indices of xs by (layer, key).
func canonicalOrder(n int, layer func(i int) int, key func(i int) string) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int {
		if la, lb := layer(a), layer(b); la != lb {
			return la - lb
		}
		return strings.Compare(key(a), key(b))
	})
	return idx
}

func stringOf(v any) string {
	switch x := v.(type) {
	case json.Number:
		return x.String()
	case string:
		return x
	}
	return ""
}
