package gristtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// LocalModel registers the App and model instance that judge its native actors.
// The caller owns the Checker and keeps it open while the composition runs.
type LocalModel struct {
	App     *grist.App
	Checker *validate.Checker
}

// ProtocolModel registers a system model and its causal-order checking limit.
// Its checker judges projected orders as supplied, without linearization.
type ProtocolModel struct {
	Checker *validate.Checker
	Limit   int
}

// Composition judges each registered service, its declared peer edges, and an
// optional system protocol against one collected native history. It does not
// infer a model's independence from how that model was authored.
type Composition struct {
	Models   []LocalModel
	Protocol *ProtocolModel
}

// System registers this composition for every run of sys, including runs made
// while navigating. Normal conformance assertions include its applicable checks.
func (c Composition) System(sys qtest.System) qtest.System {
	if sys.Assess != nil {
		panic("gristtest.Composition.System: system already has an assessment; combine its intended model registrations in one Composition instead of replacing existing checks")
	}
	c.Models = slices.Clone(c.Models)
	if c.Protocol != nil {
		p := *c.Protocol
		c.Protocol = &p
	}
	sys.Assess = c.Assess
	return sys
}

// Assess checks the same native history in every registered model and peer
// relation. Unexercised services and edges remain explicitly not checked. A
// protocol omitted from the registration remains not checked too, rather than
// acquiring a guarantee from local conformance. Errors retain the partial receipt
// and are never returned as model counterexamples.
func (c Composition) Assess(ctx context.Context, steps []qtrace.Step) (out receipt.Composition, err error) {
	out.Local = []receipt.Component{}
	out.Peers = []receipt.Component{}
	defer func() {
		if err != nil {
			out.Error = err.Error()
		}
	}()
	raw, err := json.Marshal(steps)
	if err != nil {
		return out, fmt.Errorf("gristtest.Composition: encode native history: %w", err)
	}
	sum := sha256.Sum256(raw)
	out.History = "sha256:" + hex.EncodeToString(sum[:])
	out.Protocol = receipt.Protocol{Component: receipt.Component{Status: receipt.NotChecked, Reason: "no system protocol registered"}, Quantifier: "all_causal_orders"}
	if c.Protocol != nil {
		out.Protocol.Configured = true
		out.Protocol.Limit = c.Protocol.Limit
		out.Protocol.Binding = compositionBinding(c.Protocol.Checker)
		if c.Protocol.Checker != nil && c.Protocol.Checker.Binding != nil {
			out.Protocol.Target = c.Protocol.Checker.Binding.Module
		}
		out.Protocol.Reason = "no projected commands were checked"
	}
	models := slices.Clone(c.Models)
	slices.SortStableFunc(models, func(a, b LocalModel) int {
		if a.App == nil {
			return -1
		}
		if b.App == nil {
			return 1
		}
		return strings.Compare(a.App.Name, b.App.Name)
	})
	byName := map[string]*grist.App{}
	for _, m := range models {
		row := receipt.Component{Status: receipt.NotChecked, Reason: "no native actor selected"}
		if m.App != nil {
			row.Target = m.App.Name
			row.Applicable = compositionAppActive(m.App.Name, steps)
		}
		row.Binding = compositionBinding(m.Checker)
		out.Local = append(out.Local, row)
		switch {
		case m.App == nil:
			return out, fmt.Errorf("gristtest.Composition: register a non-nil App for every local model")
		case byName[m.App.Name] != nil:
			return out, fmt.Errorf("gristtest.Composition: App %s is registered more than once", m.App.Name)
		case m.Checker == nil || m.Checker.Binding == nil:
			return out, fmt.Errorf("gristtest.Composition: App %s needs a local model checker", m.App.Name)
		}
		byName[m.App.Name] = m.App
	}
	if len(models) == 0 {
		return out, fmt.Errorf("gristtest.Composition: register at least one App and its local model checker")
	}
	var apps []*grist.App
	type edge struct{ from, to *grist.App }
	var edges []edge
	for _, m := range models {
		apps = append(apps, m.App)
		for _, d := range m.App.Dependencies() {
			peer := d.Peer()
			if peer == nil {
				continue
			}
			row := receipt.Component{Target: m.App.Name + " -> " + peer.Name, Status: receipt.NotChecked, Reason: "no peer call or caller-owned commit recorded"}
			if byName[peer.Name] != peer {
				row.Status, row.Applicable = receipt.Error, true
				row.Reason = "the declared peer App has no matching local model registration"
				out.Peers = append(out.Peers, row)
				return out, fmt.Errorf("gristtest.Composition: %s calls peer %s: register that App and its local model", m.App.Name, peer.Name)
			}
			edges = append(edges, edge{m.App, peer})
			out.Peers = append(out.Peers, row)
		}
	}
	if c.Protocol != nil && (c.Protocol.Checker == nil || c.Protocol.Checker.Binding == nil || c.Protocol.Limit < 1) {
		out.Protocol.Status = receipt.Error
		out.Protocol.Applicable = true
		out.Protocol.Reason = "a system protocol needs a checker and a positive causal-order limit"
		return out, fmt.Errorf("gristtest.Composition: %s", out.Protocol.Reason)
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	var errs []error
	for i, m := range models {
		row := &out.Local[i]
		report, e := m.Checker.Check(ctx, steps)
		if e != nil {
			row.Status, row.Applicable, row.Reason = receipt.Error, true, e.Error()
			errs = append(errs, e)
			continue
		}
		judged := report.Receipt()
		history := judged.History()
		row.Judgment = &history
		if len(report.Actors) == 0 {
			if row.Applicable {
				e = fmt.Errorf("gristtest.Composition: local model for %s selected no actor from its recorded work: select that App's native actors in the binding", m.App.Name)
				row.Status, row.Reason = receipt.Error, e.Error()
				errs = append(errs, e)
			}
			continue
		}
		row.Applicable = true
		row.Status, row.Reason = compositionJudgmentStatus(judged)
		for _, a := range report.Actors {
			app, _, clients, ok := rec.ParseActor(a.Actor)
			if !ok || clients || app != m.App.Name {
				e = fmt.Errorf("gristtest.Composition: local model registered for %s judged actor %q: select that App's native actors in its binding", m.App.Name, a.Actor)
				row.Status, row.Reason = receipt.Error, e.Error()
				errs = append(errs, e)
				break
			}
		}
	}
	traces, _ := qtrace.Reconstruct(steps)
	for i, e := range edges {
		row := &out.Peers[i]
		if !compositionEdgeActive(e.from, e.to, steps) {
			continue
		}
		row.Applicable = true
		incomplete := ""
		for _, tr := range traces {
			app, _, clients, ok := rec.ParseActor(tr.Actor)
			if !ok || clients || app != e.from.Name && app != e.to.Name {
				continue
			}
			if len(tr.Lost) > 0 || len(tr.Gaps) > 0 {
				incomplete = "native peer-link records are incomplete: collect the whole history before judging missing calls or commits"
				break
			}
		}
		if incomplete != "" {
			row.Status, row.Reason = receipt.Inconclusive, incomplete
			continue
		}
		problems := Links(e.from, e.to, steps)
		row.Status, row.Reason = receipt.Pass, ""
		if len(problems) > 0 {
			row.Status, row.Reason = receipt.Fail, strings.Join(problems, "; ")
		}
	}
	if c.Protocol != nil && compositionHasCommands(apps, steps) {
		p := &out.Protocol
		var halted *compositionUndecided
		search, e := CheckProtocolOrders(ctx, steps, c.Protocol.Limit, func(ctx context.Context, order []qtrace.Step) (bool, error) {
			report, e := c.Protocol.Checker.Check(ctx, order)
			if e != nil {
				return false, e
			}
			judged := report.Receipt()
			history := judged.History()
			p.Judgment = &history
			status, reason := compositionJudgmentStatus(judged)
			switch status {
			case receipt.Pass:
				return true, nil
			case receipt.Fail:
				return false, nil
			default:
				halted = &compositionUndecided{status: status, reason: reason}
				return false, halted
			}
		}, apps...)
		p.Checked, p.Exhaustive = search.Checked, search.Exhaustive
		p.Applicable = search.Checked > 0 || p.Judgment != nil || e != nil
		switch {
		case halted != nil:
			p.Status, p.Reason = halted.status, halted.reason
		case e != nil:
			p.Status, p.Reason = receipt.Error, e.Error()
			errs = append(errs, e)
		case len(search.Counterexample) > 0:
			p.Status, p.Reason = receipt.Fail, "a causal ordering allowed by the collected history fails the system model"
			for _, s := range search.Counterexample {
				if !s.Settled {
					p.Counterexample = append(p.Counterexample, receipt.Record{ID: validate.RecordID(&s), Actor: s.Actor, Action: s.Action})
				}
			}
		case search.Checked == 0:
			p.Status = receipt.NotChecked
			p.Exhaustive = false
		case !search.Exhaustive:
			p.Status, p.Reason = receipt.Inconclusive, "causal-order limit exhausted before every ordering was checked"
		default:
			p.Status, p.Reason = receipt.Pass, "every causal ordering of the collected history passed"
		}
	}
	return out, errors.Join(errs...)
}

// compositionUndecided stops the boolean ordering enumerator without inventing
// a counterexample for an evaluator failure or a history with no verdict.
type compositionUndecided struct {
	status receipt.Status
	reason string
}

func (e *compositionUndecided) Error() string { return e.reason }

func compositionBinding(c *validate.Checker) string {
	if c == nil || c.Binding == nil {
		return ""
	}
	if c.Binding.Path != "" {
		return c.Binding.Path
	}
	return c.Binding.SpecPath() + ":" + c.Binding.Module
}

func compositionEdgeActive(a, b *grist.App, steps []qtrace.Step) bool {
	calls := map[string]bool{}
	for _, d := range a.Catalog().Dependencies {
		if d.Peer == b.Name {
			for _, c := range d.Calls {
				calls[c.Quint] = true
			}
		}
	}
	commands := map[string]bool{}
	for _, c := range b.Catalog().Commands {
		commands[c.Quint] = true
	}
	for _, s := range steps {
		app, _, clients, ok := rec.ParseActor(s.Actor)
		if !ok || clients || s.Settled {
			continue
		}
		if app == a.Name && calls[s.Action] {
			return true
		}
		id, _ := s.Args["id"].(string)
		if app == b.Name && !s.Invocation && commands[s.Action] && strings.HasPrefix(id, a.Name+"/") {
			return true
		}
	}
	return false
}

func compositionAppActive(name string, steps []qtrace.Step) bool {
	for _, s := range steps {
		app, _, clients, ok := rec.ParseActor(s.Actor)
		if ok && app == name && !clients && !s.Invocation && !s.Settled && s.Action != "" {
			return true
		}
	}
	return false
}

func compositionHasCommands(apps []*grist.App, steps []qtrace.Step) bool {
	commands := map[string]map[string]bool{}
	for _, a := range apps {
		commands[a.Name] = map[string]bool{}
		for _, c := range a.Catalog().Commands {
			commands[a.Name][c.Quint] = true
		}
	}
	for _, s := range steps {
		app, _, clients, ok := rec.ParseActor(s.Actor)
		if ok && !clients && !s.Invocation && !s.Settled && commands[app][s.Action] {
			return true
		}
	}
	return false
}

// Composition registration promises that every configured named obligation was
// actually judged. A plain history pass alone can leave completion unjudged.
func compositionJudgmentStatus(j receipt.Summary) (receipt.Status, string) {
	if j.Status != receipt.Pass {
		return j.Status, j.Reason
	}
	for _, a := range j.Actors {
		for _, c := range a.Checks {
			if c.Status != receipt.Pass {
				reason := fmt.Sprintf("configured %s %s was not conclusively judged for actor %s", c.Kind, c.Name, a.Actor)
				if c.Reason != "" {
					reason += ": " + c.Reason
				}
				return receipt.Inconclusive, reason
			}
		}
	}
	return receipt.Pass, j.Reason
}
