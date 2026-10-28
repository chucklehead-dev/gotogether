package qtest

import (
	"fmt"
	"math"

	"github.com/chucklehead-dev/gotogether/internal/confidence"
)

// What a passing exploration covers, and the bound that limits it: a
// claim that every run, or no run, shows something is a proof only of
// the runs made. Seeded runs are a sample (SampleBound), an exhaustive
// search is complete within its bound or truncated (Extent), a PCT
// schedule has a probability per run on conditions (PCTBound), and a
// pass the Go evaluator judged alone is quint's only for the share quint
// sampled (Judges). Evidence carries each, and a passing Expect's
// summary says each in words.

// Judges says who judged an exploration's conforming runs: rejections
// and violations are always quint's, a pass the Go evaluator's unless
// quint replayed it too (a sample of the passes, GOTOGETHER_EVAL_SAMPLE;
// every run under GOTOGETHER_EVAL=quint or shadow).
type Judges struct {
	// Evaluator are the conforming runs an actor of which the Go evaluator
	// alone passed, with no quint replay: what the evaluator is trusted
	// with (the nightly shadow checks it against quint).
	Evaluator int `json:"evaluator"`
	// Confirmed are the evaluator's passes quint judged too (sampled) and
	// agreed with: a disagreement fails its run.
	Confirmed int `json:"confirmed"`
	// The rest of the conforming runs quint judged alone.
}

// A SampleBound is what a seeded exploration's universal claims (All,
// None, Conforms) are worth: Runs independent runs, none of which showed
// a failure the claim forbids, bound its rate below Rate at 95%
// confidence (confidence.Rate: about 3/Runs): a failure that 1 run in
// OneIn shows is missed at most 5% of the time, a rarer one more often.
// It bounds nothing of a failure the runs cannot reach, whatever their
// number: a fault never on, a workload that never asks, a run too short.
type SampleBound struct {
	Runs  int     `json:"runs"`
	Rate  float64 `json:"rate"`
	OneIn int     `json:"oneIn"`
}

// A PCTBound is a PCT exploration's guarantee (qenv.PCT): a bug of depth
// up to Depth is found in a run with probability at least Probability,
// 1/(MaxTasks·Steps^(Depth-1)), MaxTasks the most tasks a run created.
// It holds only of runs of at most Steps scheduling steps: when a run
// took more (MaxSteps), the change points never fall in its tail, and
// Probability is 0, the guarantee void for that run length.
type PCTBound struct {
	Depth       int     `json:"depth"`
	Steps       int     `json:"steps"`
	MaxSteps    int     `json:"maxSteps"`
	MaxTasks    int     `json:"maxTasks"`
	Probability float64 `json:"probability"`
}

// pctRun is what a PCT chooser saw of a run (qenv.PCT.Seen): its depth
// and Steps, the scheduling steps it chose at and the tasks it ranked.
type pctRun struct{ depth, bound, steps, tasks int }

// judges counts who judged the conforming runs (nil when quint judged
// every one alone).
func (x *Exploration) judges() *Judges {
	var j Judges
	for _, r := range x.Runs {
		if r == nil || r.Report == nil || !r.Report.OK() {
			continue
		}
		by := "quint"
		for _, a := range r.Report.Actors {
			switch a.JudgedBy() {
			case "evaluator":
				by = "evaluator"
			case "both":
				if by == "quint" {
					by = "both"
				}
			}
		}
		switch by {
		case "evaluator":
			j.Evaluator++
		case "both":
			j.Confirmed++
		}
	}
	if j == (Judges{}) {
		return nil
	}
	return &j
}

// sampleBound is a seeded exploration's (nil for a search, a run alone,
// runs given one by one, or none).
func (x *Exploration) sampleBound() *SampleBound {
	n := len(x.Runs)
	if x.search != nil || x.alone != nil || x.given || n == 0 {
		return nil
	}
	return &SampleBound{Runs: n, Rate: confidence.Rate(n), OneIn: confidence.OneIn(n)}
}

// pctBound is a PCT exploration's guarantee over its runs (nil: no run
// was PCT's).
func (x *Exploration) pctBound() *PCTBound {
	var b *PCTBound
	for _, r := range x.Runs {
		if r == nil || r.pct == nil {
			continue
		}
		if b == nil {
			b = &PCTBound{Depth: r.pct.depth, Steps: r.pct.bound}
		}
		b.MaxSteps, b.MaxTasks = max(b.MaxSteps, r.pct.steps), max(b.MaxTasks, r.pct.tasks)
	}
	if b != nil && b.MaxSteps <= b.Steps && b.MaxTasks > 0 {
		b.Probability = 1 / (float64(b.MaxTasks) * math.Pow(float64(b.Steps), float64(b.Depth-1)))
	}
	return b
}

// conformWords is who judged the conforming runs, as the summary says it
// after "all conform": "" when quint judged each.
func (j *Judges) conformWords(conform int) string {
	switch {
	case j == nil:
		return ""
	case j.Evaluator == 0:
		return fmt.Sprintf(" (quint confirmed %d)", j.Confirmed)
	case j.Evaluator >= conform:
		return " (the Go evaluator's, none confirmed by quint)"
	}
	return fmt.Sprintf(" (quint confirmed %d, the Go evaluator alone the other %d)", conform-j.Evaluator, j.Evaluator)
}

// boundWords is what limits the exploration's claims, for its summary:
// an exhaustive search's bound and whether it was complete within it, a
// PCT guarantee and its conditions, and, when a universal claim passed
// (universal), what a sample of seeds bounds.
func (x *Exploration) boundWords(universal bool) string {
	var out string
	if s := x.search; s != nil {
		within := fmt.Sprintf("within %s and %s", plural(s.bound.Delays, "delay"), plural(s.bound.Faults, "fault"))
		if s.left > 0 {
			out += fmt.Sprintf("; truncated %s: at least %d runs within it left untried (Bound.Partial), so the claims are about the runs it got to", within, s.left)
		} else if x.alone == nil {
			out += fmt.Sprintf("; complete %s: every run within that bound, none beyond it", within)
		}
	}
	if b := x.pctBound(); b != nil {
		head := fmt.Sprintf("; PCT depth %d, Steps %d: ", b.Depth, b.Steps)
		if b.Probability == 0 {
			out += head + fmt.Sprintf("runs took up to %d scheduling steps, past Steps, so its guarantee holds of none of them (no change point falls past Steps): set Steps to %d (qenv.NewPCT)", b.MaxSteps, b.MaxSteps)
		} else {
			out += head + fmt.Sprintf("a bug of depth up to %d is found in a run with probability at least 1/(%d tasks·%d^%d) = %.2g, if every task is counted (the most a run ranked)", b.Depth, b.MaxTasks, b.Steps, b.Depth-1, b.Probability)
		}
	}
	if sb := x.sampleBound(); sb != nil && universal {
		out += "; a sample, no proof: " + confidence.Brief(sb.Runs, "run", "a failure") + ", one the runs cannot reach always"
	}
	return out
}

// plural is n with its noun, plural but for 1: "1 fault", "2 delays".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
