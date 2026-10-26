package qnemesis

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// A Fault is one fault of a Schedule, named as qenv's Script names a
// nemesis: "nemesis.<family>.<name>" for a target's ("nemesis.crash.n1"),
// "nemesis.<family>.<a>.<b>" for a link's. The families:
//   - crash.<p>: SIGKILL (docker kill), reaped, then the next incarnation
//     started, at once or after For;
//   - pause.<p>, resume.<p>: SIGSTOP and SIGCONT (docker pause, unpause);
//   - partition.<a>.<b>, heal.<a>.<b>: the links between a and b cut and
//     healed; heal.<p> heals all of p's;
//   - isolate.<p>: every link of p's cut (Network.Pairs);
//   - latency.<a>.<b>: each response between them delayed by Arg, give
//     or take half of it;
//   - loss.<a>.<b>: the responses of a share (Toxicity, 0 for all) of
//     the connections between them dropped, each closed after Arg
//     (default 500ms).
//
// A fault is due once At has passed since Run began, the watched step
// logs have After lines (Nemesis.Steps), the schedule's earlier faults of
// the same Kind have happened, and so have the ones it Follows (indices
// in the schedule), each ended if it lasts For: Script's partial order. A fault with For lasts that
// long and is undone (a crash's restart, a heal, a resume, the toxics
// cleared); without it a crash restarts at once and the others last until
// a fault undoes them (heal, resume) or Stop.
//
// Clock skew is no family: Go reads the time through the vDSO, which
// libfaketime's LD_PRELOAD does not reach. Neither are failpoints.
type Fault struct {
	Kind    string
	At      time.Duration
	After   int
	Follows []int
	For     time.Duration
	Arg     time.Duration
	// Toxicity is a loss fault's share of connections (0 means 1).
	Toxicity float64
}

func (f Fault) String() string {
	var b strings.Builder
	b.WriteString(f.Kind)
	if f.At > 0 {
		fmt.Fprintf(&b, " at %v", f.At)
	}
	if f.After > 0 {
		fmt.Fprintf(&b, " after %d steps", f.After)
	}
	if f.For > 0 {
		fmt.Fprintf(&b, " for %v", f.For)
	}
	return b.String()
}

// A Schedule is the faults a Nemesis makes, in their partial order.
type Schedule []Fault

// DefaultLossTimeout is how long a loss fault's connection is held,
// its responses dropped, before it is closed (Fault.Arg, if not set).
const DefaultLossTimeout = 500 * time.Millisecond

// families are the fault families, and whether each names a "pair", a
// "target", or "either" (heal). Read-only.
var families = map[string]string{
	"crash": "target", "pause": "target", "resume": "target", "isolate": "target",
	"partition": "pair", "heal": "either", "latency": "pair", "loss": "pair",
}

// parse splits a kind into its family and names; ok false if it is no
// nemesis kind.
func parse(kind string) (family string, names []string, ok bool) {
	rest, ok := strings.CutPrefix(kind, "nemesis.")
	if !ok {
		return "", nil, false
	}
	parts := strings.Split(rest, ".")
	return parts[0], parts[1:], true
}

// Kinds names a family's fault on each of names: Kinds("crash", "n1",
// "n2") is nemesis.crash.n1 and nemesis.crash.n2; for a pair's family,
// one per pair of names (sorted: partition.n1.n2, not partition.n2.n1),
// as qenv's PartitionNemesis names them.
func Kinds(family string, names ...string) []string {
	var out []string
	if families[family] != "pair" {
		for _, n := range names {
			out = append(out, "nemesis."+family+"."+n)
		}
		return out
	}
	for i, a := range names {
		for _, b := range names[i+1:] {
			p := sorted(a, b)
			out = append(out, "nemesis."+family+"."+p[0]+"."+p[1])
		}
	}
	return out
}

// A Mix is what Random chooses among: each of Kinds as likely (repeat a
// kind to weigh it), Faults of them, latency faults of Latency, loss
// faults of Toxicity.
type Mix struct {
	Kinds    []string
	Faults   int
	Latency  time.Duration
	Toxicity float64
}

// Random is a Jepsen-style schedule: Faults faults, one per interval
// (the i-th at i·interval), each a kind of mix drawn by seed, undone
// after dur, before the next begins (dur < interval). Its faults are in
// a total order, each Follows the one before, which waits for its end:
// a fault that began late cannot overlap the next. A seed draws the same schedule every time: the journal of
// a failed run has its faults, and the seed reproduces their order.
func Random(seed int64, mix Mix, interval, dur time.Duration) Schedule {
	if len(mix.Kinds) == 0 || mix.Faults <= 0 {
		panic("qnemesis: Random needs a Mix with Kinds to choose among and Faults > 0 (qnemesis.Kinds(\"crash\", names...) names a family's)")
	}
	if dur <= 0 || dur >= interval {
		panic(fmt.Sprintf("qnemesis: Random(..., interval %v, for %v): each fault is undone before the next, so 0 < for < interval", interval, dur))
	}
	rng := rand.New(rand.NewPCG(uint64(seed), 0x6e656d65736973)) // "nemesis"
	s := make(Schedule, mix.Faults)
	for i := range s {
		k := mix.Kinds[rng.IntN(len(mix.Kinds))]
		f := Fault{Kind: k, At: time.Duration(i) * interval, For: dur}
		if i > 0 {
			f.Follows = []int{i - 1}
		}
		switch fam, _, _ := parse(k); fam {
		case "latency":
			f.Arg = mix.Latency
		case "loss":
			f.Toxicity = mix.Toxicity
		}
		s[i] = f
	}
	return s
}
