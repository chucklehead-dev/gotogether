package qenv

import (
	"context"

	"github.com/chucklehead-dev/gotogether/qenv/internal/choice"
	"github.com/chucklehead-dev/gotogether/qenv/internal/profile"
)

// The choosers, implemented in qenv/internal/choice, and the profile. go
// doc shows an alias without its fields and methods, so each one's doc
// lists them.
type (
	// A Chooser makes the environment's choices. Choose returns an index in
	// [0, n), n >= 2. Kinds starting with "fault." are fault choices, where 0 is
	// the nominal option; any other kind is a schedule choice, where every
	// option is normal and 0 is the canonical one (the oldest, the first).
	Chooser = choice.Chooser

	// A Choice is one decision, as recorded by a ChoiceLog: its Kind
	// ("fault.rpc.payments.response", "sched.task"), its N options, the
	// Value chosen, the Step the run was at, and the option's Name for a
	// declared fault kind. A chunk of entropy (EntropyOf) is a Choice of
	// Kind "entropy", N bytes long, in Bytes. String prints it.
	Choice = choice.Choice

	// A Wrapper is a chooser over another (a Recorder, a swarm, a Script over
	// its Base, a user's own): the package sees through it with Unwrap, so a
	// wrapper keeps what the chooser it wraps carries (the run's context, its
	// entropy, nemeses by name).
	Wrapper = choice.Wrapper

	// Entropy is how a chooser hands a simulation that is not qenv's (gosim's,
	// through qgosim) bytes from which to make its own choices other than
	// faults: one call per chunk rather than one per choice. A byte (two above
	// 256 options) modulo the number of options is the choice, so a zero byte
	// is option 0, as qhegel's Hegel chooser makes choices.
	Entropy = choice.Entropy

	// Nominal makes every choice 0: no faults, canonical schedule.
	Nominal = choice.Nominal

	// Rates gives the probability that a fault choice is not nominal, by
	// kind, named as reports print it, without the "fault." prefix
	// ("nemesis", "rpc.payments"). A kind matches its own entry, else the
	// longest entry that covers it ("rpc" covers "rpc.payments.response",
	// not "rpcx"), else Default. A zero Rates in an ExploreConfig (and
	// wherever a default applies) is DefaultRates; NoFaults is none at all.
	//
	// Its fields are Default (the rate of a kind no key covers) and Kinds
	// (rates by kind); Set returns a copy with one kind's rate, and Of is a
	// kind's rate:
	//
	//	rates := qenv.DefaultRates.Set("rpc.payments.response", 0.3)
	//	quiet := qenv.Rates{Default: 0.05, Kinds: map[string]float64{"store": 0}}
	Rates = choice.Rates

	// Seeded makes choices from a seeded generator: schedule choices uniformly,
	// fault choices at their Rates (its one field; NewSeeded sets
	// DefaultRates). Choose and Entropy draw from the generator.
	Seeded = choice.Seeded

	// PCT schedules by Probabilistic Concurrency Testing (Burckhardt et
	// al., ASPLOS 2010): a random priority per task, the highest-priority
	// runnable task always runs, and at Depth-1 random steps in 1..Steps
	// the task that ran last drops below every other. A bug of depth d
	// (d orderings that must happen: a task to run entirely before
	// another's step is depth 1) is found in a run with probability at
	// least 1/(n·Steps^(d-1)), d at most Depth, where a uniformly random
	// schedule may need 2^-steps; on two conditions: Steps at least the
	// run's scheduling steps, and n counting every task the run ever
	// creates (Seen reports both; qtest's summary checks them). Its fields
	// are Base (every other choice: faults, a queue's order), Depth and
	// Steps; NewPCT makes one, ChooseTask is what the scheduler asks, and
	// its picks are recorded as schedule choices, so a ChoiceLog of its
	// run replays without it.
	PCT = choice.PCT

	// ChoiceLog records the choices of the Chooser it wraps (its field C),
	// to replay a run: Choices, every choice made, and Nemeses, the nemeses
	// it was offered by name. Faults are the Choices that faulted; Unwrap
	// is C.
	ChoiceLog = choice.ChoiceLog

	// Bytes makes choices from a stream of bytes, by one rule shared by every
	// byte-driven chooser (Hegel's, Go fuzzing's, a corpus entry):
	//   - a schedule choice of n options is a byte (two above 256 options),
	//     modulo n;
	//   - a fault choice is nominal when a byte is below 256 × (1 − its rate);
	//     otherwise its option is 1 plus a byte modulo n−1 (no byte for n=2).
	//
	// A zero byte is the nominal or canonical option, so a shrinker's zeros are
	// fewer faults and the canonical schedule; past the end of the stream
	// every choice is nominal.
	//
	// Its fields: Next returns the next n bytes of the stream, Rates the
	// rates faults are made at, Chunk the size of Next's reads, OnFault
	// (if set) is told each fault made. Err is a stream encoded by another
	// rule's version (EncodeChoices' header).
	Bytes = choice.Bytes

	// A Replayer makes recorded choices again (Replay). At the first choice
	// that is not the one recorded (another kind, another number of options:
	// the run is not deterministic, or the code changed since) it stops
	// replaying, makes every later choice nominal, and keeps the divergence
	// (Err), which an exploration reports as the run's error. Left is how
	// many recorded choices it has not made.
	Replayer = choice.Replayer

	// A TimeProfile is where a simulation's virtual time goes: each wait at a seam
	// (Sleep, Wait, a Signal, a call's response, a lock) is a sample with its
	// stack, weighted by the virtual time the task stayed there. A CPU profile
	// of a simulation shows the scheduler; this shows the system's latency:
	// which call a storm's requests wait on, and from where. Set Sim.TimeProfile
	// before Run; write it with WriteTo and read it with `go tool pprof`. It
	// is kept by qenv's own scheduler (qenv.New), not the native runtime's.
	TimeProfile = profile.Profile
)

// NewTimeProfile returns an empty profile, for Sim.Profile.
func NewTimeProfile() *TimeProfile { return profile.New() }

// Read-only (Rates.Set returns a copy).
var (
	// DefaultRates inject a fault at one fault choice in twenty, except a
	// nemesis (a crash, a partition), which is considered at every
	// scheduling step and so happens at one step in two hundred.
	DefaultRates = choice.DefaultRates
	// NoFaults makes every fault choice nominal: a run with the schedule's
	// choices only.
	NoFaults = choice.NoFaults
)

// BytesVersion is the version of the byte rule. EncodeChoices writes it in
// a header, so a corpus entry encoded by another rule is recognised
// (Bytes.Err), not replayed as another run. Bump it when the rule changes.
const BytesVersion = choice.BytesVersion

// WithContext returns c carrying ctx: every task of a simulation made with
// it (New) has a context derived from ctx. Put what the whole run shares
// there, above all its step recorder (qobs.WithRecorder), so that runs in
// parallel do not share the global one.
func WithContext(c Chooser, ctx context.Context) Chooser { return choice.WithContext(c, ctx) }

// ContextOf returns the context c carries (WithContext), or
// context.Background(): what a run driven by c elsewhere (in gosim) records
// into.
func ContextOf(c Chooser) context.Context { return choice.ContextOf(c) }

// As finds the first chooser of type T in c's chain: c, then what it
// wraps (Wrapper), and so on.
func As[T any](c Chooser) (T, bool) { return choice.As[T](c) }

// IsFault reports whether a choice kind is a fault choice.
func IsFault(kind string) bool { return choice.IsFault(kind) }

// EntropyOf returns c's source of entropy, through the wrappers of this
// package (WithContext, Recorder, Replay), or nil if it has none.
func EntropyOf(c Chooser) func(n int) []byte { return choice.EntropyOf(c) }

// NewSeeded returns a Seeded chooser with DefaultRates. The seed is a
// uint64, as math/rand/v2's PCG takes one: an int or int64 seed (a loop
// index, a flag) converts with uint64(seed), every value a seed of its
// own, a negative one included.
func NewSeeded(seed uint64) *Seeded { return choice.NewSeeded(seed) }

// NewPCT returns a PCT scheduler of depth d over base (its other
// choices), drawing priorities and change points from seed, for runs of
// about steps scheduling choices (0: 1000): the points where more than
// one task could run, each recorded as a "sched.task" choice, so a
// nominal run's count is how many of its Run.Choices (qtest) have that
// kind. Change points past a run's end make no change. In an exploration:
//
//	ExploreConfig{Chooser: func(seed uint64) qenv.Chooser {
//		base := qenv.NewSeeded(seed)
//		base.Rates = sys.FaultRates()
//		return qenv.NewPCT(base, seed, 3, 500)
//	}}
func NewPCT(base Chooser, seed uint64, depth, steps int) *PCT {
	return choice.NewPCT(base, seed, depth, steps)
}

// Replay repeats recorded choices, then makes every later choice 0. A run
// that asks for a different kind or option count than was recorded is not
// deterministic (it reads time, maps or goroutines outside the simulation):
// Replay stops there and reports it (Replayer.Err), naming the first
// difference.
func Replay(choices []Choice) *Replayer { return choice.Replay(choices) }

// Swarm turns some fault kinds off for a whole run, chosen once from c:
// runs with fewer kinds of faults reach deeper into each kind (swarm
// testing). kinds are as reports print them, such as "rpc"; each is on in about
// half of the runs, and a Hegel-driven swarm shrinks toward all off.
func Swarm(c Chooser, kinds ...string) Chooser { return choice.Swarm(c, kinds...) }

// Without turns the given fault kinds (prefixes, as in Swarm) off for a
// whole run: a sweep's row says which kinds are on (qtest.System.Sweep).
func Without(c Chooser, kinds ...string) Chooser { return choice.Without(c, kinds...) }

// FromBytes makes choices from data (a fuzz input, a corpus entry) at the
// given rates. An encoded run's header (EncodeChoices) is skipped; one of
// another version makes every choice nominal and is reported by Err.
func FromBytes(data []byte, rates Rates) *Bytes { return choice.FromBytes(data, rates) }

// EncodeChoices is the byte stream from which Bytes, at the given rates,
// makes the recorded choices again: a recorded run, or a shrunk Hegel
// failure (qhegel.Violation.Choices), as a fuzz corpus entry. A fault
// of a kind whose rate is 0 cannot be encoded, and neither can entropy
// chunks; both are reported. The bytes start with a header naming the
// byte rule's version (BytesVersion).
func EncodeChoices(choices []Choice, rates Rates) ([]byte, error) {
	return choice.EncodeChoices(choices, rates)
}
