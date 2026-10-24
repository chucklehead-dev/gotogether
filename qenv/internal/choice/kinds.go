package choice

import (
	"fmt"
	"slices"
	"strings"
	"sync"
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
// unnamed). Declaring it again with other options panics: option numbers
// would change meaning.
func Declare(name string, options ...string) {
	registry.Lock()
	defer registry.Unlock()
	k := kindOf(name)
	if k.named && !slices.Equal(k.options, options) {
		panic(fmt.Sprintf("qenv: fault kind %s declared with options %v, then %v", strings.TrimPrefix(name, "fault."), k.options, options))
	}
	k.options, k.named = append([]string(nil), options...), true
}

// seen are the fault kinds chosen (Seen), declared or not: a double that
// chooses "fault.mykind" without DeclareFaults has a kind all the same.
// Process-wide.
var seen sync.Map

// Seen notes a fault kind a choice was made of ("fault." prefix or not).
func Seen(kind string) {
	k := strings.TrimPrefix(kind, "fault.")
	if _, ok := seen.Load(k); !ok {
		seen.Store(k, true)
	}
}

// DeclareNemesis registers a nemesis's name, as a declaration names a
// kind's options: Known then knows it.
func DeclareNemesis(name string) {
	registry.Lock()
	defer registry.Unlock()
	if registry.nemeses == nil {
		registry.nemeses = map[string]bool{}
	}
	registry.nemeses[name] = true
}

// Known reports whether name (as reports print it) covers anything this
// process declared or chose: a fault kind, one of its options
// (kind.option), a nemesis (nemesis.<name>), or a prefix of any of them
// at a dot ("queue" of "queue.payments.ack"). A name that covers nothing matches
// nothing wherever it is given (a misspelling, a renamed kind).
func Known(name string) bool {
	registry.Lock()
	defer registry.Unlock()
	covers := func(x string) bool { return x == name || strings.HasPrefix(x, name+".") }
	for k, v := range registry.kinds {
		if covers(k) {
			return true
		}
		for _, o := range v.options {
			if covers(k + "." + o) {
				return true
			}
		}
	}
	for n := range registry.nemeses {
		if covers("nemesis." + n) {
			return true
		}
	}
	found := false
	seen.Range(func(k, _ any) bool {
		found = covers(k.(string))
		return !found
	})
	return found
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
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
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
