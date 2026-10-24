package choice

import "math/rand/v2"

// PCT schedules tasks by Probabilistic Concurrency Testing (Burckhardt,
// Kothari, Musuvathi and Nagarakatte, ASPLOS 2010): every task gets a
// random priority when it is first runnable, the highest-priority runnable
// task always runs, and at Depth-1 scheduling steps drawn in 1..Steps the
// task that ran last drops below every other. A bug that needs Depth
// orderings to happen (a task to run entirely before another's step, then
// one more) is found with probability at least 1/(n·Steps^(Depth-1)) per
// run for n tasks, where a uniformly random schedule may need every step
// to go one way: 2^-100 for a task that must finish 100 steps first.
//
// It chooses among tasks by id (ChooseTask, which qenv's scheduler asks
// it through the chain of wrappers), and leaves every other choice
// (faults, a queue's order) to Base. Its picks are recorded as the
// schedule choices they stand for, so a ChoiceLog of a PCT run replays
// without it. gosim's own scheduler is not qenv's, and does not ask.
type PCT struct {
	Base  Chooser // every choice but which task runs: NewSeeded's, at the run's Rates
	Depth int     // the bug depth d: d-1 change points (at least 1)
	Steps int     // about how many scheduling steps a run takes (default 1000)

	r      *rand.Rand
	prio   map[int]float64
	change map[int]int // step -> its change point j (1..Depth-1)
	step   int
	last   int
}

// NewPCT returns a PCT scheduler of depth d over base, drawing its
// priorities and change points from seed, for runs of about steps
// scheduling steps (0: 1000).
//
//	base := qenv.NewSeeded(seed)
//	base.Rates = sys.FaultRates()
//	return qenv.NewPCT(base, seed, 3, 500)
func NewPCT(base Chooser, seed uint64, depth, steps int) *PCT {
	if depth < 1 {
		depth = 1
	}
	if steps <= 0 {
		steps = 1000
	}
	p := &PCT{Base: base, Depth: depth, Steps: steps, r: rand.New(rand.NewPCG(seed, 0x5bd1e9955bd1e995)),
		prio: map[int]float64{}, change: map[int]int{}}
	for j := 1; j < depth; j++ {
		p.change[1+p.r.IntN(steps)] = j // two at one step: the later j wins, a depth lost, as rare as it is in the paper's
	}
	return p
}

// Choose implements Chooser: Base's choice.
func (p *PCT) Choose(kind string, n int) int { return p.Base.Choose(kind, n) }

// Unwrap implements Wrapper.
func (p *PCT) Unwrap() Chooser { return p.Base }

// ChooseTask picks the task to run among the runnable ones, by id, and
// returns its index in runnable.
func (p *PCT) ChooseTask(runnable []int) int {
	p.step++
	if j, ok := p.change[p.step]; ok && p.last != 0 {
		p.prio[p.last] = float64(p.Depth - j) // below every initial priority (>= Depth), later ones lower
	}
	best := 0
	for i, id := range runnable {
		if _, ok := p.prio[id]; !ok {
			p.prio[id] = float64(p.Depth) + p.r.Float64()
		}
		if p.prio[id] > p.prio[runnable[best]] {
			best = i
		}
	}
	p.last = runnable[best]
	return best
}
