// Package grist is an event-sourced application framework whose
// applications are checked against a Quint model by construction: an
// application is a fixed set of blocks (events, read models, commands,
// queries, processors) registered on an App, and because every block is
// the framework's, the framework records each block's step (qobs), runs
// on qenv's seams and knows the topology. What the application writes is
// its decisions and folds.
//
// Two rules make grain's two bugs impossible to write by accident (see
// docs/grist.md §1): a command's append is conditioned on everything its
// decision read (Reads), unless it says otherwise in words
// (Unconditioned); and a processor's own appends carry its checkpoint in
// the same transaction (Emit), so the only duplicates left are external
// effects, whose delivery guarantee is declared (Effect, AtLeastOnce),
// recorded and judged.
//
// Slice 1 (docs/grist.md §8.1): one event version, plain and keyed read
// models folded from the log on every read, pinned Reads, commands with
// read-set conditions and idempotency (a key's first answer is final,
// refusals included), queries, processors with Emit, Skip and
// Effect(AtLeastOnce), one node. The store contract is grist/store; the
// stores are grist/memstore (unit tests) and grist/simstore
// (qenv.Store); grist/gristsim runs an application in a simulation.
package grist

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// An App is a registry of blocks: the application's topology. Blocks are
// package-level values built by the constructors (NewEvent,
// NewReadModel, NewCommand, ...) on it.
type App struct {
	// Name is the application's name: its store's, and so the process of
	// every step it records.
	Name string

	events     map[string]*eventInfo
	names      map[string]string // block name -> what it is, for uniqueness
	quintNames map[string]string // Quint identifier -> block name
	eventOrder []string          // event names in registration order, for the Catalog
	commands   map[string]*commandInfo
	queries    map[string]*queryInfo
	processors []*processorInfo
	readModels []*readModelInfo
	problems   []string
}

// New returns an empty App named name.
func New(name string) *App {
	a := &App{Name: name, events: map[string]*eventInfo{}, names: map[string]string{}, quintNames: map[string]string{},
		commands: map[string]*commandInfo{}, queries: map[string]*queryInfo{}}
	a.events[failedType] = failedInfo()
	return a
}

// register claims a block's name, noting a problem (Verify reports it)
// when it is taken or does not map to a Quint identifier.
func (a *App) register(kind, name string) {
	if prev, ok := a.names[name]; ok {
		a.problems = append(a.problems, fmt.Sprintf("%s %q: the name is taken by a %s: give every block its own name", kind, name, prev))
		return
	}
	a.names[name] = kind
	if err := checkText(kind+" name", name); err != nil {
		a.problems = append(a.problems, fmt.Sprintf("%s %q: %v", kind, name, strings.TrimPrefix(err.Error(), "grist: ")))
	}
	if strings.HasPrefix(name, "grist") {
		a.problems = append(a.problems, fmt.Sprintf("%s %q: names starting with grist are the framework's (grist.failed): rename it", kind, name))
	}
	if kind == "event" || kind == "read model" {
		return // not actions of the model
	}
	q, err := QuintName(name)
	if err != nil {
		a.problems = append(a.problems, fmt.Sprintf("%s %q: %v", kind, name, err))
		return
	}
	actions := []string{q}
	if kind == "processor" {
		actions = append(actions, q+"Effect") // its effect steps' action
	}
	for _, n := range actions {
		if prev, ok := a.quintNames[n]; ok {
			a.problems = append(a.problems, fmt.Sprintf("%s %q maps to the Quint action %s, as %q does: rename one", kind, name, n, prev))
		}
		a.quintNames[n] = name
	}
}

// Verify checks the registry as boot does (docs/grist.md §2.9, slice 1's
// part): every name unique and a Quint identifier once mapped, every
// event a block consumes or emits registered on this App, no event field
// a float or a time. It returns every problem found, each naming the
// block and the fix; nil when the App may start.
func (a *App) Verify() error {
	ps := append([]string(nil), a.problems...)
	for _, rm := range a.readModels {
		for _, t := range rm.types {
			if a.events[t] == nil {
				ps = append(ps, fmt.Sprintf("read model %q folds %q, an event of another App: register it on %s", rm.name, t, a.Name))
			}
		}
	}
	for _, p := range a.processors {
		if a.events[p.on] == nil {
			ps = append(ps, fmt.Sprintf("processor %q subscribes to %q, an event of another App: register it on %s", p.name, p.on, a.Name))
		}
	}
	if len(ps) == 0 {
		return nil
	}
	sort.Strings(ps)
	return fmt.Errorf("grist: app %s does not verify:\n  %s", a.Name, strings.Join(ps, "\n  "))
}

// Commands are the names of the App's commands, sorted.
func (a *App) Commands() []string { return sortedKeys(a.commands) }

// Queries are the names of the App's queries, sorted.
func (a *App) Queries() []string { return sortedKeys(a.queries) }

// Processors are the names of the App's processors, in registration order.
func (a *App) Processors() []string {
	out := make([]string, len(a.processors))
	for i, p := range a.processors {
		out[i] = p.name
	}
	return out
}

// quintReserved are names Quint will not take as an action's (its
// keywords and the built-ins a model most often meets): `to` fails to
// parse (QNT101). Read-only.
var quintReserved = map[string]bool{
	"to": true, "and": true, "or": true, "not": true, "all": true, "any": true, "if": true, "else": true, "iff": true,
	"implies": true, "val": true, "def": true, "var": true, "const": true, "action": true, "run": true, "temporal": true,
	"nondet": true, "pure": true, "import": true, "export": true, "module": true, "type": true, "assume": true,
	"init": true, "step": true, "match": true, "true": true, "false": true, "Set": true, "List": true, "Map": true,
	"Rec": true, "Tup": true, "Bool": true, "Int": true, "Nat": true, "exists": true, "forall": true,
}

// QuintName maps a block's name to its Quint action's: kebab case to
// camel case ("create-counter" is createCounter). A name that does not
// map to an identifier, or maps to a reserved one, is an error with the
// fix.
func QuintName(name string) (string, error) {
	parts := strings.Split(name, "-")
	var b strings.Builder
	for i, p := range parts {
		if p == "" {
			return "", fmt.Errorf("empty segment: write names as lower-case words joined by single dashes (create-counter)")
		}
		for j, r := range p {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z') || i == 0 && j == 0 && !unicode.IsLetter(r) {
				return "", fmt.Errorf("%q is no Quint identifier once mapped: letters and digits joined by dashes, a letter first (create-counter)", name)
			}
		}
		if i == 0 {
			b.WriteString(p)
		} else {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	q := b.String()
	if quintReserved[q] {
		return "", fmt.Errorf("maps to %s, which Quint reserves: rename it", q)
	}
	return q, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// eventNames are the App's event types, sorted, for an error's fix.
func (a *App) eventNames() []string { return sortedKeys(a.events) }
