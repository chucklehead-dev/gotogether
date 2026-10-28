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
// Events have versions, each old one upcast to the next (Upcast), and
// read models fold from snapshots (SnapshotEvery): docs/grist.md §2.1,
// §2.2. Slice 1 (docs/grist.md §8.1): plain and keyed read
// models, pinned Reads, commands with
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

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"
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
	rules      []*ruleInfo // in registration order
	sites      []*siteInfo
	deps       map[string]*Dependency // by name
	depOrder   []*Dependency          // in registration order, for the Catalog
	problems   []string
	// every, audit and memoSize are the read settings (Snapshots,
	// AuditSnapshots, Memo)
	every, audit, memoSize int
	// stats are the read counters (ReadStats), memos the memo's states,
	// one table per store (memo.go)
	stats readStats
	memos memos
}

// New returns an empty App named name, its reads configured by opts
// (Snapshots, AuditSnapshots, Memo).
func New(name string, opts ...AppOption) *App {
	a := &App{Name: name, events: map[string]*eventInfo{}, names: map[string]string{}, quintNames: map[string]string{},
		commands: map[string]*commandInfo{}, queries: map[string]*queryInfo{},
		every: DefaultSnapshotEvery, audit: DefaultSnapshotAudit, memoSize: DefaultMemoEntries}
	for _, o := range opts {
		if o.apply != nil {
			o.apply(a)
		}
	}
	a.events[failedType] = failedInfo()
	a.events[unknownType] = unknownInfo()
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
	q, err := catalog.QuintName(name)
	if err != nil {
		a.problems = append(a.problems, fmt.Sprintf("%s %q: %v", kind, name, err))
		return
	}
	if kind == "rule" {
		return // its model is holes the generator names, no action
	}
	actions := []string{q}
	if kind == "processor" {
		actions = append(actions, q+"Effect", q+"Outcome") // its effect steps' action, an at-most-once outcome's
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
// a float or a time, every rule on an event some block appends (a
// command's Appends, a processor's Emits or Delivers), with its refusals
// declared, every old version of an event with its upcaster to the next,
// each total, pure and keeping the event's tags on generated values (the
// rest of CheckUpcasters' checks are a test's). It returns every problem
// found, each naming the block and the fix; nil when the App may start.
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
	ps = append(ps, a.verifyRules()...)
	ps = append(ps, a.upcasterProblems(false)...)
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
