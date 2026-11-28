package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/veilfrag"
	"github.com/chucklehead-dev/gotogether/itf"
	"golang.org/x/mod/semver"
)

// Proof by induction with quint's Veil backend (DESIGN §116): `quint
// verify --backend veil --inductive-invariant I` proves that init
// establishes I and every action of step preserves it, for every value of
// the constants. The inductive invariant of a holds invariant P is P and
// its lemmas, so a proof of it is a proof of P for every instance: more
// than Proves' complete graph of one instance, and never in its place
// (Proves checks the instance the code is checked against, whatever Veil
// can translate). Its outcomes are never conflated:
//
//   - proved: I is inductive, so P holds in every reachable state, for
//     every value of the constants (and of what the main module assumes);
//   - counterexample: a state satisfying I from which an action leaves it
//     (or a state init reaches outside it): no verdict, since P may still
//     hold (a lemma is missing), the two states and the action shown and
//     the ITF kept;
//   - refused: the model is outside Veil's fragment, with Veil's reason:
//     no verdict, never a failure (gotogether lint -veil names the idioms);
//   - undecided: Veil's solver answered neither way: no verdict;
//   - not done: no quint ≥ 0.35 or no Veil installed, with how to install.
//
// A proved implication `A implies B` is checked for vacuity where it is
// cheap: a lemma negating A, a settle flag step never sets, and whether
// the binding's instance reaches A at all.

// VeilVersion is the oldest quint with the Veil backend and its
// --inductive-invariant.
const VeilVersion = "0.35.0"

// veilInstall says how to install quint's Veil backend, which gotogether
// never installs itself.
const veilInstall = "install it once with `quint veil install` (about 5 GB and 10 minutes; QUINT_HOME chooses where); gotogether never installs it"

// An InductiveRequest asks for a proof of the holds invariant Name by
// induction (SimulateOptions.Inductive). One request with no Name stands
// for every invariant of simulate.holds.
type InductiveRequest struct {
	Name string
	// Lemmas are conjoined with Name as the inductive invariant, vals of
	// the binding's simulate.inductive.module (nil: simulate.inductive's
	// lemmas for Name; empty: none).
	Lemmas []string
}

// Outcomes of an InductiveVerdict.
const (
	InductiveProved         = "proved"
	InductiveCounterexample = "counterexample"
	InductiveRefused        = "refused"
	InductiveUndecided      = "undecided"
	InductiveNotDone        = "not done"
)

// An InductiveVerdict is one holds invariant's proof by induction.
type InductiveVerdict struct {
	// Name is the holds invariant, Lemmas its lemmas, Invariant the
	// inductive invariant Veil was given (Name and the lemmas), Module the
	// module proved.
	Name      string
	Lemmas    []string
	Invariant string
	Module    string
	// Outcome is one of InductiveProved, InductiveCounterexample,
	// InductiveRefused, InductiveUndecided, InductiveNotDone.
	Outcome string
	// Reason is Veil's for a refusal or an undecided check, and for a
	// counterexample whose states it could not decode (no States then), and
	// what is missing (and how to install it) for not done.
	Reason string
	// Action is the action a counterexample to induction takes ("": the
	// state is one init reaches), Broken what it breaks (the inductive
	// invariant, or an invariant Veil added), States its states (before
	// and after Action; init's alone), each "var = value, ...", and ITF
	// the kept file of them.
	Action, Broken string
	States         []string
	ITF            string
	// Added are the invariants Veil added from init (the keys of a map)
	// and proved with I.
	Added []string
	// Antecedent is P's antecedent when P is an implication, and Reached
	// what the instance says of it: "reached" (with Witness), "unreached"
	// (in the whole state graph: the proof is vacuous on the instance) or
	// "" (not searched, or not found within the bound).
	Antecedent, Reached, Witness string
	// Vacuous are warnings that the proof may say nothing: a lemma that
	// negates the antecedent, a settle flag step never sets, an antecedent
	// the instance never reaches.
	Vacuous []string
	// Open are the constants the proof covers every value of (the
	// module's own, and those it imports uninstantiated), Fixed those an
	// instance of the module fixes, "NAME = value" each: an instance
	// module's proof is of its values only (DESIGN §116).
	Open, Fixed []string
	// Elapsed is Veil's time.
	Elapsed time.Duration
	// scoped says Open and Fixed were read (from the model's IR)
	scoped bool
	// searchNote says the antecedent's search was not done, and why
	searchNote string
}

// VeilStatus says whether quint's Veil backend can run: "" when it can,
// else why not and how to install it (a not-done reason). An error is a
// quint that could not be asked: one that is there but did not answer
// --version in time (a loaded machine: quintcmd.ProbeTimeoutEnv raises
// the bound) or failed, never a missing one, which is not done.
func VeilStatus(ctx context.Context, quint, dir string) (string, error) {
	v, err := quintcmd.Version(ctx, quint, dir)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if !quintcmd.NotFound(err) {
			return "", err
		}
		return fmt.Sprintf("no quint (%v): install quint ≥ %s (npm i -g @informalsystems/quint@%s), then Veil: %s", err, VeilVersion, VeilVersion, veilInstall), nil
	}
	sv := "v" + strings.TrimPrefix(strings.TrimSpace(v), "v")
	if !semver.IsValid(sv) || semver.Compare(sv, "v"+VeilVersion) < 0 {
		return fmt.Sprintf("quint %s has no Veil backend: install quint ≥ %s (npm i -g @informalsystems/quint@%s), then Veil: %s", v, VeilVersion, VeilVersion, veilInstall), nil
	}
	out, err := quintcmd.Command(ctx, quint, dir, "veil", "status").CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err == nil {
		return "", nil
	}
	if _, exited := errors.AsType[*exec.ExitError](err); !exited {
		return "", fmt.Errorf("quint veil status: %w", err)
	}
	return fmt.Sprintf("Veil is not installed (quint veil status: %s): %s", strings.TrimSpace(string(out)), veilInstall), nil
}

// inductive is simulate's proof of each of opt.Inductive by induction.
func (s *sim) inductive(ctx context.Context, bpath string, b *binding.Binding, sec *binding.Simulation, opt SimulateOptions) error {
	spec, module := b.InductiveSpec(), b.InductiveModule()
	var reqs []InductiveRequest
	asked := opt.Inductive
	if len(asked) == 1 && asked[0].Name == "" {
		// every holds invariant (simulate -inductive alone): the eventual
		// vals of holds are liveness, not induction's
		asked = nil
		for _, h := range sec.Holds {
			if !slices.Contains(b.Eventually, h) {
				asked = append(asked, InductiveRequest{Name: h})
			}
		}
		if len(asked) == 0 {
			return fmt.Errorf("simulate -inductive: %s's simulate.holds lists no invariant to prove", bpath)
		}
	}
	for _, r := range asked {
		switch {
		case slices.Contains(b.Eventually, r.Name):
			return fmt.Errorf("simulate -inductive: %s is an eventual val: induction proves invariants (claim it with -graph's liveness, qtest.Live)", r.Name)
		case !slices.Contains(sec.Holds, r.Name):
			return fmt.Errorf("simulate -inductive: %s is not in %s's simulate.holds (%s): induction proves what the model guarantees; list it in holds", r.Name, bpath, strings.Join(sec.Holds, ", "))
		}
		if r.Lemmas == nil {
			r.Lemmas = b.Lemmas(r.Name)
		}
		reqs = append(reqs, r)
	}
	if _, err := os.Stat(spec); err != nil {
		return fmt.Errorf("simulate -inductive: %s's proof module file: %w (simulate.inductive.spec is relative to the binding)", bpath, err)
	}
	missing, err := VeilStatus(ctx, opt.Quint, filepath.Dir(spec))
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		return fmt.Errorf("simulate -inductive: cannot ask quint whether Veil is installed: %w", err)
	}
	if missing != "" {
		s.printf("simulate %s: %s by induction with Veil (quint verify --backend veil)\n", bpath, module)
		s.rep.NotDone = append(s.rep.NotDone, "the proofs by induction: "+missing)
		for _, r := range reqs {
			s.rep.Inductive = append(s.rep.Inductive, InductiveVerdict{Name: r.Name, Lemmas: r.Lemmas, Module: module, Invariant: conjunction(r), Outcome: InductiveNotDone, Reason: missing})
			s.printf("  not done %s: %s\n", r.Name, missing)
		}
		s.rep.Code = 2
		return nil
	}
	// the IR, for the vacuity checks: a model quint cannot parse fails
	// Veil's run anyway, which says why
	ir, _ := veilfrag.Parse(ctx, opt.Quint, spec)
	var open, fixed []string
	if ir != nil {
		open, fixed = ir.Constants(module)
	}
	s.printf("simulate %s: %s by induction with Veil (quint verify --backend veil), %s\n", bpath, module, scope(ir != nil, open, fixed))
	keep := opt.InductiveOut
	for _, r := range reqs {
		v, err := proveInductive(ctx, opt.Quint, spec, module, r, &keep)
		if err != nil {
			return err
		}
		v.Open, v.Fixed, v.scoped = open, fixed, ir != nil
		if v.Outcome == InductiveProved && ir != nil {
			s.vacuity(ctx, bpath, b, ir, module, &v, opt)
		}
		s.rep.Inductive = append(s.rep.Inductive, v)
		s.printf("%s", inductiveText(v))
		if v.Outcome != InductiveProved {
			s.rep.Code = 2
		}
	}
	return nil
}

// scope is what a proof of a module whose constants are open and fixed
// covers (known: they were read from its IR): every value of them only
// when the module leaves them open, an instance module's values alone.
func scope(known bool, open, fixed []string) string {
	switch {
	case !known:
		return "for every value of the constants the module leaves open"
	case len(open) > 0 && len(fixed) > 0:
		return "for every value of the constants " + strings.Join(open, ", ") + ", with " + strings.Join(fixed, ", ")
	case len(open) > 0:
		return "for every value of the constants " + strings.Join(open, ", ")
	case len(fixed) > 0:
		return "for this instance's constants only (" + strings.Join(fixed, ", ") + "), not for every value of them"
	}
	return "the module has no constants"
}

// conjunction is r's inductive invariant: its invariant and lemmas.
func conjunction(r InductiveRequest) string {
	return strings.Join(append([]string{r.Name}, r.Lemmas...), " and ")
}

// vacuity adds to v, a proof of an implication, what says the proof may
// be vacuous.
func (s *sim) vacuity(ctx context.Context, bpath string, b *binding.Binding, ir *veilfrag.Parsed, module string, v *InductiveVerdict, opt SimulateOptions) {
	for _, f := range ir.Vacuous(module, []string{v.Name}) {
		v.Vacuous = append(v.Vacuous, f.Message)
	}
	a, negating := ir.Antecedent(module, v.Name, v.Lemmas)
	v.Antecedent = a
	for _, l := range negating {
		v.Vacuous = append(v.Vacuous, fmt.Sprintf("the lemma %s negates %s's antecedent %s: the proof rests on %s never holding, and says nothing of the states where it does", l, v.Name, a, a))
	}
	if a == "" {
		return
	}
	// whether the instance reaches the antecedent at all: the Go
	// evaluator's search, then quint's sampling
	rep, err := simulate(ctx, bpath, b, nil, SimulateOptions{Quint: opt.Quint, Seed: opt.Seed, Constants: opt.Constants, States: opt.States, Depth: opt.Depth, Compiled: opt.Compiled,
		Invariants: []string{}, Eventually: []string{}, Possibles: []binding.Possible{{Name: "the antecedent of " + v.Name, When: a}}})
	if err != nil {
		nd := fmt.Sprintf("the search of the instance for %s's antecedent %s, whether the proof is vacuous on it: %v", v.Name, a, firstLine(err.Error()))
		s.rep.NotDone = append(s.rep.NotDone, nd)
		v.searchNote = nd
		return
	}
	if len(rep.Possibles) == 0 {
		return
	}
	switch p := rep.Possibles[0]; {
	case p.Found:
		v.Reached, v.Witness = "reached", p.Verdict
	case p.Impossible:
		v.Reached = "unreached"
		v.Vacuous = append(v.Vacuous, fmt.Sprintf("no state of the instance reaches %s's antecedent %s (%s): on it the proof holds vacuously; an eventual val judged after a recorded settle may be the property meant", v.Name, a, p.Verdict))
	}
}

// veilResult is the part of quint verify's --out that a proof by
// induction reads.
type veilResult struct {
	Stage   string `json:"stage"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Errors  []struct {
		Explanation string `json:"explanation"`
	} `json:"errors"`
	ElapsedMs       int64       `json:"elapsedMs"`
	InductionChecks []veilCheck `json:"inductionChecks"`
	AddedInvariants []string    `json:"addedInvariants"`
}

// veilCheck is one of Veil's induction checks: the invariant, the check
// (initializer or an action) and its status (proven, disproven, unknown).
type veilCheck struct {
	Invariant string `json:"invariant"`
	Check     string `json:"check"`
	Status    string `json:"status"`
}

// proveInductive runs Veil on r's inductive invariant in module of spec.
// A counterexample's ITF is kept in *keep (created on the first one when
// "": a new temporary directory, named in the verdict). An error is a
// quint that failed other than by an outcome: a name the module lacks, a
// model that does not typecheck.
func proveInductive(ctx context.Context, quint, spec, module string, r InductiveRequest, keep *string) (InductiveVerdict, error) {
	v := InductiveVerdict{Name: r.Name, Lemmas: r.Lemmas, Module: module, Invariant: conjunction(r)}
	work, err := os.MkdirTemp("", "gotogether-veil-*")
	if err != nil {
		return v, err
	}
	defer os.RemoveAll(work)
	result, trace := filepath.Join(work, "result.json"), filepath.Join(work, "cti.itf.json")
	argv := []string{"verify", "--backend", "veil", "--main", module, "--inductive-invariant", v.Invariant, "--out", result, "--out-itf", trace, spec}
	out, cmdErr := runQuint(ctx, quint, filepath.Dir(spec), argv)
	if ctx.Err() != nil {
		return v, ctx.Err()
	}
	if cmdErr != nil {
		if e, ok := errors.AsType[*exec.ExitError](cmdErr); !ok || e.ExitCode() != 1 {
			return v, fmt.Errorf("quint verify --backend veil (%s): %w\n%s", r.Name, cmdErr, out)
		}
	}
	data, err := os.ReadFile(result)
	if err != nil {
		return v, fmt.Errorf("quint verify --backend veil (%s): no structured result: %w\n%s", r.Name, err, out)
	}
	var res veilResult
	if err := json.Unmarshal(data, &res); err != nil {
		return v, fmt.Errorf("quint verify --backend veil (%s): its result: %w", r.Name, err)
	}
	v.Elapsed, v.Added = time.Duration(res.ElapsedMs)*time.Millisecond, res.AddedInvariants
	if res.Stage != "verifying" || len(res.Errors) > 0 {
		var why []string
		for _, e := range res.Errors {
			why = append(why, e.Explanation)
		}
		return v, fmt.Errorf("quint verify --backend veil: %s of %s fails at %s (%s): each of %s is a val of module %s (simulate.inductive.module, its lemmas simulate.inductive.lemmas)",
			v.Invariant, module, res.Stage, strings.Join(append(why, res.Message), "; "), v.Invariant, module)
	}
	switch {
	case res.Status == "ok" && cmdErr == nil:
		v.Outcome = InductiveProved
	case res.Status == "violation":
		v.Outcome = InductiveCounterexample
		v.Broken, v.Action = ctiWhere(res)
		if t, err := itf.ReadFile(trace); err == nil {
			for _, st := range t.States {
				var kv []string
				for _, name := range t.Vars {
					kv = append(kv, name+" = "+st.Vars[name].String())
				}
				v.States = append(v.States, strings.Join(kv, ", "))
			}
			if *keep == "" {
				if *keep, err = os.MkdirTemp("", "gotogether-inductive-*"); err != nil {
					return v, err
				}
			} else if err := os.MkdirAll(*keep, 0o755); err != nil {
				return v, err
			}
			kept := filepath.Join(*keep, r.Name+".cti.itf.json")
			if raw, err := os.ReadFile(trace); err == nil && os.WriteFile(kept, raw, 0o644) == nil {
				v.ITF = kept
			}
		}
	case ctiUnshown(res) != "":
		// a counterexample whose states Veil could not decode
		// (Induction.lean's statesOf, its map reader): one all the same
		v.Outcome, v.Reason = InductiveCounterexample, ctiUnshown(res)
		v.Broken, v.Action = ctiWhere(res)
	case strings.HasPrefix(res.Message, "Veil could not decide"):
		v.Outcome, v.Reason = InductiveUndecided, strings.TrimSpace(strings.TrimPrefix(res.Message, "Veil could not decide:"))
	case strings.HasPrefix(res.Message, "Veil could not check the spec"):
		v.Outcome, v.Reason = InductiveRefused, refusal(res.Message)
	default:
		return v, fmt.Errorf("quint verify --backend veil (%s): status %q: %s\n%s", r.Name, res.Status, res.Message, out)
	}
	return v, nil
}

// ctiUnshown is Veil's reason a counterexample to induction has no
// states ("the model has no post-state; ..."), "" if res is none: a
// disproven check whose failure says "in a state Veil could not show: "
// (or "that could not be read: "), the reason up to quint's hint on the
// next line.
func ctiUnshown(res veilResult) string {
	if res.Status != "failure" || !slices.ContainsFunc(res.InductionChecks, func(c veilCheck) bool { return c.Status == "disproven" }) {
		return ""
	}
	first := firstLine(res.Message)
	for _, marker := range []string{", in a state Veil could not show: ", ", in a state that could not be read: "} {
		if _, why, ok := strings.Cut(first, marker); ok {
			return strings.TrimSpace(why)
		}
	}
	return ""
}

// ctiWhere is what a counterexample to induction breaks and the action
// it takes ("": the initializer's, which Veil's ITF and message show
// whenever init fails, whatever the order of its checks).
func ctiWhere(res veilResult) (broken, action string) {
	for _, c := range res.InductionChecks {
		if c.Status != "disproven" {
			continue
		}
		if broken == "" || c.Check == "initializer" && action != "initializer" {
			broken, action = brokenName(c.Invariant, res.AddedInvariants), c.Check
		}
	}
	if action == "initializer" {
		action = ""
	}
	if broken == "" {
		broken = "the inductive invariant"
	}
	return broken, action
}

// refusal is Veil's reason in its message, without the generated Lean
// file's path: "the size of a set whose elements are not known is not
// supported".
func refusal(msg string) string {
	lines := strings.Split(strings.TrimSpace(msg), "\n")
	last := lines[len(lines)-1]
	if i := strings.LastIndex(last, "quint_module: "); i >= 0 {
		return last[i+len("quint_module: "):]
	}
	if i := strings.LastIndex(last, "error: "); i >= 0 {
		return last[i+len("error: "):]
	}
	return last
}

// brokenName is what a disproven check breaks, as a report says it.
func brokenName(inv string, added []string) string {
	switch {
	case inv == "q::inductiveInv":
		return "the inductive invariant"
	case inv == "doesNotThrow":
		return "an action's evaluation (a read of a map outside its keys, a nondet over an empty set)"
	case strings.HasPrefix(inv, "q::keys::"):
		m := strings.TrimPrefix(inv, "q::keys::")
		for _, a := range added {
			if strings.HasPrefix(a, m+".keys()") {
				return a + " (Veil added it from init: state the keys of " + m + " in a lemma)"
			}
		}
		return "the keys of " + m + " (Veil added them from init: state them in a lemma)"
	}
	return inv
}

// InductiveText is v's lines of the report, as simulate prints them.
func InductiveText(v InductiveVerdict) string { return inductiveText(v) }

func inductiveText(v InductiveVerdict) string {
	var b strings.Builder
	with := ""
	if len(v.Lemmas) > 0 {
		with = " with " + strings.Join(v.Lemmas, ", ")
	}
	switch v.Outcome {
	case InductiveProved:
		added := ""
		if len(v.Added) > 0 {
			added = "; Veil added " + strings.Join(v.Added, " and ") + " from init and proved it too"
		}
		took := v.Elapsed.Round(100 * time.Millisecond).String()
		where := " " + scope(v.scoped, v.Open, v.Fixed)
		if v.scoped && len(v.Open) == 0 && len(v.Fixed) == 0 {
			where, took = "", "the module has no constants; "+took
		}
		fmt.Fprintf(&b, "  proved   %s%s: inductive, so it holds in every reachable state%s (%s%s)\n", v.Name, with, where, took, added)
		if v.Reached == "reached" {
			fmt.Fprintf(&b, "    its antecedent %s is reached on the instance: %s\n", v.Antecedent, v.Witness)
		}
		for _, w := range v.Vacuous {
			fmt.Fprintf(&b, "    warning: vacuous? %s\n", w)
		}
		if v.searchNote != "" {
			fmt.Fprintf(&b, "    not done: %s\n", v.searchNote)
		}
	case InductiveCounterexample:
		where := "after " + v.Action
		if v.Action == "" {
			where = "at the initializer"
		}
		fmt.Fprintf(&b, "  no verdict %s%s: not inductive: a counterexample to induction %s breaks %s (%s may still hold: add a lemma, simulate.inductive.lemmas or qtest.Inductive(...).With)\n", v.Name, with, where, v.Broken, v.Name)
		if v.Action == "" {
			fmt.Fprintf(&b, "    a state init reaches is outside %s: if %s itself fails there the model violates it (simulate -graph shows it), else a lemma does not hold at init\n", v.Invariant, v.Name)
		}
		if len(v.States) == 0 && v.Reason != "" {
			hint := "simulate -graph says whether the instance reaches a state that breaks it"
			if strings.Contains(v.Reason, "non-enumerable keys") {
				hint = "Veil reads no map keyed by int or a constant's type: keyed by str or an enumeration it shows the states; " + hint
			}
			fmt.Fprintf(&b, "    its states: Veil could not show them: %s (%s)\n", v.Reason, hint)
		}
		for i, st := range v.States {
			label := "init"
			if v.Action != "" {
				label = "before"
				if i > 0 {
					label = "after " + v.Action
				}
			}
			fmt.Fprintf(&b, "    %s: %s\n", label, st)
		}
		if v.ITF != "" {
			fmt.Fprintf(&b, "    ITF: %s\n", v.ITF)
		}
	case InductiveRefused:
		fmt.Fprintf(&b, "  refused  %s: not in Veil's fragment: %s (no verdict, not a failure: gotogether lint -veil names the idioms Veil refuses and their rewrites; Proves checks the instance)\n", v.Name, v.Reason)
	case InductiveUndecided:
		fmt.Fprintf(&b, "  no verdict %s%s: Veil's solver decided neither way: %s\n", v.Name, with, v.Reason)
	default:
		fmt.Fprintf(&b, "  not done %s: %s\n", v.Name, v.Reason)
	}
	return b.String()
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
