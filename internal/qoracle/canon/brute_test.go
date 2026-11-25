package canon

import (
	"encoding/json"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The form claims: two compile IRs have equal forms exactly when one is
// the other with its ids renamed by a bijection, each declaration's
// suffixes that are no id renamed by a bijection, and each types entry's
// variables renamed by a bijection (names that embed a binder's id
// following the binder). TestAgainstBruteForce checks the claim on small
// random IRs (two declarations, a handful of ids), against every
// bijection, rather than trusting the form's own walk.

type bgen struct {
	r    *rand.Rand
	next int
	ids  []int
	// lambdas are the ids of the lambdas made, with the name each binds
	binds map[int]string
}

func (g *bgen) id() int {
	if len(g.ids) > 0 && g.r.IntN(8) == 0 {
		return g.ids[g.r.IntN(len(g.ids))] // a shared id, as an inlined alias's
	}
	g.next++
	g.ids = append(g.ids, g.next)
	return g.next
}

func num(n int) json.Number { return json.Number(strconv.Itoa(n)) }

// expr is a small expression; params are the names in scope with their
// binders' param ids.
func (g *bgen) expr(d int, params map[string]int, table map[string]any) map[string]any {
	switch k := g.r.IntN(4); {
	case d == 0 || k == 0:
		return map[string]any{"id": num(g.id()), "kind": "int", "value": num(g.r.IntN(3))}
	case k == 1 && len(params) > 0:
		names := slices.Sorted(func(yield func(string) bool) {
			for n := range params {
				if !yield(n) {
					return
				}
			}
		})
		n := names[g.r.IntN(len(names))]
		id := g.id()
		table[strconv.Itoa(id)] = map[string]any{"id": num(params[n]), "kind": "param", "name": n}
		return map[string]any{"id": num(id), "kind": "name", "name": n}
	case k == 2:
		return map[string]any{"id": num(g.id()), "kind": "app", "opcode": "iadd",
			"args": []any{g.expr(d-1, params, table), g.expr(d-1, params, table)}}
	default:
		l, p := g.id(), g.id()
		name := "p_" + strconv.Itoa(l) // the unshadower's: its own lambda's id
		if g.r.IntN(3) == 0 {
			name = "q_" + []string{"70", "80"}[g.r.IntN(2)] // an instance's copy: an id the IR lacks
		}
		inner := map[string]int{}
		for k, v := range params {
			inner[k] = v
		}
		inner[name] = p
		return map[string]any{"id": num(l), "kind": "lambda", "qualifier": "def",
			"params": []any{map[string]any{"id": num(p), "name": name}}, "expr": g.expr(d-1, inner, table)}
	}
}

func (g *bgen) typ() map[string]any {
	v := func() map[string]any {
		return map[string]any{"kind": "var", "name": []string{"_t1", "_t2"}[g.r.IntN(2)]}
	}
	if g.r.IntN(2) == 0 {
		return v()
	}
	return map[string]any{"kind": "oper", "args": []any{v()}, "res": v()}
}

// bruteDoc is a small IR: at most five ids, so that brute force can try
// every bijection.
func bruteDoc(seed uint64) map[string]any {
	for try := uint64(0); ; try++ {
		if d := tryDoc(seed*1000 + try); len(idsOf(d)) <= 5 {
			return d
		}
	}
}

func tryDoc(seed uint64) map[string]any {
	g := &bgen{r: rand.New(rand.NewPCG(seed, 1)), binds: map[int]string{}}
	table := map[string]any{}
	var decls []any
	for i := range 2 {
		decls = append(decls, map[string]any{"id": num(g.id()), "kind": "def", "name": "d" + strconv.Itoa(i),
			"qualifier": "val", "expr": g.expr(2, map[string]int{}, table)})
	}
	types := map[string]any{}
	for range g.r.IntN(3) {
		types[strconv.Itoa(g.ids[g.r.IntN(len(g.ids))])] = map[string]any{"type": g.typ()}
	}
	return map[string]any{"main": "m", "errors": []any{}, "table": table, "types": types,
		"modules": []any{map[string]any{"id": num(g.id()), "name": "m", "declarations": decls}}}
}

// relabel is doc with ids renamed by pi, the suffixes that are no id
// renamed per declaration by sigma[d], and each types entry's variables
// by tau[entry key as doc has it].
func relabel(doc map[string]any, pi map[string]string, sigma []map[string]string, tau map[string]map[string]string) map[string]any {
	declOf := map[string]int{}
	mods := doc["modules"].([]any)
	m0 := mods[0].(map[string]any)
	for i, d := range m0["declarations"].([]any) {
		collectInto(d, func(id string) { declOf[id] = i })
	}
	name := func(s string, d int) string {
		m := suffixRE.FindStringSubmatch(s)
		if m == nil {
			return s
		}
		if p, ok := pi[m[2]]; ok {
			return m[1] + "_" + p
		}
		if r, ok := sigma[d][m[2]]; ok {
			return m[1] + "_" + r
		}
		return s
	}
	var walk func(v any, d int) any
	walk = func(v any, d int) any {
		switch x := v.(type) {
		case map[string]any:
			out := map[string]any{}
			for k, e := range x {
				switch {
				case k == "id":
					out[k] = json.Number(pi[e.(json.Number).String()])
				case k == "name":
					out[k] = name(e.(string), d)
				default:
					out[k] = walk(e, d)
				}
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = walk(e, d)
			}
			return out
		}
		return v
	}
	var decls []any
	for i, d := range m0["declarations"].([]any) {
		decls = append(decls, walk(d, i))
	}
	table := map[string]any{}
	for k, e := range doc["table"].(map[string]any) {
		table[pi[k]] = walk(e, declOf[k])
	}
	types := map[string]any{}
	for k, e := range doc["types"].(map[string]any) {
		var vars func(v any) any
		vars = func(v any) any {
			x, ok := v.(map[string]any)
			if !ok {
				if l, ok := v.([]any); ok {
					out := make([]any, len(l))
					for i, e := range l {
						out[i] = vars(e)
					}
					return out
				}
				return v
			}
			out := map[string]any{}
			for kk, e := range x {
				if kk == "name" && x["kind"] == "var" {
					out[kk] = tau[k][e.(string)]
				} else {
					out[kk] = vars(e)
				}
			}
			return out
		}
		types[pi[k]] = vars(e)
	}
	return map[string]any{"main": "m", "errors": []any{}, "table": table, "types": types,
		"modules": []any{map[string]any{"id": json.Number(pi[m0["id"].(json.Number).String()]), "name": "m", "declarations": decls}}}
}

func collectInto(v any, f func(string)) {
	switch x := v.(type) {
	case map[string]any:
		if n, ok := x["id"].(json.Number); ok {
			f(n.String())
		}
		for _, e := range x {
			collectInto(e, f)
		}
	case []any:
		for _, e := range x {
			collectInto(e, f)
		}
	}
}

func idsOf(doc map[string]any) []string {
	set := map[string]bool{}
	collectInto(doc["modules"], func(id string) { set[id] = true })
	return slices.Sorted(func(yield func(string) bool) {
		for k := range set {
			if !yield(k) {
				return
			}
		}
	})
}

func perms(xs []string) [][]string {
	if len(xs) <= 1 {
		return [][]string{slices.Clone(xs)}
	}
	var out [][]string
	for i := range xs {
		rest := append(slices.Clone(xs[:i]), xs[i+1:]...)
		for _, p := range perms(rest) {
			out = append(out, append([]string{xs[i]}, p...))
		}
	}
	return out
}

func swaps(a, b string) []map[string]string {
	return []map[string]string{{a: a, b: b}, {a: b, b: a}}
}

// bruteEqual: some bijection of ids, of each declaration's suffixes and
// of each entry's variables makes a into b.
func bruteEqual(a, b map[string]any) bool {
	ia, ib := idsOf(a), idsOf(b)
	if len(ia) != len(ib) {
		return false
	}
	want, _ := json.Marshal(b)
	keys := slices.Sorted(func(yield func(string) bool) {
		for k := range a["types"].(map[string]any) {
			if !yield(k) {
				return
			}
		}
	})
	for _, p := range perms(ib) {
		pi := map[string]string{}
		for i, id := range ia {
			pi[id] = p[i]
		}
		for _, s0 := range swaps("70", "80") {
			for _, s1 := range swaps("70", "80") {
				// every assignment of a variable swap to each types entry
				for mask := range 1 << len(keys) {
					tau := map[string]map[string]string{}
					for i, k := range keys {
						tau[k] = swaps("_t1", "_t2")[mask>>i&1]
					}
					got, _ := json.Marshal(relabel(a, pi, []map[string]string{s0, s1}, tau))
					if string(got) == string(want) {
						return true
					}
				}
			}
		}
	}
	return false
}

// mutate changes one thing of doc: a literal, a lambda's suffix (its own
// id to one the IR lacks, or the other way), or an id shared or unshared.
func mutate(doc map[string]any, r *rand.Rand) map[string]any {
	b, _ := json.Marshal(doc)
	s := string(b)
	switch r.IntN(4) {
	case 0:
		s = strings.Replace(s, `"value":1`, `"value":2`, 1)
	case 1:
		s = strings.Replace(s, `"q_70"`, `"q_80"`, 1)
	case 2:
		// unshare one id: rename one occurrence of a repeated id
		for _, id := range idsOf(doc) {
			tok := `"id":` + id + `,`
			if strings.Count(s, tok) > 1 {
				s = strings.Replace(s, tok, `"id":999,`, 1)
				break
			}
		}
	default:
		s = strings.Replace(s, `"_t1"`, `"_t2"`, 1)
	}
	v, err := decode([]byte(s))
	if err != nil {
		panic(err)
	}
	return v.(map[string]any)
}

func formOf(t *testing.T, doc map[string]any) *Form {
	t.Helper()
	b, _ := json.Marshal(doc)
	f, err := Of(b, nil, Options{Kind: Compile, Tier: Read})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAgainstBruteForce(t *testing.T) {
	n := 300
	if testing.Short() {
		n = 100
	}
	var equal, differ int
	for seed := range uint64(n) {
		a := bruteDoc(seed)
		r := rand.New(rand.NewPCG(seed, 2))
		// b: a relabelled at random, then perhaps changed once
		ia := idsOf(a)
		fresh := r.Perm(60)
		pi := map[string]string{}
		for i, id := range ia {
			pi[id] = strconv.Itoa(fresh[i] + 1)
		}
		tau := map[string]map[string]string{}
		for k := range a["types"].(map[string]any) {
			tau[k] = swaps("_t1", "_t2")[r.IntN(2)]
		}
		b := relabel(a, pi, []map[string]string{swaps("70", "80")[r.IntN(2)], swaps("70", "80")[r.IntN(2)]}, tau)
		if r.IntN(2) == 0 {
			b = mutate(b, r)
		}
		canonEq := Equal(formOf(t, a), formOf(t, b))
		bruteEq := bruteEqual(a, b)
		if canonEq != bruteEq {
			ja, _ := json.Marshal(a)
			jb, _ := json.Marshal(b)
			t.Fatalf("seed %d: the form says equal=%v, brute force %v\na: %s\nb: %s\ndiff: %v", seed, canonEq, bruteEq, ja, jb, Diff(formOf(t, a), formOf(t, b)))
		}
		if bruteEq {
			equal++
		} else {
			differ++
		}
	}
	// both answers occur, so the comparison decides something
	if equal < n/10 || differ < n/10 {
		t.Fatalf("a lopsided sample: %d equal, %d not", equal, differ)
	}
	t.Logf("%d pairs: %d equal, %d not", n, equal, differ)
}
