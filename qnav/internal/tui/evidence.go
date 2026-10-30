package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// A recordNav retrieves a captured record, never an evaluator-generated step.
type recordNav interface {
	Record(diagnostic.RecordRef) (*diagnostic.RecordDetail, error)
}

type evidenceGroup struct {
	label  string
	detail diagnostic.Result
}

type evidenceItem struct {
	label, scope, kind string
	item               diagnostic.Item
	ref                *diagnostic.RecordRef
}

type evidenceList struct {
	items  []evidenceItem
	source entry
}

// evidenceGroups includes composition diagnoses as well as the primary model.
// Identical primary/receipt copies are one entry; cursor scopes stay separate.
func evidenceGroups(c *entry) []evidenceGroup {
	var out []evidenceGroup
	seen := map[string]bool{}
	add := func(label string, details []diagnostic.Result) {
		for _, d := range details {
			if len(d.Items) == 0 {
				continue
			}
			b, _ := json.Marshal(d)
			if seen[string(b)] {
				continue
			}
			seen[string(b)] = true
			out = append(out, evidenceGroup{label, d})
		}
	}
	if c.m != nil {
		add("model", c.m.Diagnostics)
	}
	if c.r == nil {
		return out
	}
	add("whole run", c.r.Diagnostics)
	actors := func(label string, as []receipt.Actor) {
		for _, a := range as {
			add(label+" · "+a.Actor, a.Diagnostics)
		}
	}
	actors("whole run", c.r.Receipt.Actors)
	if comp := c.r.Receipt.Composition; comp != nil {
		component := func(v receipt.Component) {
			if v.Judgment != nil {
				actors("composition · "+v.Target, v.Judgment.Actors)
			}
		}
		for _, v := range comp.Local {
			component(v)
		}
		for _, v := range comp.Peers {
			component(v)
		}
		component(comp.Protocol.Component)
	}
	for _, a := range c.r.Model {
		add("cursor · "+a.Actor, a.Diagnostics)
	}
	return out
}

func (m *Model) openEvidence() tea.Cmd {
	c := m.cur()
	if c.evidence != nil || c.record != nil {
		m.status = "back returns to the navigation cursor"
		return nil
	}
	list := &evidenceList{source: *c}
	for _, group := range evidenceGroups(c) {
		d := group.detail
		scope := group.label + " · " + d.Property + " · " + d.Scope
		for _, item := range d.Items {
			label := fmt.Sprintf("%s · %s %q · intent %q [%s]", group.label, item.Kind, item.Subject, item.Intent, item.Evidence.Status)
			if len(item.Evidence.Matches) == 0 {
				list.items = append(list.items, evidenceItem{label: label, scope: scope, item: item})
				continue
			}
			for i, match := range item.Evidence.Matches {
				prefix := label
				total := max(item.Evidence.Total, len(item.Evidence.Matches))
				if total > 1 {
					prefix += fmt.Sprintf(" · candidate %d/%d", i+1, total)
				}
				record, origin := match.Record, match.Origin
				kind := "captured record"
				if !sameRecord(record, origin) {
					kind = "projected record"
				}
				list.items = append(list.items, evidenceItem{label: prefix + " · " + kind, scope: scope, kind: kind, item: item, ref: &record})
				if !sameRecord(record, origin) {
					list.items = append(list.items, evidenceItem{label: prefix + " · native origin", scope: scope, kind: "native origin", item: item, ref: &origin})
				}
			}
		}
	}
	if len(list.items) == 0 {
		m.status = "no outstanding diagnostic items at this node"
		return nil
	}
	return func() tea.Msg { return loaded{e: entry{label: "evidence", evidence: list}} }
}

func sameRecord(a, b diagnostic.RecordRef) bool {
	return a.Actor == b.Actor && a.Recorder == b.Recorder && a.Seq == b.Seq
}

func (m *Model) inspectEvidence(list *evidenceList, item evidenceItem) tea.Cmd {
	if item.ref == nil {
		m.status = item.item.Evidence.Status + ": " + item.item.Evidence.Reason
		return nil
	}
	ref := *item.ref
	m.busy = true
	run := m.c.Run
	return func() tea.Msg {
		var record *diagnostic.RecordDetail
		var err error
		if list.source.r != nil {
			var node *navproto.Node
			node, err = run.Node(navproto.Request{Node: list.source.r.ID, Record: &ref})
			if err == nil && node != nil {
				record = node.Record
			}
		} else if nav, ok := list.source.nav.(recordNav); ok {
			record, err = nav.Record(ref)
		} else {
			err = fmt.Errorf("this navigator cannot retrieve captured records")
		}
		if err == nil && record == nil {
			err = fmt.Errorf("captured record %s seq %d was not returned", ref.Recorder, ref.Seq)
		}
		return loaded{e: entry{label: item.kind, record: record, recordKind: item.kind}, err: err}
	}
}

func (m *Model) evidenceScreen(c *entry) screen {
	sc := screen{head: []string{m.st.title.Render("gotogether-nav") + "  outstanding obligation evidence", "path: " + m.crumbs(), "Captured records only · navigation cursors unchanged"}}
	sc.branches = []string{"", m.st.section.Render(fmt.Sprintf("evidence (%d) · enter inspects selected record", len(c.evidence.items)))}
	sc.branches = append(sc.branches, m.branchLines(c, max(3, m.h/4), func(int) string { return "" })...)
	vis := m.visible()
	if c.cursor < len(vis) {
		item := c.evidence.items[vis[c.cursor].i]
		sc.detail = []string{"", "scope: " + item.scope, "kind: " + item.item.Kind, "subject: " + item.item.Subject, "intent: " + item.item.Intent, "expected: " + item.item.Expected, "evidence: " + item.item.Evidence.Status}
		if item.item.Evidence.Reason != "" {
			sc.detail = append(sc.detail, item.item.Evidence.Reason)
		}
		if item.item.Evidence.Omitted > 0 {
			sc.detail = append(sc.detail, fmt.Sprintf("%d of %d matching records omitted from this result", item.item.Evidence.Omitted, item.item.Evidence.Total))
		}
		if item.ref != nil {
			sc.detail = append(sc.detail, fmt.Sprintf("%s: actor %q · recorder %q · seq %d", item.kind, item.ref.Actor, item.ref.Recorder, item.ref.Seq))
			if item.ref.Source != "" {
				sc.detail = append(sc.detail, "source: "+item.ref.Source)
			}
		}
	}
	sc.detail = append(sc.detail, "", "Evidence matching does not identify a root cause.", "Back returns to the unchanged navigation cursor.")
	return sc
}

func (m *Model) recordScreen(c *entry) screen {
	r := c.record
	sc := screen{head: []string{m.st.title.Render("gotogether-nav") + "  " + c.recordKind, "path: " + m.crumbs(), "Captured record · no model judgment or cursor move"}}
	sc.detail = []string{fmt.Sprintf("actor: %s", r.Reference.Actor), fmt.Sprintf("recorder: %s · seq %d", r.Reference.Recorder, r.Reference.Seq), "action: " + r.Action}
	if r.Outcome != "" {
		sc.detail = append(sc.detail, "outcome: "+r.Outcome)
	}
	if r.Error != "" {
		sc.detail = append(sc.detail, "error: "+r.Error)
	}
	if r.Invocation {
		sc.detail = append(sc.detail, "invocation: true (not a completed answer)")
	}
	if r.Call != 0 {
		sc.detail = append(sc.detail, fmt.Sprintf("call: %d", r.Call))
	}
	if r.Invoked != "" {
		sc.detail = append(sc.detail, "invoked: "+r.Invoked)
	}
	if r.Time != "" {
		sc.detail = append(sc.detail, "time: "+r.Time)
	}
	if r.Source != "" {
		sc.detail = append(sc.detail, "source: "+r.Source)
	}
	if r.Process != "" {
		sc.detail = append(sc.detail, "process: "+r.Process)
	}
	if r.Thread != "" {
		sc.detail = append(sc.detail, "thread: "+r.Thread)
	}
	if r.Ref != "" {
		sc.detail = append(sc.detail, "artifact: "+r.Ref)
	}
	appendJSON := func(label string, raw json.RawMessage) {
		if len(raw) == 0 {
			return
		}
		// Indent the original bytes: decoding into float64 would round large IDs.
		var pretty bytes.Buffer
		if json.Indent(&pretty, raw, "  ", "  ") != nil {
			sc.detail = append(sc.detail, label+": "+string(raw))
			return
		}
		sc.detail = append(sc.detail, label+":")
		sc.detail = append(sc.detail, strings.Split(pretty.String(), "\n")...)
	}
	appendJSON("arguments", r.Arguments)
	appendJSON("observations", r.Observations)
	sc.detail = append(sc.detail, "", "Back returns to the evidence list.")
	return sc
}
