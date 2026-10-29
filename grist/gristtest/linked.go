package gristtest

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Linked checks a run of app a calling its peer b (grist.NewPeer) across
// the two services, by key (docs/grist-instrumentation.md §3.7): every
// call a saw answered, ok or refused, has b's step of its key (the
// command id), of the called command in the same tenant and with the
// same outcome; every step of b whose id names a (<a>/...) has a's call
// of that command and key, pending included;
// and no key has two steps at b. Each model judges its own side (a's
// binding the calls, b's the commits); neither sees the other's records,
// which is what Linked relates: a call a saw answered that b never
// committed, a commit under an id a never sent (a client minting an id
// per try: PeerMutants' freshKey), an answer a was told that b's commit
// contradicts. steps are the run's as recorded (qtest.Run.Recorded:
// invocation records included), both Apps' in one run (gristsim's
// Config.Peers), or both services' step files read together. It fails t
// once per problem (Links).
func Linked(t testing.TB, a, b *grist.App, steps []qtrace.Step) {
	t.Helper()
	for _, p := range Links(a, b, steps) {
		t.Errorf("%s", p)
	}
}

// Links are Linked's problems with a run of a calling b, sorted: none
// when the two services agree. That a calls b at all is the caller's
// declaration (grist.NewPeer of b), and that it called is the run's: a
// run with no call of a's to b is a problem too, so a claim of none holds
// of something.
func Links(a, b *grist.App, steps []qtrace.Step) []string {
	// a's peer calls of b, by their caller step's action, and b's commands
	// they are, by their step's action
	callOf := map[string]string{} // a's action -> b's command name
	for _, d := range a.Catalog().Dependencies {
		if d.Peer != b.Name {
			continue
		}
		for _, c := range d.Calls {
			callOf[c.Quint] = c.Name
		}
	}
	if len(callOf) == 0 {
		return []string{fmt.Sprintf("gristtest.Links: %s declares no peer call of %s (grist.NewPeer, grist.NewPeerCall): give the caller first, then its peer", a.Name, b.Name)}
	}
	commandOf := map[string]string{} // b's action -> its command name
	for _, c := range b.Catalog().Commands {
		// An id naming a on any command must be accounted for, including
		// a command a did not declare: a transport may route to the wrong one.
		commandOf[c.Quint] = c.Name
	}
	type key struct{ tenant, id, command string }
	called := map[key]bool{}       // a's calls, pending included
	answered := map[key][]string{} // a's answered calls' outcomes, unknown aside
	var keys []key
	atPeer := map[key][]string{} // b's outcomes, by tenant, id and command
	reused := grist.ErrKeyReused.(*grist.Rejected).Name
	for _, s := range steps {
		app, tenant, clients, ok := rec.ParseActor(s.Actor)
		if !ok || clients || s.Settled {
			continue
		}
		switch {
		case app == a.Name && callOf[s.Action] != "":
			id, _ := s.Args["key"].(string)
			k := key{tenant, id, callOf[s.Action]}
			if !called[k] {
				keys = append(keys, k)
			}
			called[k] = true
			if s.Invocation || s.Outcome == "unknown" {
				continue // pending, or answered nothing it can be held to
			}
			answered[k] = append(answered[k], outcome(s))
		case app == b.Name && commandOf[s.Action] != "" && !s.Invocation:
			id, _ := s.Args["id"].(string)
			if !strings.HasPrefix(id, a.Name+"/") {
				continue // another caller's, or a client's own
			}
			k := key{tenant, id, commandOf[s.Action]}
			atPeer[k] = append(atPeer[k], outcome(s))
		}
	}
	var out []string
	if len(keys) == 0 {
		out = append(out, fmt.Sprintf("gristtest.Links: no call of %s's to %s in %d steps: record a run in which %s's effects call it (both Apps' steps: gristsim's Config.Peers)", a.Name, b.Name, len(steps), a.Name))
	}
	for _, k := range keys {
		for _, o := range answered[k] {
			found := false
			for _, p := range atPeer[k] {
				found = found || p == o
			}
			if !found {
				out = append(out, fmt.Sprintf("%s's call %s of %s in tenant %s was answered %s, and %s recorded %v for that command under that id: an answer the peer's commit does not give (a commit missing, another command, or another outcome)", a.Name, k.id, k.command, k.tenant, o, b.Name, atPeer[k]))
			}
		}
	}
	type identity struct{ tenant, id string }
	counts := map[identity]int{}
	for k, os := range atPeer {
		if !called[k] {
			out = append(out, fmt.Sprintf("%s committed %s (%s) in tenant %s, an id naming %s, which %s never called with for command %s: a command under an id that is no call's key (a client minting a request id per try, or routing to another command?)", b.Name, k.id, strings.Join(os, ", "), k.tenant, a.Name, a.Name, k.command))
		}
		for _, o := range os {
			if o == reused {
				out = append(out, fmt.Sprintf("%s refused %s in tenant %s as %s: %s sent the key with other arguments (a call's request is its rule's for the trigger, the same on every try)", b.Name, k.id, k.tenant, reused, a.Name))
				continue
			}
			counts[identity{k.tenant, k.id}]++
		}
	}
	for k, n := range counts {
		if n > 1 {
			out = append(out, fmt.Sprintf("%s has %d steps of %s in tenant %s: a key names one outcome, across all commands", b.Name, n, k.id, k.tenant))
		}
	}
	sort.Strings(out)
	return out
}

// outcome is a step's outcome, ok for a commit's.
func outcome(s qtrace.Step) string {
	if s.Outcome == "" {
		return "ok"
	}
	return s.Outcome
}
