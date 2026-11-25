package canon

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// A Difference is one place two forms disagree: its path in the form
// (`modules[m].declarations[m/step].expr.args[1]`) and each side's value
// there, as compact JSON ("" where a side has nothing).
type Difference struct {
	Path string
	A, B string
}

// String prints the difference as a report line.
func (d Difference) String() string {
	return fmt.Sprintf("%s: %s | %s", d.Path, orNone(d.A), orNone(d.B))
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// maxValue bounds a side's value in a Difference.
const maxValue = 240

// Diff is every place a and b differ, in path order. Arrays whose
// elements all carry a distinct id (declarations, modules by name) are
// aligned by it, so a declaration added or renamed is one difference
// there, not a shift of every declaration after it; their order is then
// compared on its own (`...declarations#order`).
func Diff(a, b *Form) []Difference {
	var out []Difference
	diff(a.Tree, b.Tree, "", &out)
	return out
}

func show(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	if len(b) > maxValue {
		return string(b[:maxValue]) + "..."
	}
	return string(b)
}

func diff(a, b any, path string, out *[]Difference) {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, Difference{path, show(a), show(b)})
			return
		}
		keys := sortedKeys(x)
		for _, k := range sortedKeys(y) {
			if _, ok := x[k]; !ok {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		for _, k := range keys {
			p := k
			if path != "" {
				p = path + "." + k
			}
			va, oka := x[k]
			vb, okb := y[k]
			switch {
			case !oka:
				*out = append(*out, Difference{p, "", show(vb)})
			case !okb:
				*out = append(*out, Difference{p, show(va), ""})
			default:
				diff(va, vb, p, out)
			}
		}
	case []any:
		y, ok := b.([]any)
		if !ok {
			*out = append(*out, Difference{path, show(a), show(b)})
			return
		}
		ka, oka := keyed(x)
		kb, okb := keyed(y)
		if oka && okb {
			diffKeyed(x, y, ka, kb, path, out)
			return
		}
		for i := range max(len(x), len(y)) {
			p := path + "[" + strconv.Itoa(i) + "]"
			switch {
			case i >= len(x):
				*out = append(*out, Difference{p, "", show(y[i])})
			case i >= len(y):
				*out = append(*out, Difference{p, show(x[i]), ""})
			default:
				diff(x[i], y[i], p, out)
			}
		}
	default:
		if show(a) != show(b) {
			*out = append(*out, Difference{path, show(a), show(b)})
		}
	}
}

// keyed is each element's key (its id, else its name) if every element
// has a distinct one.
func keyed(xs []any) ([]string, bool) {
	if len(xs) == 0 {
		return nil, false
	}
	keys := make([]string, len(xs))
	seen := map[string]bool{}
	for i, e := range xs {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, false
		}
		k, ok := m["id"].(string)
		if !ok {
			if k, ok = m["name"].(string); !ok {
				return nil, false
			}
		}
		if seen[k] {
			return nil, false
		}
		seen[k] = true
		keys[i] = k
	}
	return keys, true
}

func diffKeyed(x, y []any, ka, kb []string, path string, out *[]Difference) {
	ib := map[string]int{}
	for i, k := range kb {
		ib[k] = i
	}
	ia := map[string]int{}
	for i, k := range ka {
		ia[k] = i
	}
	var common []string
	for i, k := range ka {
		p := path + "[" + k + "]"
		j, ok := ib[k]
		if !ok {
			*out = append(*out, Difference{p, show(x[i]), ""})
			continue
		}
		common = append(common, k)
		diff(x[i], y[j], p, out)
	}
	var commonB []string
	for j, k := range kb {
		if _, ok := ia[k]; !ok {
			*out = append(*out, Difference{path + "[" + k + "]", "", show(y[j])})
			continue
		}
		commonB = append(commonB, k)
	}
	if !slices.Equal(common, commonB) {
		*out = append(*out, Difference{path + "#order", strings.Join(common, " "), strings.Join(commonB, " ")})
	}
}
