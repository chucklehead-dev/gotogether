package qoracle

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// envInt is a GOTOGETHER_QFRONT_* count, or def.
func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		t.Fatalf("%s=%s: want a count", name, s)
	}
	return n
}

// seeds are the generated sample's: GOTOGETHER_QFRONT_SEED=s alone, else
// GOTOGETHER_QFRONT_SAMPLE of them (def unset) from GOTOGETHER_SEED_OFFSET+1
// (the nightly's, by date).
func seeds(t *testing.T, def int) []uint64 {
	if s := envInt(t, "GOTOGETHER_QFRONT_SEED", 0); s > 0 {
		return []uint64{uint64(s)}
	}
	n, off := envInt(t, "GOTOGETHER_QFRONT_SAMPLE", def), envInt(t, "GOTOGETHER_SEED_OFFSET", 0)
	var out []uint64
	for i := range n {
		out = append(out, uint64(off+i+1))
	}
	return out
}

// The tree shrink, on a predicate rather than quint: the program shrinks
// while it still holds, and ends where no reduction holds (1-minimal for
// the reductions), smaller than it began.
func TestShrinkModule(t *testing.T) {
	for seed := range uint64(20) {
		p := Module(seed)
		holds := func(q *GenProgram) bool {
			return strings.Contains(q.Files["main.qnt"].Text(), "action step") && strings.Contains(q.Files["base.qnt"].Text(), "type S")
		}
		s, tried := ShrinkModule(p, holds, 100000)
		if !holds(s) || s.Size() >= p.Size() {
			t.Fatalf("seed %d: shrank from %d to %d (%d tried), holds %v", seed, p.Size(), s.Size(), tried, holds(s))
		}
		for _, c := range reductions(s) {
			if holds(c) {
				t.Fatalf("seed %d: a reduction of the result still holds: not minimal", seed)
			}
		}
	}
}

func runQuint(t *testing.T, p *GenProgram, id string) *Output {
	t.Helper()
	dir := t.TempDir()
	prog, err := p.Write(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	o, err := Quint{}.Run(context.Background(), prog, t.TempDir())
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return o
}

// The module generator's programs are well typed, quint judging: one it
// refuses is the generator's bug, named by its seed. Push tier: one seed.
func TestModuleWellTyped(t *testing.T) {
	requireQuint(t)
	for _, s := range seeds(t, 1) {
		t.Run(fmt.Sprint(s), func(t *testing.T) {
			t.Parallel()
			p := Module(s)
			o := runQuint(t, p, p.String())
			v, err := VerdictOf(o)
			if err != nil {
				t.Fatal(err)
			}
			if len(v.Typecheck) > 0 || len(v.Compile) > 0 || o.Compile == nil {
				errs, _ := Errors(o.Typecheck, 1, o.Root)
				t.Fatalf("quint refuses generated program %s (GOTOGETHER_QFRONT_SEED=%d): %v %v\n%s\n%s", p, s, v, errs, p.Files["main.qnt"].Text(), p.Files["base.qnt"].Text())
			}
		})
	}
}

// The ill-typed generator reaches every front-end code, quint judging
// (exit criterion 4): each mutation of the catalog on one program, every
// code of FrontEndCodes among quint's answers. Each mutation's tag is a
// hint, logged beside quint's answer. The nightly's (its 37 programs cost
// about 60 CPU s); per push, GOTOGETHER_QFRONT_ILL=1 runs it.
func TestIllReachesEveryCode(t *testing.T) {
	if os.Getenv("GOTOGETHER_QFRONT_ILL") != "1" && os.Getenv("GOTOGETHER_QFRONT_CORPUS") != "1" {
		t.Skip("the nightly's (GOTOGETHER_QFRONT_ILL=1)")
	}
	requireQuint(t)
	seed := seeds(t, 1)[0]
	var (
		mu      sync.Mutex
		reached = map[string][]string{}
		wg      sync.WaitGroup
	)
	for i := range Mutations() {
		wg.Go(func() {
			p := Ill(seed, i)
			o := runQuint(t, p, p.String())
			v, err := VerdictOf(o)
			if err != nil {
				t.Error(err)
				return
			}
			codes := append(slices.Clone(v.Typecheck), v.Compile...)
			if pe, err := Errors(o.Parse, 0, o.Root); err == nil {
				codes = append(codes, Codes(pe)...)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, c := range codes {
				reached[c] = append(reached[c], p.Mutation)
			}
			mark := ""
			if !slices.Contains(codes, p.Tag) {
				mark = " (not its tag)"
			}
			t.Logf("%-22s tag %s, quint %v%s", p.Mutation, p.Tag, codes, mark)
			if len(codes) == 0 {
				t.Errorf("%s: quint accepts it: a mutation that makes nothing ill", p)
			}
		})
	}
	wg.Wait()
	for _, c := range FrontEndCodes {
		if len(reached[c]) == 0 {
			t.Errorf("no generated program reaches %s", c)
		}
	}
}

// The generated sample through the harness (the nightly's, and a push
// tier of one well-typed and one ill program): quint against the stub,
// a disagreement shrunk by the program's tree.
func TestGeneratedHarness(t *testing.T) {
	requireQuint(t)
	fe := stub(t)
	sample := seeds(t, 1)
	for _, s := range sample {
		progs := []*GenProgram{Module(s)}
		// per seed, three of the mutations in turn (the push tier one);
		// a seed rerun alone, every one
		ills, from := 3, int(s)*3
		if len(sample) == 1 {
			ills = 1
		}
		if os.Getenv("GOTOGETHER_QFRONT_SEED") != "" {
			ills, from = Mutations(), 0
		}
		for i := range ills {
			progs = append(progs, Ill(s, from+i))
		}
		for _, p := range progs {
			t.Run(strings.ReplaceAll(p.String(), " ", "_"), func(t *testing.T) {
				t.Parallel()
				compare := func(q *GenProgram) *Disagreement {
					prog, err := q.Write(t.TempDir(), q.String())
					if err != nil {
						t.Fatal(err)
					}
					d, err := Compare(context.Background(), prog, Quint{}, fe, t.TempDir())
					if err != nil {
						t.Fatalf("%s: %v", q, err)
					}
					return d
				}
				d := compare(p)
				if d == nil {
					return
				}
				small, tried := ShrinkModule(p, func(q *GenProgram) bool { return Same(d, compare(q)) }, 300)
				t.Fatalf("quint and %s disagree on %s (GOTOGETHER_QFRONT_SEED=%d): %v\nshrunk (%d tried) to:\n%s", fe.Name(), p, p.Seed, d, tried, small.Files[small.Main].Text())
			})
		}
	}
}
