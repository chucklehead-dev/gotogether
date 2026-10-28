package qeval

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
)

// The differential test: random well-typed expressions and actions
// (gen_test.go), each evaluated by the evaluator and by quint's two
// backends, a batch of them in one module (a val, a variable and a run
// assigning it for each: one `quint test` process a backend, each run's
// value in its ITF). Every difference where the evaluator answered (a
// value, or the model's runtime error; not Unsupported) is a bug: the
// evaluator decides passing runs (DESIGN §56), and a wrong "holds" is
// the worst answer the toolkit can give. Each is shrunk to a minimal
// expression and reported.
//
// Per push: a fixed corpus (GOTOGETHER_QEVAL_FUZZ batches, 2 unset, of
// GOTOGETHER_QEVAL_FUZZ_SIZE entries, 100 unset), at seeds 1..n, or
// GOTOGETHER_SEED_OFFSET+1.. (the nightly's, by date, with many
// batches). GOTOGETHER_QEVAL_FUZZ_SEED=s runs batch s alone.
func TestDifferentialAgainstQuint(t *testing.T) {
	skip.Quint(t)
	if testing.Short() {
		t.Skip("three quint processes a batch, each typechecking hundreds of expressions")
	}
	batches := envNum(t, "GOTOGETHER_QEVAL_FUZZ", 2)
	size := envNum(t, "GOTOGETHER_QEVAL_FUZZ_SIZE", 100)
	offset := envNum(t, "GOTOGETHER_SEED_OFFSET", 0)
	seeds := make([]uint64, batches)
	for i := range seeds {
		seeds[i] = uint64(offset + i + 1)
	}
	if s := envNum(t, "GOTOGETHER_QEVAL_FUZZ_SEED", 0); s > 0 {
		seeds = []uint64{uint64(s)}
	}
	var mu sync.Mutex
	var total tally
	t.Run("batches", func(t *testing.T) {
		for _, seed := range seeds {
			t.Run(fmt.Sprint(seed), func(t *testing.T) {
				t.Parallel()
				entries := generate(seed, size)
				tl := differ(t, entries, seed, fmt.Sprintf("GOTOGETHER_QEVAL_FUZZ_SEED=%d", seed))
				mu.Lock()
				total.add(tl)
				mu.Unlock()
			})
		}
	})
	t.Logf("%d batches: %s", len(seeds), total)
}

// The disagreements the differential test found, each fixed: the
// evaluator's outcome now (a value, "error" for the model's runtime
// error, "refused" for Unsupported), checked with both backends' on every
// push. Each fails with its fix undone.
var differentialFindings = []struct{ typ, expr, want string }{
	// a let is evaluated where its name is read (bindLet): the evaluator
	// bound it eagerly and threw where quint, on both backends, gives 0
	{"int", "{ val n = 1 / 0; 0 }", "value 0"},
	{"bool", "{ val n = Map().get(1); 1 > 0 }", "value true"},
	{"int", "{ val n = List().head(); if (false) n else 3 }", "value 3"},
	{"int", "{ val n = 1 / 0; n + 1 }", "error"},
	// exists and forall evaluate every element: TypeScript fails on any,
	// Rust stops at a witness in its own order; the evaluator stopped at
	// the first in its order, giving false where TypeScript fails
	{"bool", "Set(5, 9, 33).forall(x => if (x == 33) 1 / 0 > 0 else false)", "refused"},
	{"bool", "Set(5, 9, 33).exists(x => if (x == 5) 1 / 0 > 0 else true)", "refused"},
	{"bool", "Set(0, 1).exists(x => 1 / x > 5)", "error"},
	{"bool", "Set(0, 1).exists(x => 1 / x > 0)", "refused"}, // both backends fail, Rust by its order
	{"bool", "Set(1, 2).forall(x => x > 0)", "value true"},
	// setToMap of two pairs of one key: which one quint keeps is its
	// set's order (Rust the first here, TypeScript the last); the
	// evaluator kept the greatest pair's
	{"str -> List[str]", `Set(("k", ["a", "a", ""]), ("k", ["ab", "a", "ab"])).setToMap()`, "refused"},
	{"int -> int", "Set((1, 2), (3, 2), (1, 2)).setToMap()", "value Map(1 -> 2, 3 -> 2)"},
}

func TestDifferentialFindings(t *testing.T) {
	skip.Quint(t)
	var entries []gentry
	for _, f := range differentialFindings {
		entries = append(entries, gentry{t: &gtyp{k: -1, names: []string{f.typ}}, expr: &gnode{op: "lit", lit: f.expr}})
	}
	b := &gbatch{entries: entries, drop: map[int]bool{}}
	q, qs, err := b.run(t.Context(), keepDir(t, "findings"), 1, backends, func(e gentry, msg string) {
		t.Errorf("ill-typed: %s: %s", e.root(), msg)
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range differentialFindings {
		if b.drop[i] {
			continue
		}
		if got := q[i].String(); !strings.HasPrefix(got, f.want) {
			t.Errorf("%s: the evaluator's %s, want %s", f.expr, got, f.want)
		}
		for _, be := range backends {
			if d, _ := judge(b, q, qs, i, be); d != nil {
				t.Errorf("%s: the evaluator and quint (%s) disagree (%s): %s, quint %s", f.expr, be, d.what, d.q, d.b)
			}
		}
	}
}

func envNum(t *testing.T, name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		t.Fatalf("%s=%q: a count, or unset", name, v)
	}
	return n
}

// A gentry is an expression of type t, or an action assigning a value of
// t to v (succ: the index of the entry holding its successors as an
// expression), from no state or from v = from, its run's first step.
type gentry struct {
	t    *gtyp
	expr *gnode
	act  *gnode
	succ int
	v    string
	from *gnode
}

func (e gentry) root() *gnode {
	if e.act != nil {
		return e.act
	}
	return e.expr
}

func (e gentry) typeText() string {
	if e.t.k == -1 { // a regression's, given as text
		return e.t.names[0]
	}
	return e.t.String()
}

// generate is the batch of seed: about a quarter actions, each followed by its
// successors' expression.
func generate(seed uint64, n int) []gentry {
	g := newGen(seed)
	var out []gentry
	for len(out) < n {
		t := g.randType(2)
		if g.chance(0.25) {
			v := fmt.Sprintf("v%d", len(out))
			var from *gnode
			var env []gbind
			if g.chance(0.5) { // from a state: the action reads v
				from, env = g.literal(t, 2), []gbind{{v, t}}
			}
			a := g.action(v, t, 1+g.r.IntN(3), env)
			out = append(out, actionPair(len(out), t, a, v, from)...)
			continue
		}
		out = append(out, gentry{t: t, expr: g.expr(t, 1+g.r.IntN(4), nil)})
	}
	return out
}

// actionPair is action a, assigning v, as entry i of a batch (its
// variable v<i>), and its successors as an expression, entry i+1: from a
// state, v read as its value there.
func actionPair(i int, t *gtyp, a *gnode, v string, from *gnode) []gentry {
	nv := fmt.Sprintf("v%d", i)
	a = a.clone()
	descend(a, nil, func(n *gnode, _ []string) {
		if (n.op == "assign" || n.op == "name") && n.lit == v {
			n.lit = nv
		}
	})
	succ := successors(a, t)
	if from != nil {
		succ = subst(succ, nv, from)
	}
	return []gentry{{t: t, act: a, succ: i + 1, v: nv, from: from}, {t: setOfT(t), expr: succ}}
}

// subst is n with each read of name replaced by val.
func subst(n *gnode, name string, val *gnode) *gnode {
	if n.op == "name" && n.lit == name {
		return val.clone()
	}
	c := *n
	c.kids = make([]*gnode, len(n.kids))
	for i, k := range n.kids {
		c.kids[i] = subst(k, name, val)
	}
	return &c
}

// ---- outcomes -----------------------------------------------------------

// A goutcome is what one evaluator made of an entry.
type goutcome struct {
	val     Value   // an expression's value (nil past 64 bits: raw)
	raw     string  // quint's value as ITF, for a report
	vals    []Value // the values an action assigns, sorted (none: disabled)
	err     string  // a runtime error (quint's code, the evaluator's message)
	isFalse bool    // quint: the run returned false (the action disabled)
	refused string  // the evaluator's Unsupported
	partial bool    // the evaluator: some of a choice's values disable the action
	missing bool    // quint wrote nothing for it
}

func (o goutcome) String() string {
	switch {
	case o.refused != "":
		return "refused (" + o.refused + ")"
	case o.err != "":
		return "error " + o.err
	case o.isFalse:
		return "false (disabled)"
	case o.missing:
		return "no result"
	case o.vals != nil:
		return "successors " + String(&Set{o.vals})
	case o.val == nil && o.raw != "":
		return "value " + o.raw
	case o.val == nil:
		return "disabled (no successor)"
	}
	return "value " + String(o.val)
}

// evaluate is the evaluator's outcome of entry i (e<i>) of m.
func evaluate(m *Model, e gentry, i int) (o goutcome) {
	name := fmt.Sprintf("e%d", i)
	if e.act == nil {
		v, err := m.Eval(nil, State{}, name)
		return fromErr(o, v, err)
	}
	v, st := fmt.Sprintf("v%d", i), State{}
	if e.from != nil {
		x, err := m.Eval(nil, State{}, fmt.Sprintf("s%d", i))
		if err != nil {
			o.refused = "harness: the start state: " + err.Error()
			return o
		}
		st[v] = x
	}
	vals, err := assigned(m, name, v, st)
	if err != nil {
		return fromErr(o, nil, err)
	}
	o.vals = vals
	if o.vals == nil {
		o.vals = []Value{}
	}
	if _, err := assigned(m.AsReplayed(), name, v, st); err != nil {
		var u *Unsupported
		if errors.As(err, &u) && u.Choice != "" {
			o.partial = true
		}
	}
	return o
}

func fromErr(o goutcome, v Value, err error) goutcome {
	var u *Unsupported
	var ee *EvalError
	switch {
	case errors.As(err, &u):
		o.refused = u.Op + ": " + u.Why
	case errors.As(err, &ee):
		o.err = ee.Msg
	case err != nil:
		o.refused = "harness: " + err.Error()
	default:
		o.val = v
	}
	return o
}

// assigned is the values action name may assign v from state st, sorted.
func assigned(m *Model, name, v string, st State) (vals []Value, err error) {
	defer recoverInto(&err)
	d, ok := m.Decl(name)
	if !ok {
		return nil, fmt.Errorf("no %s", name)
	}
	ev := m.evaluation(nil, st)
	body, en, err := ev.bindArgs(d, nil, nil, name)
	if err != nil {
		return nil, err
	}
	succs, err := ev.action(body, en)
	if err != nil {
		return nil, err
	}
	for _, s := range succs {
		x, ok := s.a[v]
		if !ok || len(s.a) != 1 {
			return nil, fmt.Errorf("the action assigns %v, not %s alone", s.a, v)
		}
		vals = append(vals, x)
	}
	return NewSet(vals).Elems, nil
}

// ---- the batch ----------------------------------------------------------

var backends = []string{"rust", "typescript"}

// A gbatch is entries rendered as one module: e<i> an expression's val or
// an action, v<i> its variable, t<i> the run that assigns it.
type gbatch struct {
	entries []gentry
	drop    map[int]bool // ill-typed: out of the module
	lines   []int        // each entry's first line in the module, then its run's
	runs    []int
}

func (b *gbatch) module() string {
	var w strings.Builder
	line := 1
	put := func(s string) {
		w.WriteString(s)
		w.WriteByte('\n')
		line += strings.Count(s, "\n") + 1
	}
	put("module fz {")
	put("  type V = VA(int) | VB(bool) | VC")
	b.lines, b.runs = make([]int, len(b.entries)), make([]int, len(b.entries))
	for i, e := range b.entries {
		if b.drop[i] {
			continue
		}
		b.lines[i] = line
		put(fmt.Sprintf("  var v%d: %s", i, e.typeText()))
		if e.act != nil {
			if e.from != nil {
				put(fmt.Sprintf("  val s%d = %s", i, e.from))
			}
			put(fmt.Sprintf("  action e%d = %s", i, e.act))
		} else {
			put(fmt.Sprintf("  val e%d = %s", i, e.expr))
		}
	}
	for i, e := range b.entries {
		if b.drop[i] {
			continue
		}
		b.runs[i] = line
		switch {
		case e.act != nil && e.from != nil:
			put(fmt.Sprintf("  run t%d = (v%d' = s%d).then(e%d)", i, i, i, i))
		case e.act != nil:
			put(fmt.Sprintf("  run t%d = e%d", i, i))
		default:
			put(fmt.Sprintf("  run t%d = v%d' = e%d", i, i, i))
		}
	}
	put("  action init = true")
	put("  action step = true")
	put("}")
	return w.String()
}

// entryAt is the entry whose text holds line.
func (b *gbatch) entryAt(line int) int {
	best := -1
	for i := range b.entries {
		if b.drop[i] {
			continue
		}
		if b.lines[i] <= line && (best < 0 || b.lines[i] > b.lines[best]) {
			best = i
		}
		if b.runs[i] == line {
			return i
		}
	}
	return best
}

var errLine = regexp.MustCompile(`fz\.qnt:(\d+):\d+`)

// run compiles the batch (dropping the entries quint's typecheck refuses,
// each reported by ill), evaluates each with the evaluator and with
// quint's backends. dir holds the module.
func (b *gbatch) run(ctx context.Context, dir string, seed uint64, bes []string, ill func(gentry, string)) (q []goutcome, qs map[string][]goutcome, err error) {
	file := filepath.Join(dir, "fz.qnt")
	var ir []byte
	for range 20 {
		if err := os.WriteFile(file, []byte(b.module()), 0o644); err != nil {
			return nil, nil, err
		}
		// to stdout, a file, as compile (eval_test.go) does: --out writes
		// the modules unflattened, and a pipe loses what quint has not
		// written when it exits
		irPath := filepath.Join(dir, "ir.json")
		f, err := os.Create(irPath)
		if err != nil {
			return nil, nil, err
		}
		cmd := quintcmd.Command(ctx, "", dir, "compile", "--target", "json", "--main", "fz", file)
		var stderr strings.Builder
		cmd.Stdout, cmd.Stderr = f, &stderr
		err = cmd.Run()
		f.Close()
		if err == nil {
			if ir, err = os.ReadFile(irPath); err != nil {
				return nil, nil, err
			}
			break
		}
		out, _ := os.ReadFile(irPath)
		msg := string(out) + stderr.String()
		if !errLine.MatchString(msg) {
			// compile says nothing of a parse error: typecheck does
			tc := quintcmd.Command(ctx, "", dir, "typecheck", file)
			text, _ := tc.CombinedOutput()
			msg += string(text)
		}
		ms := errLine.FindAllStringSubmatch(msg, -1)
		if len(ms) == 0 {
			return nil, nil, fmt.Errorf("quint compile: %s", msg)
		}
		for _, mm := range ms {
			l, _ := strconv.Atoi(mm[1])
			i := b.entryAt(l)
			if i < 0 {
				return nil, nil, fmt.Errorf("quint compile, line %d: %s", l, msg)
			}
			if !b.drop[i] {
				b.drop[i] = true
				ill(b.entries[i], firstLines(msg, 3))
				if b.entries[i].act != nil && b.entries[i].succ < len(b.entries) {
					b.drop[b.entries[i].succ] = true
				}
			}
		}
	}
	if ir == nil {
		return nil, nil, fmt.Errorf("quint compile: still failing after dropping entries")
	}
	m, err := Load(ir)
	if err != nil {
		return nil, nil, err
	}
	q = make([]goutcome, len(b.entries))
	for i, e := range b.entries {
		if !b.drop[i] {
			q[i] = evaluate(m, e, i)
		}
	}
	qs = map[string][]goutcome{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	errs := make([]error, len(bes))
	for k, be := range bes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := b.quintTest(ctx, dir, file, be, seed)
			mu.Lock()
			qs[be], errs[k] = res, err
			mu.Unlock()
		}()
	}
	wg.Wait()
	return q, qs, errors.Join(errs...)
}

func firstLines(s string, n int) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	return strings.Join(ls[:min(n, len(ls))], " / ")
}

var (
	passedLine = regexp.MustCompile(`^\s*ok (t\d+) passed`)
	failedLine = regexp.MustCompile(`^\s*\d+\) (t\d+) failed`)
	detailLine = regexp.MustCompile(`^\s*\d+\) (t\d+):\s*$`)
	errorLine  = regexp.MustCompile(`Error \[(QNT\d+)\]: (.*)`)
)

// quintTest is backend be's outcome of each entry: its run's ITF state,
// or its failure as quint reports it.
func (b *gbatch) quintTest(ctx context.Context, dir, file, be string, seed uint64) ([]goutcome, error) {
	out := filepath.Join(dir, be)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	cmd := quintcmd.Command(ctx, "", dir, "test", "--main", "fz", "--backend", be, "--match", "^t[0-9]+$",
		"--max-samples", "3", "--seed", fmt.Sprint(seed), "--out-itf", filepath.Join(out, "{test}.itf.json"), file)
	// to a file: a pipe loses what quint has not written when it exits
	// (the TypeScript backend's report stopped after 23 failures)
	outPath := filepath.Join(dir, be+".out")
	f, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = f, f
	err = cmd.Run()
	f.Close()
	text, rerr := os.ReadFile(outPath)
	if rerr != nil {
		return nil, rerr
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("quint test --backend %s: %v", be, ctx.Err())
	}
	res := make([]goutcome, len(b.entries))
	for i := range res {
		res[i].missing = true
	}
	idx := func(name string) int {
		i, _ := strconv.Atoi(strings.TrimPrefix(name, "t"))
		if i < 0 || i >= len(res) {
			return 0
		}
		return i
	}
	var cur = -1
	for _, l := range strings.Split(string(text), "\n") {
		switch {
		case passedLine.MatchString(l):
			res[idx(passedLine.FindStringSubmatch(l)[1])].missing = false
		case failedLine.MatchString(l):
			i := idx(failedLine.FindStringSubmatch(l)[1])
			res[i].missing, res[i].err = false, "failed"
		case detailLine.MatchString(l):
			cur = idx(detailLine.FindStringSubmatch(l)[1])
		case cur >= 0 && errorLine.MatchString(l):
			m := errorLine.FindStringSubmatch(l)
			if m[1] == "QNT511" { // the run returned false
				res[cur].err, res[cur].isFalse = "", true
			} else {
				res[cur].err = m[1] + " " + m[2]
			}
			cur = -1
		}
	}
	for i := range res {
		if b.drop[i] || res[i].missing || res[i].err != "" || res[i].isFalse {
			continue
		}
		path := filepath.Join(out, fmt.Sprintf("t%d.itf.json", i))
		// by its type: the Rust backend writes integers of 16 characters or
		// more as bignumber.js objects, which itf reads by it
		name := fmt.Sprintf("v%d", i)
		tr, err := itf.ReadFileWith(path, &itf.Schema{Vars: map[string]*itf.Type{name: b.entries[i].t.itfType()}})
		if err != nil || len(tr.States) == 0 {
			res[i].missing = true
			continue
		}
		x, ok := tr.States[len(tr.States)-1].Vars[name]
		if !ok {
			res[i].missing = true
			continue
		}
		res[i].raw = fmt.Sprint(x)
		if v, err := FromITF(x); err == nil {
			res[i].val = v
		}
	}
	if err != nil && !strings.Contains(string(text), "passing") && !strings.Contains(string(text), "failed") {
		return nil, fmt.Errorf("quint test --backend %s: %v\n%s", be, err, firstLines(string(text), 20))
	}
	return res, nil
}

// itfType is t as itf reads a trace by it (nil: a regression's, given
// as text, not known).
func (t *gtyp) itfType() *itf.Type {
	switch t.k {
	case kInt:
		return &itf.Type{Kind: itf.Int}
	case kBool:
		return &itf.Type{Kind: itf.Bool}
	case kStr:
		return &itf.Type{Kind: itf.Str}
	case kVariant:
		return &itf.Type{Kind: itf.Variant, Fields: map[string]*itf.Type{"VA": {Kind: itf.Int}, "VB": {Kind: itf.Bool}, "VC": {Kind: itf.Tuple}}}
	case kSet:
		return &itf.Type{Kind: itf.Set, Elem: t.elem.itfType()}
	case kList:
		return &itf.Type{Kind: itf.List, Elem: t.elem.itfType()}
	case kMap:
		return &itf.Type{Kind: itf.Map, Elem: t.elem.itfType(), Value: t.val.itfType()}
	case kTup:
		out := &itf.Type{Kind: itf.Tuple}
		for _, x := range t.items {
			out.Elems = append(out.Elems, x.itfType())
		}
		return out
	case kRec:
		out := &itf.Type{Kind: itf.Record, Fields: map[string]*itf.Type{}}
		for i, x := range t.items {
			out.Fields[t.names[i]] = x.itfType()
		}
		return out
	}
	return nil
}

// ---- judging ------------------------------------------------------------

// A disagreement is the evaluator's outcome of an entry against one
// backend's (or, for an action, against its own successors' expression).
type disagreement struct {
	what    string
	backend string
	q, b    goutcome
}

// judge is how entry i's outcomes disagree, if they do; ok false when the
// evaluator refused (no verdict to compare).
func judge(b *gbatch, q []goutcome, qs map[string][]goutcome, i int, be string) (d *disagreement, answered bool) {
	e, qo := b.entries[i], q[i]
	if qo.refused != "" {
		return nil, false
	}
	bo := qs[be][i]
	if bo.missing {
		return &disagreement{"missing", be, qo, bo}, true
	}
	if e.act == nil {
		switch {
		case qo.err != "" && bo.err == "":
			return &disagreement{"error/value", be, qo, bo}, true
		case qo.err == "" && bo.err != "":
			return &disagreement{"value/error", be, qo, bo}, true
		case qo.err == "" && (bo.val == nil || Compare(qo.val, bo.val) != 0):
			return &disagreement{"value/value", be, qo, bo}, true
		}
		return nil, true
	}
	if qo.err != "" {
		return nil, false // a choice's value may fail where quint's draw does not
	}
	switch {
	case bo.err != "":
		return &disagreement{"act/error", be, qo, bo}, true
	case bo.isFalse:
		if len(qo.vals) > 0 && !qo.partial {
			return &disagreement{"act/disabled", be, qo, bo}, true
		}
	case bo.val == nil || !(&Set{qo.vals}).has(bo.val):
		return &disagreement{"act/member", be, qo, bo}, true
	}
	if so := q[e.succ]; so.refused == "" && so.err == "" && Compare(&Set{qo.vals}, so.val) != 0 {
		return &disagreement{"act/successors", "", qo, so}, true
	}
	return nil, true
}

type tally struct {
	entries, refused, answered, illTyped, disagreements int
	// why: the refusals by operator (what the generator spends on the
	// fragment's edge)
	why map[string]int
}

func (t *tally) add(u tally) {
	t.entries += u.entries
	t.refused += u.refused
	t.answered += u.answered
	t.illTyped += u.illTyped
	t.disagreements += u.disagreements
	for k, n := range u.why {
		if t.why == nil {
			t.why = map[string]int{}
		}
		t.why[k] += n
	}
}

func (t tally) String() string {
	var why []string
	for _, k := range slices.SortedFunc(maps.Keys(t.why), func(a, b string) int { return cmp.Or(t.why[b]-t.why[a], strings.Compare(a, b)) }) {
		why = append(why, fmt.Sprintf("%s %d", k, t.why[k]))
	}
	return fmt.Sprintf("%d entries, %d answered by the evaluator and judged by both backends, %d refused (%s), %d disagreements, %d ill-typed",
		t.entries, t.answered, t.refused, strings.Join(why, ", "), t.disagreements, t.illTyped)
}

// differ runs entries as a batch and fails t on each disagreement,
// shrunk.
func differ(t *testing.T, entries []gentry, seed uint64, rerun string) tally {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	b := &gbatch{entries: entries, drop: map[int]bool{}}
	var tl tally
	q, qs, err := b.run(ctx, keepDir(t, "batch"), seed, backends, func(e gentry, msg string) {
		tl.illTyped++
		t.Errorf("the generator's gentry is ill-typed (a bug of the generator's: %s): %s\n  %s", rerun, msg, e.root())
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range entries {
		if b.drop[i] {
			continue
		}
		tl.entries++
		answered := false
		for _, be := range backends {
			d, ok := judge(b, q, qs, i, be)
			answered = answered || ok
			if d == nil {
				continue
			}
			tl.disagreements++
			small, sd := entries[i], *d
			if os.Getenv("GOTOGETHER_QEVAL_FUZZ_SHRINK") != "0" {
				small, sd = shrink(ctx, t, entries[i], *d)
			}
			t.Errorf("the evaluator and quint (%s) disagree (%s; %s):\n  %s\n  the evaluator: %s\n  quint: %s\n  as generated: %s",
				cmp.Or(sd.backend, "its own successors"), sd.what, rerun, small.root(), sd.q, sd.b, entries[i].root())
			break
		}
		if answered {
			tl.answered++
		} else {
			tl.refused++
			if tl.why == nil {
				tl.why = map[string]int{}
			}
			why := q[i].refused
			if why == "" { // judge's: an action the evaluator fails, which quint's draw may not
				why = "action: fails (" + q[i].err + ")"
			}
			op, _, _ := strings.Cut(why, ":")
			tl.why[op]++
			if os.Getenv("GOTOGETHER_VERBOSE") == "1" {
				t.Logf("no verdict: %s\n  %s", why, entries[i].root())
			}
		}
	}
	return tl
}

// keepDir is a batch's directory: under GOTOGETHER_KEEP if set (the
// module, quint's IR and each backend's ITF), else the test's.
func keepDir(t *testing.T, what string) string {
	k := os.Getenv("GOTOGETHER_KEEP")
	if k == "" {
		return t.TempDir()
	}
	d, err := os.MkdirTemp(k, "qeval-"+strings.ReplaceAll(t.Name(), "/", "-")+"-"+what+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("kept in %s", d)
	return d
}

// shrink is the smallest entry reductions reach that still disagrees as
// d does, one batch of candidates a round.
func shrink(ctx context.Context, t *testing.T, e gentry, d disagreement) (gentry, disagreement) {
	bes := []string{d.backend}
	if d.backend == "" {
		bes = backends[:1]
	}
	for range 40 {
		cands := reductions(e.root())
		slices.SortStableFunc(cands, func(a, b *gnode) int { return a.size() - b.size() })
		// the smallest first, up to a module quint compiles in seconds
		text := 0
		for k, c := range cands {
			if text += len(c.String()); text > 64<<10 || k == 150 {
				cands = cands[:k]
				break
			}
		}
		var entries []gentry
		var at []int
		for _, c := range cands {
			if e.act != nil {
				at = append(at, len(entries))
				entries = append(entries, actionPair(len(entries), e.t, c, e.v, e.from)...)
			} else {
				at = append(at, len(entries))
				entries = append(entries, gentry{t: e.t, expr: c})
			}
		}
		if len(entries) == 0 {
			break
		}
		b := &gbatch{entries: entries, drop: map[int]bool{}}
		q, qs, err := b.run(ctx, keepDir(t, "shrink"), 1, bes, func(gentry, string) {})
		if err != nil {
			t.Logf("shrinking stopped: %v", err)
			break
		}
		found := false
		for _, i := range at {
			if b.drop[i] {
				continue
			}
			nd, _ := judge(b, q, qs, i, bes[0])
			if nd != nil && nd.what == d.what {
				e, d, found = entries[i], *nd, true
				break
			}
		}
		if !found {
			break
		}
	}
	return e, d
}
