package qtest

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// A Catalog is what a system declares it does, as the model actions its
// runs can take: one Take per block and outcome, each with how to reach
// it (Take.Hint), named as Take.Name names them ("borrow.limit"). grist
// builds an App's from its catalog (gristtest.Catalog); any system can
// list its own. Its claims are Exercised, every Take of it, and Take,
// one of them: the catalog's twins of the model-wide Exercised and
// Takes.
type Catalog struct {
	// Name is the system's, for messages.
	Name string
	// Takes are its blocks, by outcome.
	Takes []Take
}

// Exercised is the claim that the runs took every Take of the catalog
// but except, each in some run (each Take's own share and outcome),
// naming every one missed with its Hint (TakesEach). A run that
// exercised none of a system's own blocks passes every other claim:
// this one fails it. except names what the test does not expect, each as
// Take.Name names it ("borrow.limit"), or an action for all of its
// outcomes ("borrow"); a name that is none of them panics, naming them,
// as does an exception of every Take (or a catalog of none), which no
// runs could fail.
// It is the catalog's twin of the model-wide Exercised, which wants
// every action and fault option of the model: this one wants the
// catalog's blocks by outcome, and none of a model's helpers.
func (c Catalog) Exercised(except ...string) TakeEach {
	names := map[string]bool{}
	for _, k := range c.Takes {
		names[k.Name()] = true
		names[k.action] = true
	}
	skip := map[string]bool{}
	for _, x := range except {
		if !names[x] {
			panic(fmt.Sprintf("qtest: Catalog(%s).Exercised(%q): %q names no block of the catalog (its names: %s)", c.Name, x, x, strings.Join(slices.Sorted(maps.Keys(names)), ", ")))
		}
		skip[x] = true
	}
	var want []Take
	for _, k := range c.Takes {
		if !skip[k.Name()] && !skip[k.action] {
			want = append(want, k)
		}
	}
	if len(want) == 0 {
		// it would hold of any runs, none at all included
		panic(fmt.Sprintf("qtest: Catalog(%s).Exercised(%s): no Take left to want (the catalog's: %s): except fewer, or list the system's blocks in Takes", c.Name, strings.Join(except, ", "), strings.Join(c.names(), ", ")))
	}
	return TakesEach(want...)
}

// Take is the catalog's Take of name, as Take.Name names it
// ("borrow.limit"), with its Hint: for a test that wants one block in a
// share of the runs (Take.InAtLeast), or with arguments (Take.With). A
// name the catalog lacks, or has more than one Take of, panics, naming
// its Takes.
func (c Catalog) Take(name string) Take {
	var found []Take
	for _, k := range c.Takes {
		if k.Name() == name {
			found = append(found, k)
		}
	}
	switch len(found) {
	case 0:
		panic(fmt.Sprintf("qtest: Catalog(%s).Take(%q): no such block (its takes: %s)", c.Name, name, strings.Join(c.names(), ", ")))
	case 1:
		return found[0]
	}
	panic(fmt.Sprintf("qtest: Catalog(%s).Take(%q): %d Takes of that name (Take.With's arguments are not in it): build the one you want with qtest.Takes", c.Name, name, len(found)))
}

// names are its Takes' names, in order.
func (c Catalog) names() []string {
	out := make([]string, len(c.Takes))
	for i, k := range c.Takes {
		out[i] = k.Name()
	}
	return out
}
