package catalog

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// The kinds of a change between a deployed catalog and a new one, from
// least to most demanding of a rollout (docs/grist-platform.md §3.9).
const (
	// Safe rolls out as it is: readers and writers in any order.
	Safe = "safe"
	// ReadersFirst needs every node to read it before any writes it:
	// a release that reads, then one that writes.
	ReadersFirst = "readers first"
	// Breaking cannot roll over the deployed release: an event its nodes
	// write that this one cannot read, a client's call refused.
	Breaking = "breaking"
)

// A Change is one difference between a deployed catalog and a new one:
// its kind, what changed, and what to do.
type Change struct {
	Kind string
	What string
	Fix  string
}

// A Diff is a new catalog against a deployed one: its changes, the most
// demanding first.
type Diff struct {
	Changes []Change
}

// Verdict is the diff's most demanding kind: Safe when nothing needs more.
func (d Diff) Verdict() string {
	v := Safe
	for _, c := range d.Changes {
		if rank(c.Kind) > rank(v) {
			v = c.Kind
		}
	}
	return v
}

func rank(kind string) int { return slices.Index([]string{Safe, ReadersFirst, Breaking}, kind) }

// String is the diff as the catalog command prints it: the verdict, then
// each change with its fix.
func (d Diff) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", d.Verdict())
	for _, c := range d.Changes {
		fmt.Fprintf(&b, "  %s: %s", c.Kind, c.What)
		if c.Fix != "" {
			fmt.Fprintf(&b, "\n    %s", c.Fix)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// the catalog's JSON, as much of it as a diff reads
type (
	diffCatalog struct {
		Name       string          `json:"name"`
		Events     []diffEvent     `json:"events"`
		ReadModels []diffReadModel `json:"readModels"`
		Commands   []diffBlock     `json:"commands"`
		Queries    []diffBlock     `json:"queries"`
		Processors []diffProcessor `json:"processors"`
	}
	diffEvent struct {
		Name    string         `json:"name"`
		Version int            `json:"version"`
		Body    map[string]any `json:"body"`
		Tags    []string       `json:"tags"`
		Older   []diffVersion  `json:"older"`
		Writes  int            `json:"writes"`
	}
	diffVersion struct {
		Version int            `json:"version"`
		Body    map[string]any `json:"body"`
		Tags    []string       `json:"tags"`
	}
	diffReadModel struct {
		Name        string `json:"name"`
		Version     int    `json:"version"`
		Fingerprint string `json:"fingerprint"`
	}
	diffBlock struct {
		Name   string         `json:"name"`
		Args   map[string]any `json:"args"`
		Result map[string]any `json:"result"`
	}
	diffProcessor struct {
		Name string `json:"name"`
		On   string `json:"on"`
	}
)

// writes is the version the event's appends write.
func (e diffEvent) writes() int {
	if e.Writes > 0 {
		return e.Writes
	}
	return e.Version
}

// version is version v's body and tags, if the event has it.
func (e diffEvent) version(v int) (map[string]any, []string, bool) {
	if v == e.Version {
		return e.Body, e.Tags, true
	}
	for _, o := range e.Older {
		if o.Version == v {
			return o.Body, o.Tags, true
		}
	}
	return nil, nil, false
}

// CompareCatalogs is current (a catalog's JSON, Main's catalog command's)
// against deployed (the deployed release's): each change classified as
// Safe (a new command, query, processor or read model; a field added to
// an event's version, an argument added; a new version written as the
// deployed release's newest, WritesVersion), ReadersFirst (a new event
// type; a new version written at once: the deployed nodes refuse it) or
// Breaking (an event or a version removed, a version's field removed or
// changed, a tag changed, a command or query removed or its arguments'
// or result's fields removed or changed).
func CompareCatalogs(deployed, current []byte) (Diff, error) {
	var old, cur diffCatalog
	if err := json.Unmarshal(deployed, &old); err != nil {
		return Diff{}, fmt.Errorf("the deployed catalog is not a catalog's JSON (%v): write it with the deployed release's binary, `<app> catalog > deployed.json`", err)
	}
	if err := json.Unmarshal(current, &cur); err != nil {
		return Diff{}, fmt.Errorf("the catalog is not a catalog's JSON: %v", err)
	}
	if old.Name != cur.Name {
		return Diff{}, fmt.Errorf("the deployed catalog is app %s's, this one app %s's: compare an app with its own deployed release", old.Name, cur.Name)
	}
	var d Diff
	add := func(kind, what, fix string) { d.Changes = append(d.Changes, Change{kind, what, fix}) }
	oldEvents := map[string]diffEvent{}
	for _, e := range old.Events {
		oldEvents[e.Name] = e
	}
	curEvents := map[string]bool{}
	for _, e := range cur.Events {
		curEvents[e.Name] = true
		if strings.HasPrefix(e.Name, "grist.") {
			continue // grist's own
		}
		was, ok := oldEvents[e.Name]
		if !ok {
			add(ReadersFirst, fmt.Sprintf("event %s is new: the deployed nodes do not read it, so their decisions ignore it while the rollout lasts", e.Name),
				"append it only from a release after the one that reads it (its commands behind a flag set once every node runs it, docs/grist-platform.md §3.6), unless no deployed decision depends on it")
			continue
		}
		diffEvents(add, was, e)
	}
	for _, e := range old.Events {
		if !curEvents[e.Name] && !strings.HasPrefix(e.Name, "grist.") {
			add(Breaking, fmt.Sprintf("event %s is removed: the deployed nodes append it, and this release cannot read their events", e.Name),
				"keep it registered (grist.NewEvent) while any node may append it; its stored events stay in the log")
		}
	}
	diffBlocks(add, "command", old.Commands, cur.Commands)
	diffBlocks(add, "query", old.Queries, cur.Queries)
	oldProcs := map[string]bool{}
	for _, p := range old.Processors {
		oldProcs[p.Name] = true
	}
	for _, p := range cur.Processors {
		if !oldProcs[p.Name] {
			add(Safe, fmt.Sprintf("processor %s is new: it handles every %s of the log from the first, once deployed", p.Name, p.On), "")
		}
	}
	oldRMs := map[string]diffReadModel{}
	for _, rm := range old.ReadModels {
		oldRMs[rm.Name] = rm
	}
	for _, rm := range cur.ReadModels {
		switch was, ok := oldRMs[rm.Name]; {
		case !ok:
			add(Safe, fmt.Sprintf("read model %s is new", rm.Name), "")
		case was.Fingerprint != rm.Fingerprint:
			add(Safe, fmt.Sprintf("read model %s folds otherwise (its fingerprint changed): its reads refold from the start until they take their own snapshots, each release keeping its own", rm.Name), "")
		}
	}
	sort.SliceStable(d.Changes, func(i, j int) bool { return rank(d.Changes[i].Kind) > rank(d.Changes[j].Kind) })
	return d, nil
}

// diffEvents adds the changes of event type e since was.
func diffEvents(add func(kind, what, fix string), was, e diffEvent) {
	if e.Version < was.Version {
		add(Breaking, fmt.Sprintf("event %s: this release reads it up to version %d, and the deployed one appends version %d: it would refuse their events (grist.NewerVersionError)", e.Name, e.Version, was.writes()),
			fmt.Sprintf("keep version %d and its upcasters (a release reads every version ever stored)", was.Version))
	}
	for v := 1; v <= min(e.Version, was.Version); v++ {
		ob, ot, ok1 := was.version(v)
		nb, nt, ok2 := e.version(v)
		if !ok1 || !ok2 {
			continue
		}
		if !slices.Equal(ot, nt) {
			add(Breaking, fmt.Sprintf("event %s version %d's tags were %v, and are %v: the store indexed its events by the tags they were appended with", e.Name, v, ot, nt),
				"keep the tags; a tag that must change is a new event type")
		}
		removed, changed, added := diffProps(ob, nb)
		if len(removed)+len(changed) > 0 {
			add(Breaking, fmt.Sprintf("event %s version %d changed shape (%s): its stored events would decode into the new struct silently", e.Name, v, fields(removed, changed)),
				fmt.Sprintf("keep version %d's struct as it was, and register the new shape as version %d with an upcaster (grist.Upcast)", v, e.Version+1))
		}
		if len(added) > 0 {
			add(Safe, fmt.Sprintf("event %s version %d gained %v: its stored events read them as zero, and the deployed nodes ignore them", e.Name, v, added), "")
		}
	}
	if e.Version > was.Version {
		if w := e.writes(); w > was.Version {
			add(ReadersFirst, fmt.Sprintf("event %s: this release writes version %d, and the deployed nodes read it up to version %d: they would refuse its events (grist.NewerVersionError) until the rollout ends", e.Name, w, was.Version),
				fmt.Sprintf("write version %d in this release, grist.WritesVersion(%d, func(v New) Old {...}) on its NewEvent, and drop it in the next, once no deployed node runs", was.Version, was.Version))
		} else {
			add(Safe, fmt.Sprintf("event %s: this release reads version %d and writes version %d, which the deployed nodes read (readers first)", e.Name, e.Version, e.writes()),
				fmt.Sprintf("once it runs on every node, the next release drops WritesVersion and writes version %d", e.Version))
		}
	}
}

// diffBlocks adds the changes of a kind of block (commands, queries).
func diffBlocks(add func(kind, what, fix string), kind string, old, cur []diffBlock) {
	was := map[string]diffBlock{}
	for _, b := range old {
		was[b.Name] = b
	}
	now := map[string]bool{}
	for _, b := range cur {
		now[b.Name] = true
		o, ok := was[b.Name]
		if !ok {
			add(Safe, fmt.Sprintf("%s %s is new: a deployed node refuses it (404) until the rollout ends, so its clients ship after", kind, b.Name), "")
			continue
		}
		removed, changed, added := diffProps(o.Args, b.Args)
		if len(removed)+len(changed) > 0 {
			add(Breaking, fmt.Sprintf("%s %s's arguments changed (%s): the deployed clients send the old ones", kind, b.Name, fields(removed, changed)),
				"keep every argument the deployed clients send; add new ones (a missing one decodes as zero)")
		}
		if len(added) > 0 {
			add(Safe, fmt.Sprintf("%s %s takes %v too: a deployed client's call leaves them zero", kind, b.Name, added), "")
		}
		if removed, changed, _ := diffProps(o.Result, b.Result); len(removed)+len(changed) > 0 {
			add(Breaking, fmt.Sprintf("%s %s's result changed (%s): the deployed clients read the old one", kind, b.Name, fields(removed, changed)),
				"keep every field of the result the deployed clients read; add new ones")
		}
	}
	for _, b := range old {
		if !now[b.Name] {
			add(Breaking, fmt.Sprintf("%s %s is removed: the deployed clients' calls of it are refused (404)", kind, b.Name),
				"keep it until no client calls it (grist.Internal stops serving it over the API)")
		}
	}
}

// diffProps compares two JSON Schemas' properties: those removed, those
// whose schema changed and those added, each sorted.
func diffProps(old, cur map[string]any) (removed, changed, added []string) {
	op, _ := old["properties"].(map[string]any)
	np, _ := cur["properties"].(map[string]any)
	for n, s := range op {
		switch t, ok := np[n]; {
		case !ok:
			removed = append(removed, n)
		case !reflect.DeepEqual(s, t):
			changed = append(changed, n)
		}
	}
	for n := range np {
		if _, ok := op[n]; !ok {
			added = append(added, n)
		}
	}
	sort.Strings(removed)
	sort.Strings(changed)
	sort.Strings(added)
	return
}

// fields says which fields were removed and which changed.
func fields(removed, changed []string) string {
	var parts []string
	if len(removed) > 0 {
		parts = append(parts, "removed "+strings.Join(removed, ", "))
	}
	if len(changed) > 0 {
		parts = append(parts, "changed "+strings.Join(changed, ", "))
	}
	return strings.Join(parts, "; ")
}
