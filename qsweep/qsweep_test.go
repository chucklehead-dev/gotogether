package qsweep_test

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qsweep"
)

func threes(n int) []qsweep.Factor {
	var fs []qsweep.Factor
	for i := range n {
		fs = append(fs, qsweep.Factor{Name: string(rune('a' + i)), Values: []string{"0", "1", "2"}})
	}
	return fs
}

// Pairwise rows of four 3-valued factors cover every pair in close to the
// optimum 9 rows of the 81; three-way rows cover every triple.
func TestCover(t *testing.T) {
	fs := threes(4)
	for _, c := range []struct{ t, most int }{{2, 11}, {3, 40}} {
		rows, err := qsweep.Cover(fs, c.t, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > c.most {
			t.Errorf("t=%d: %d rows, want at most %d", c.t, len(rows), c.most)
		}
		if miss, _ := qsweep.Missing(fs, c.t, nil, rows); len(miss) > 0 {
			t.Errorf("t=%d: missing %v", c.t, miss)
		}
		t.Logf("t=%d: %d rows of 81", c.t, len(rows))
	}
	// deterministic
	a, _ := qsweep.Cover(fs, 2, nil)
	b, _ := qsweep.Cover(fs, 2, nil)
	for i := range a {
		if a[i].String() != b[i].String() {
			t.Fatal("two generations differ")
		}
	}
}

// A constraint rules rows out, and the pairs only those rows had are not
// required; every row returned is allowed.
func TestCoverConstraint(t *testing.T) {
	fs := []qsweep.Factor{qsweep.Ints("retries", 0, 1, 3), qsweep.Bool("budget"), qsweep.Factor{Name: "iso", Values: []string{"ser", "si", "rc"}}}
	allow := func(r qsweep.Row) bool { return r.Int("retries") > 0 || !r.On("budget") }
	rows, err := qsweep.Cover(fs, 2, allow)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if !allow(r) {
			t.Errorf("row %s is not allowed", r)
		}
	}
	if miss, _ := qsweep.Missing(fs, 2, allow, rows); len(miss) > 0 {
		t.Errorf("missing %v", miss)
	}
	// rows chosen by hand: Missing names what they leave out
	miss, _ := qsweep.Missing(fs, 2, allow, rows[:1])
	if len(miss) == 0 || strings.Contains(strings.Join(miss, " "), "budget=on,retries=0") {
		t.Errorf("missing from one row: %v", miss)
	}
	if _, err := qsweep.Cover(fs, 2, func(qsweep.Row) bool { return false }); err == nil {
		t.Error("no allowed row, no error")
	}
}

// Every ordered pair, and every ordered triple, of events appears as a
// subsequence of some order.
func TestSequences(t *testing.T) {
	ev := []string{"crash", "lose", "dup", "delay", "partition"}
	for _, k := range []int{2, 3} {
		seqs := qsweep.Sequences(ev, k)
		pos := func(seq []string, e string) int {
			for i, x := range seq {
				if x == e {
					return i
				}
			}
			return -1
		}
		var check func(prefix []string)
		check = func(prefix []string) {
			if len(prefix) == k {
				for _, s := range seqs {
					ok := true
					for i := 1; i < k; i++ {
						ok = ok && pos(s, prefix[i-1]) < pos(s, prefix[i])
					}
					if ok {
						return
					}
				}
				t.Errorf("t=%d: no order has %v", k, prefix)
				return
			}
			for _, e := range ev {
				if pos(prefix, e) < 0 {
					check(append(append([]string(nil), prefix...), e))
				}
			}
		}
		check(nil)
		t.Logf("t=%d: %d orders of %d events", k, len(seqs), len(ev))
	}
}

// A factor naming its model constant gives each row's constants.
func TestRowConstants(t *testing.T) {
	rows, err := qsweep.Cover([]qsweep.Factor{qsweep.Ints("stock", 1, 2).Constant("STOCK"), qsweep.Bool("crash")}, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		cs := r.Constants()
		if len(cs) != 1 || cs["STOCK"] != r.Get("stock") {
			t.Errorf("%v: constants %v", r, cs)
		}
	}
}

// A sweep's rows are its subtests' names: the same factors give the same
// rows, in the same words, from one version to the next (a CI failure's
// subtest name reruns it); FaultsOff names a row's kinds that are off,
// and Missing lists what rows leave out in order.
func TestRowsStayTheSame(t *testing.T) {
	fs := []qsweep.Factor{qsweep.Ints("retries", 0, 1, 3), qsweep.FaultKind("rpc.pay"), {Name: "iso", Values: []string{"ser", "si"}}}
	rows, err := qsweep.Cover(fs, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, fmt.Sprint(r, r.FaultsOff()))
	}
	want := []string{
		"retries=0,rpc.pay=off,iso=ser [rpc.pay]", "retries=0,rpc.pay=on,iso=si []",
		"retries=1,rpc.pay=off,iso=si [rpc.pay]", "retries=1,rpc.pay=on,iso=ser []",
		"retries=3,rpc.pay=off,iso=ser [rpc.pay]", "retries=3,rpc.pay=on,iso=si []",
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows:\n got %q\nwant %q", got, want)
	}
	if got := fmt.Sprint(qsweep.Sequences([]string{"a", "b", "c"}, 2)); got != "[[a b c] [c b a]]" {
		t.Errorf("sequences: %s", got)
	}
	if got, want := fmt.Sprint(qsweep.Sequences([]string{"a", "b", "c", "d"}, 3)), "[[a c b d] [d c a b] [b c a d] [a d b c] [c d b a] [b d a c]]"; got != want {
		t.Errorf("3-way sequences: %s, want %s", got, want)
	}
	miss, err := qsweep.Missing(fs, 2, nil, rows[:2])
	if err != nil {
		t.Fatal(err)
	}
	if !slices.IsSorted(miss) || len(miss) != 10 || miss[0] != "retries=1,iso=ser" {
		t.Errorf("missing from two rows: %q, want 10, sorted", miss)
	}
}

// On small random spaces with random constraints, Cover's rows are
// allowed and cover every t-way combination some allowed row has, as an
// enumeration of the whole space says (Missing asks Cover, so it cannot
// check Cover).
func TestCoverAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for trial := range 300 {
		var fs []qsweep.Factor
		for i := range 2 + r.IntN(4) {
			var vs []string
			for v := range 1 + r.IntN(4) {
				vs = append(vs, fmt.Sprint(v))
			}
			fs = append(fs, qsweep.Factor{Name: string(rune('a' + i)), Values: vs})
		}
		// a row's text, as Row.String writes it, and a constraint on it
		// admitting about three rows in four
		text := func(vals []string) string {
			var parts []string
			for i, v := range vals {
				parts = append(parts, fs[i].Name+"="+v)
			}
			return strings.Join(parts, ",")
		}
		salt := r.Uint64()
		ok := func(s string) bool {
			h := salt
			for _, c := range s {
				h = h*31 + uint64(c)
			}
			return h%4 != 0
		}
		tw := min(1+r.IntN(3), len(fs))
		rows, err := qsweep.Cover(fs, tw, func(row qsweep.Row) bool { return ok(row.String()) })
		// the t-way combinations of one row's values
		tuples := func(vals []string, add func(string)) {
			var walk func(start int, chosen []string)
			walk = func(start int, chosen []string) {
				if len(chosen) == tw {
					add(strings.Join(chosen, ","))
					return
				}
				for i := start; i < len(fs); i++ {
					walk(i+1, append(chosen, fs[i].Name+"="+vals[i]))
				}
			}
			walk(0, nil)
		}
		need := map[string]bool{}
		var space func(i int, vals []string)
		space = func(i int, vals []string) {
			if i == len(fs) {
				if ok(text(vals)) {
					tuples(vals, func(s string) { need[s] = true })
				}
				return
			}
			for _, v := range fs[i].Values {
				space(i+1, append(vals, v))
			}
		}
		space(0, nil)
		if len(need) == 0 {
			if err == nil {
				t.Fatalf("trial %d: no row is allowed, and Cover returned %v", trial, rows)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		for _, row := range rows {
			if !ok(row.String()) {
				t.Fatalf("trial %d: row %s is not allowed", trial, row)
			}
			var vals []string
			for _, f := range fs {
				vals = append(vals, row.Get(f.Name))
			}
			// each row covers a combination no row before it did: Cover adds
			// none it does not need (all allowed rows would cover too)
			fresh := false
			tuples(vals, func(s string) {
				if need[s] {
					fresh = true
				}
				delete(need, s)
			})
			if !fresh {
				t.Fatalf("trial %d: row %s covers nothing the rows before it did not", trial, row)
			}
		}
		for tuple := range need {
			t.Fatalf("trial %d: t=%d: no row of %d covers %s", trial, tw, len(rows), tuple)
		}
	}
}

// MaxSpace bounds the space Cover enumerates, inclusive: 21 two-valued
// factors are allowed, 22 are not.
func TestMaxSpace(t *testing.T) {
	var fs []qsweep.Factor
	for i := range 22 {
		fs = append(fs, qsweep.Bool(fmt.Sprint("f", i)))
	}
	if 1<<21 != qsweep.MaxSpace {
		t.Fatalf("MaxSpace is %d: change the factors", qsweep.MaxSpace)
	}
	first := func(r qsweep.Row) bool { return !r.On("f0") && !r.On("f1") && !r.On("f20") && r.FaultsOff() == nil }
	if _, err := qsweep.Cover(fs[:21], 1, first); err != nil {
		t.Errorf("a space of MaxSpace rows: %v", err)
	}
	if _, err := qsweep.Cover(fs, 1, first); err == nil {
		t.Error("a space of twice MaxSpace rows: no error")
	}
}
