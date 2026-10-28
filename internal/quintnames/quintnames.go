// Package quintnames is the table of the names quint refuses as an
// identifier (a parameter, a definition): its built-in operators
// (QNT101, "Built-in name 'item' is redefined") and its keywords (a parse
// error, or QNT008). A step's field becomes a parameter by its name, so
// a field named `item` or `to` needs another name in the model: the
// scaffold maps it, the binding's lint names the fix. The table is
// checked against quint itself (quintnames_test.go: quint's own list of
// built-ins, and a parameter of every name typechecked), never kept by
// hand alone: AGENTS.md's hand-kept list once missed `item`.
package quintnames

import (
	"slices"
	"strings"
)

// Builtins are quint 0.33's built-in operators (its names/base.ts
// builtinNames, the namespaced q::* and apalache::* left out: no
// identifier can be one). Read-only.
var Builtins = []string{
	"not", "and", "or", "iff", "implies", "exists", "forall", "in", "union", "contains", "fold", "intersect",
	"exclude", "subseteq", "map", "applyTo", "filter", "powerset", "flatten", "allLists", "allListsUpTo",
	"getOnlyElement", "chooseSome", "oneOf", "isFinite", "size", "get", "put", "keys", "mapBy", "setToMap",
	"setOfMaps", "set", "setBy", "fields", "with", "tuples", "append", "concat", "head", "tail", "length", "nth",
	"indices", "replaceAt", "slice", "select", "foldl", "foldr", "to", "always", "eventually", "next", "then",
	"expect", "reps", "fail", "assert", "orKeep", "mustChange", "enabled", "weakFair", "strongFair", "leadsTo",
	"Bool", "Int", "Nat", "Set", "Map", "List", "Tup", "Rec", "range", "igt", "ilt", "igte", "ilte", "iadd",
	"isub", "iuminus", "imul", "idiv", "imod", "ipow", "actionAll", "actionAny", "field", "fieldNames", "item",
	"assign", "of", "eq", "neq", "ite", "cross", "difference", "matchVariant", "variant",
}

// Keywords are quint 0.33's keywords that are no built-in: a parameter
// of one of these names does not parse (QNT000, or QNT008 for the
// operators' keywords). `from` and `as` are keywords only after an
// import, and `init` and `step` are ordinary names. Read-only.
var Keywords = []string{
	"module", "const", "var", "assume", "val", "pure", "type", "def", "action", "run", "temporal", "nondet",
	"int", "str", "bool", "all", "any", "if", "else", "match", "import", "export", "true", "false",
}

// Reserved reports whether quint refuses name as an identifier.
func Reserved(name string) bool {
	return slices.Contains(Builtins, name) || slices.Contains(Keywords, name)
}

// Rename is the name a model gives a parameter quint refuses by its
// own: name with an underscore (item_), as grist's generator names
// it. A name quint takes is itself.
func Rename(name string) string {
	if Reserved(name) {
		return name + "_"
	}
	return name
}

// Original is the step field a parameter renamed by Rename stands for
// (item for item_), and whether p is such a parameter.
func Original(p string) (string, bool) {
	if n, ok := strings.CutSuffix(p, "_"); ok && Reserved(n) {
		return n, true
	}
	return "", false
}
