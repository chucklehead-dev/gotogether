package gristtest

import (
	"container/heap"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"
	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// ProtocolSteps projects native records of apps into one system-model
// actor per tenant (protocol/<tenant>). Judge each App's original records
// with its own binding too: this projection includes committed commands
// alone, named <app's Quint name><command's capitalized Quint name>.
// Arguments and observations stay intact; observations protocol.service,
// protocol.position, protocol.parentApp and protocol.parentID identify the
// service, its head and the commit containing the peer call's trigger.
// A peer commit with no matching recorded call has empty parent fields:
// the system model must decide whether such a command is allowed.
//
// Ordering uses each store's committed sequence and explicit causal edges
// from recorded peer calls (pending included) to their trigger commits.
// It uses neither wall clocks nor relay arrival order. Independent commits
// take one deterministic extension, ordered by App name then store seq:
// this checks that extension, not every interleaving of concurrent work.
// Original recorder/seq and source survive in each projected step's Source.
// protocol.recorder and protocol.seq observations keep that identity when
// a projected stream is written to a file and read back.
// The projected stream has fresh dense seqs; native gaps, losses, rewinds,
// ambiguous causes and cycles are refused before that renumbering. A settle
// is preserved only when every contributing native actor was settled.
func ProtocolSteps(steps []qtrace.Step, apps ...*grist.App) ([]qtrace.Step, error) {
	byName := map[string]*grist.App{}
	commands := map[string]map[string]string{}
	prefix := map[string]string{}
	for _, a := range apps {
		if a == nil || byName[a.Name] != nil {
			return nil, fmt.Errorf("gristtest.ProtocolSteps: give non-nil Apps with unique names")
		}
		q, err := catalog.QuintName(a.Name)
		if err != nil {
			return nil, fmt.Errorf("gristtest.ProtocolSteps: App %s: %w", a.Name, err)
		}
		byName[a.Name], prefix[a.Name] = a, q
		commands[a.Name] = map[string]string{}
		for _, c := range a.Catalog().Commands {
			commands[a.Name][c.Quint] = c.Name
		}
	}
	for _, a := range apps {
		for _, d := range a.Dependencies() {
			if p := d.Peer(); p != nil && byName[p.Name] != p {
				return nil, fmt.Errorf("gristtest.ProtocolSteps: %s calls peer %s: include the App declared by grist.NewPeer", a.Name, p.Name)
			}
		}
	}
	traces, issues := qtrace.Reconstruct(steps)
	for _, issue := range issues {
		if issue.Severity == "error" {
			return nil, fmt.Errorf("gristtest.ProtocolSteps: native records: %s", issue)
		}
	}
	type event struct {
		app, tenant string
		pos         int64
	}
	type node struct {
		s           qtrace.Step
		app, tenant string
		head        int64
		parent      *node
		trigger     int64
		next        []*node
		indegree    int
	}
	var nodes []*node
	parents := map[event]*node{}
	settled := map[string]bool{}
	for _, tr := range traces {
		app, tenant, clients, ok := rec.ParseActor(tr.Actor)
		if !ok || byName[app] == nil {
			continue
		}
		for _, issue := range tr.Issues {
			if issue.Severity == "error" {
				return nil, fmt.Errorf("gristtest.ProtocolSteps: %s: %s", tr.Actor, issue)
			}
		}
		if len(tr.Lost) > 0 || len(tr.Gaps) > 0 {
			return nil, fmt.Errorf("gristtest.ProtocolSteps: %s has missing native records: collect the whole history before projecting", tr.Actor)
		}
		if clients {
			continue
		}
		if _, seen := settled[tenant]; !seen {
			settled[tenant] = true
		}
		settled[tenant] = settled[tenant] && tr.Settled
		var local []*node
		for _, s := range tr.Steps {
			if s.Action == "gristRewound" || s.Action == "gristResumed" {
				return nil, fmt.Errorf("gristtest.ProtocolSteps: %s rewound: projection across log epochs is not supported", tr.Actor)
			}
			if s.Recorder != grist.RecorderID(app, tenant) {
				continue // queries, call views and effects are not commits
			}
			if s.Seq == 0 {
				return nil, fmt.Errorf("gristtest.ProtocolSteps: %s has no native sequence: keep the original recorder's sequence numbers", s.Recorder)
			}
			head, err := protocolInt(s.Obs["head"])
			var types []string
			if err == nil {
				b, ok := s.Obs["events"].(string)
				if !ok {
					raw, _ := json.Marshal(s.Obs["events"])
					b = string(raw)
				}
				err = json.Unmarshal([]byte(b), &types)
				if err == nil && types == nil {
					err = fmt.Errorf("missing event list")
				}
			}
			if err != nil || head < int64(len(types)) {
				return nil, fmt.Errorf("gristtest.ProtocolSteps: %s seq %d lacks a valid head/event batch: keep native grist commit records", s.Recorder, s.Seq)
			}
			n := &node{s: s, app: app, tenant: tenant, head: head}
			for p := head - int64(len(types)) + 1; p <= head; p++ {
				key := event{app, tenant, p}
				if parents[key] != nil {
					return nil, fmt.Errorf("gristtest.ProtocolSteps: %s position %d has two commits: collect one log epoch", tr.Actor, p)
				}
				parents[key] = n
			}
			local = append(local, n)
		}
		slices.SortFunc(local, func(a, b *node) int { return cmpUint(a.s.Seq, b.s.Seq) })
		for i := 1; i < len(local); i++ {
			local[i-1].next = append(local[i-1].next, local[i])
			local[i].indegree++
		}
		nodes = append(nodes, local...)
	}
	// The call's own records say which App, tenant and trigger caused it;
	// the catalog says which peer command it calls. Do not infer a parent
	// from an id's spelling: freshKey is precisely a transport lying there.
	type callKey struct{ peer, tenant, command, key string }
	causes := map[callKey]event{}
	type route struct{ peer, command string }
	declared := map[string]map[string]route{}
	for _, a := range apps {
		declared[a.Name] = map[string]route{}
		for _, d := range a.Catalog().Dependencies {
			if d.Peer != "" {
				for _, c := range d.Calls {
					declared[a.Name][c.Quint] = route{d.Peer, c.Name}
				}
			}
		}
	}
	for _, s := range steps {
		app, tenant, clients, ok := rec.ParseActor(s.Actor)
		r := declared[app][s.Action]
		if !ok || clients || r.peer == "" {
			continue
		}
		key, _ := s.Args["key"].(string)
		trigger, err := protocolInt(s.Args["trigger"])
		if key == "" || err != nil || trigger < 1 {
			return nil, fmt.Errorf("gristtest.ProtocolSteps: %s's %s lacks its key/trigger: keep native call records", app, s.Action)
		}
		k, cause := callKey{r.peer, tenant, r.command, key}, (event{app, tenant, trigger})
		if prev, exists := causes[k]; exists && prev != cause {
			return nil, fmt.Errorf("gristtest.ProtocolSteps: key %s of %s/%s has conflicting triggers", key, r.peer, r.command)
		}
		if parents[cause] == nil {
			return nil, fmt.Errorf("gristtest.ProtocolSteps: %s's %s refers to missing trigger %d in %s: collect its committed log", app, s.Action, trigger, tenant)
		}
		causes[k] = cause
	}
	for _, n := range nodes {
		command := commands[n.app][n.s.Action]
		id, _ := n.s.Args["id"].(string)
		if cause, ok := causes[callKey{n.app, n.tenant, command, id}]; ok {
			n.parent, n.trigger = parents[cause], cause.pos
			n.parent.next = append(n.parent.next, n)
			n.indegree++
		}
	}
	// Kahn's algorithm, with stable identities as its only tie-breaker.
	// A cycle is inconsistent evidence, never broken to obtain a verdict.
	slices.SortFunc(nodes, func(a, b *node) int {
		if c := strings.Compare(a.tenant, b.tenant); c != 0 {
			return c
		}
		if c := strings.Compare(a.app, b.app); c != 0 {
			return c
		}
		return cmpUint(a.s.Seq, b.s.Seq)
	})
	var out []qtrace.Step
	seq := map[string]uint64{}
	var settleTime time.Time
	indices := map[*node]int{}
	ready := &protocolReady{}
	for i, n := range nodes {
		indices[n] = i
		if n.indegree == 0 {
			heap.Push(ready, i)
		}
	}
	for remaining := len(nodes); remaining > 0; remaining-- {
		if ready.Len() == 0 {
			return nil, fmt.Errorf("gristtest.ProtocolSteps: causal cycle between commits: check peer keys and trigger positions")
		}
		n := nodes[heap.Pop(ready).(int)]
		for _, next := range n.next {
			next.indegree--
			if next.indegree == 0 {
				heap.Push(ready, indices[next])
			}
		}
		if commands[n.app][n.s.Action] == "" {
			continue // processor commits remain causal barriers, not protocol actions
		}
		seq[n.tenant]++
		s := n.s
		s.Action = prefix[n.app] + strings.ToUpper(s.Action[:1]) + s.Action[1:]
		s.Actor, s.Recorder = "protocol/"+n.tenant, "grist/protocol/"+n.tenant
		s.Process, s.Thread, s.Seq = "protocol", "commits", seq[n.tenant]
		s.Call, s.Invoked, s.Order = 0, time.Time{}, nil
		s.Args, s.Obs = maps.Clone(s.Args), maps.Clone(s.Obs)
		s.Obs["protocol.service"], s.Obs["protocol.position"] = n.app, n.head
		s.Obs["protocol.recorder"], s.Obs["protocol.seq"] = n.s.Recorder, strconv.FormatUint(n.s.Seq, 10)
		s.Obs["protocol.parentApp"], s.Obs["protocol.parentID"] = "", ""
		s.Obs["protocol.parentPosition"], s.Obs["protocol.trigger"] = int64(0), n.trigger
		if n.parent != nil {
			s.Obs["protocol.parentPosition"] = n.parent.head
			s.Obs["protocol.parentApp"] = n.parent.app
			id, _ := n.parent.s.Args["id"].(string)
			s.Obs["protocol.parentID"] = id
		}
		s.Source = fmt.Sprintf("%s seq %d", n.s.Recorder, n.s.Seq)
		if n.s.Source != "" {
			s.Source += " (" + n.s.Source + ")"
		}
		if s.Time.After(settleTime) {
			settleTime = s.Time
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("gristtest.ProtocolSteps: no committed commands of the supplied Apps: record both ends of the protocol")
	}
	for _, tenant := range slices.Sorted(maps.Keys(seq)) {
		if settled[tenant] {
			out = append(out, qtrace.Step{Action: "settled", Actor: "protocol/" + tenant, Recorder: "grist/protocol/" + tenant,
				Seq: seq[tenant] + 1, Time: settleTime, Record: qtrace.Record{Settled: true}})
		}
	}
	return out, nil
}

func protocolInt(v any) (int64, error) { return strconv.ParseInt(fmt.Sprint(v), 10, 64) }

func cmpUint(a, b uint64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// protocolReady orders the ready nodes by their index in stable identity order.
type protocolReady []int

func (p protocolReady) Len() int           { return len(p) }
func (p protocolReady) Less(i, j int) bool { return p[i] < p[j] }
func (p protocolReady) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p *protocolReady) Push(v any)        { *p = append(*p, v.(int)) }
func (p *protocolReady) Pop() any          { a := *p; v := a[len(a)-1]; *p = a[:len(a)-1]; return v }
