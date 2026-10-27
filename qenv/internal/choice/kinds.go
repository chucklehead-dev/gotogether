package choice

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qrun"
)

// The fault kinds of a process: each one's options' names (Declare) and
// the sites that declared it (DeclareSite), in one registry, so a choice,
// a coverage report and a script name an option the same way.
// Process-wide: a kind declared anywhere is the same kind everywhere.
var registry struct {
	sync.Mutex
	kinds   map[string]*kind
	nemeses map[string]bool // nemeses registered by name ("crash.payments")
}

type kind struct {
	options []string // nil: not declared by name
	named   bool
	sites   []string
	static  bool // declared outside any run (KnownStatic)
}

// runNames are the FaultNames of the calling goroutine's run, and whether
// it is in a run at all: a run without them (Hegel's, a load experiment's)
// checks no names, and its declarations are neither its nor static.
func runNames() (*hooks.FaultNames, bool) {
	ctx, ok := qrun.Current()
	if !ok {
		return nil, false
	}
	return hooks.FaultNamesOf(ctx), true
}

func kindOf(name string) *kind {
	name = strings.TrimPrefix(name, "fault.")
	if registry.kinds == nil {
		registry.kinds = map[string]*kind{}
	}
	k := registry.kinds[name]
	if k == nil {
		k = &kind{}
		registry.kinds[name] = k
	}
	return k
}

// Declare names a fault kind's options in order (option 0, nominal,
// unnamed), in the calling goroutine's run's names (DeclareIn).
func Declare(name string, options ...string) {
	f, inRun := runNames()
	DeclareIn(f, inRun, name, options...)
}

// DeclareIn names a fault kind's options in order (option 0, nominal,
// unnamed), for the run whose names are f (nil: none), or outside any
// run when inRun is false, which makes the kind static (KnownStatic).
// Declaring it again with other options panics: option numbers would
// change meaning.
func DeclareIn(f *hooks.FaultNames, inRun bool, name string, options ...string) {
	registry.Lock()
	defer registry.Unlock()
	k := kindOf(name)
	if k.named && !slices.Equal(k.options, options) {
		panic(fmt.Sprintf("qenv: fault kind %s declared with options %v, then %v", strings.TrimPrefix(name, "fault."), k.options, options))
	}
	k.options, k.named = append([]string(nil), options...), true
	name = strings.TrimPrefix(name, "fault.")
	switch {
	case f != nil:
		f.Add(name)
		for _, o := range options {
			f.Add(name + "." + o)
		}
	case !inRun:
		k.static = true
	}
}

// seen are the fault kinds chosen (Seen), declared or not: a double that
// chooses "fault.mykind" without DeclareFaults has a kind all the same.
// Process-wide.
var seen sync.Map

// Seen notes a fault kind a choice was made of ("fault." prefix or not).
// A run's names get its chosen kinds from its choice log (qtest's runOne),
// which also sees choices a simulation under gosim makes.
func Seen(kind string) {
	k := strings.TrimPrefix(kind, "fault.")
	if _, ok := seen.Load(k); !ok {
		seen.Store(k, true)
	}
}

// DeclareNemesis registers a nemesis's name, as a declaration names a
// kind's options: Known then knows it, and f, its run's names (nil: none).
func DeclareNemesis(f *hooks.FaultNames, name string) {
	registry.Lock()
	defer registry.Unlock()
	if registry.nemeses == nil {
		registry.nemeses = map[string]bool{}
	}
	registry.nemeses[name] = true
	if f != nil {
		f.Add("nemesis." + name)
	}
}

// KnownStatic reports whether name covers a fault kind, option or
// nemesis declared outside any run, by Known's rule: a woven fault
// point's (its site is declared at init) or a double's built before any
// simulation. With a run's FaultNames it is what that run knows.
func KnownStatic(name string) bool {
	registry.Lock()
	defer registry.Unlock()
	covers := func(x string) bool { return x == name || strings.HasPrefix(x, name+".") }
	for k, v := range registry.kinds {
		if !v.static && len(v.sites) == 0 {
			continue
		}
		if covers(k) {
			return true
		}
		for _, o := range v.options {
			if covers(k + "." + o) {
				return true
			}
		}
	}
	return false // a nemesis is a simulation's: none is static
}

// Options are a declared kind's option names (with or without the
// "fault." prefix), or nil.
func Options(name string) []string {
	registry.Lock()
	defer registry.Unlock()
	if k := registry.kinds[strings.TrimPrefix(name, "fault.")]; k != nil && k.named {
		return append([]string(nil), k.options...)
	}
	return nil
}

// OptionName is the name of a kind's option v (1-based), or "".
func OptionName(name string, v int) string {
	if o := Options(name); v >= 1 && v <= len(o) {
		return o[v-1]
	}
	return ""
}

// DeclareSite registers a site (a function, a position) of a fault kind:
// what woven code does at init, so a coverage report can name the points
// no run reached.
func DeclareSite(name, site string) {
	registry.Lock()
	defer registry.Unlock()
	k := kindOf(name)
	if !slices.Contains(k.sites, site) {
		k.sites = append(k.sites, site)
	}
}

// Sites are the declared sites: kind -> sites.
func Sites() map[string][]string {
	registry.Lock()
	defer registry.Unlock()
	out := map[string][]string{}
	for n, k := range registry.kinds {
		if len(k.sites) > 0 {
			out[n] = append([]string(nil), k.sites...)
		}
	}
	return out
}

// Exact reports whether name (as reports print it) is a fault kind this
// process declared or chose, or a nemesis by its full name
// ("nemesis.crash.payments"): what a Script matches, where Known also
// takes prefixes.
func Exact(name string) bool {
	registry.Lock()
	defer registry.Unlock()
	if n, ok := strings.CutPrefix(name, "nemesis."); ok {
		// by its full name only: a family ("nemesis.crash") is a choice's
		// kind, seen, but no nemesis a Script can name
		return registry.nemeses[n]
	}
	if _, ok := registry.kinds[name]; ok {
		return true
	}
	_, ok := seen.Load(name)
	return ok
}

// Names are the fault kinds declared or chosen and the nemeses
// ("nemesis.<name>"), sorted: what a misspelling is nearest to.
func Names() []string {
	registry.Lock()
	defer registry.Unlock()
	set := map[string]bool{}
	for k := range registry.kinds {
		set[k] = true
	}
	for n := range registry.nemeses {
		set["nemesis."+n] = true
	}
	seen.Range(func(k, _ any) bool {
		set[k.(string)] = true
		return true
	})
	return slices.Sorted(maps.Keys(set))
}

// Nearest is the name of names nearest to name by edit distance, if one
// is near enough to be a misspelling of it, else "".
func Nearest(name string, names []string) string {
	best, bestD := "", -1
	for _, n := range names {
		if d := editDistance(name, n); bestD < 0 || d < bestD {
			best, bestD = n, d
		}
	}
	if bestD >= 0 && bestD <= max(2, len(name)/4) {
		return best
	}
	return ""
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
