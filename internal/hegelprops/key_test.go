package hegelprops

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"hegel.dev/go/hegel"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// verdictOf is what a report decides: each actor's conformance and every
// invariant's, eventual val's and bounded-response obligation's verdict.
func verdictOf(rep *validate.Report) string {
	var out []string
	for _, a := range rep.Actors {
		out = append(out, fmt.Sprintf("%s conforms %s", a.Actor, a.Conforms))
		for _, inv := range a.Invariants {
			out = append(out, fmt.Sprintf("%s %s %s", a.Actor, inv.Name, inv.Verdict))
		}
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

// Equal keys, equal verdicts: a run and another made from it by changes
// a key may or may not see (every time shifted, one step's time moved, a
// recorder renamed, a settle added or dropped, an action replaced), when
// Checker.Key says they are the same, are judged the same. A key that
// left out what a verdict reads (a within's times once) gave a run the
// cached verdict of another. The bounded-response model's binding reads
// times and settles both.
func TestEqualKeysEqualVerdicts(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	c, err := validate.NewChecker("../../validate/testdata/within.yaml", validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	actions := []string{"crash", "heal", "elect", "tick", "depose", "recover"}
	at := func(s int) time.Time { return time.Unix(1000, 0).Add(time.Duration(s) * time.Second) }
	same, differ := 0, 0
	hegel.Test(t, func(ht *hegel.T) {
		var a []qtrace.Step
		now := 0
		near := hegel.Draw(ht, hegel.Booleans())
		if near {
			// a heal answered near the bound, where a time decides
			h := hegel.Draw(ht, hegel.Integers(0, 2))
			now = h + hegel.Draw(ht, hegel.Integers(1, 5))
			a = []qtrace.Step{{Action: "crash", Time: at(0)}, {Action: "heal", Time: at(h)}, {Action: "elect", Time: at(now)}}
			for i := range a {
				a[i].Recorder, a[i].Seq = "n", uint64(i+1)
			}
		}
		for range hegel.Draw(ht, hegel.Integers(1, 6)) {
			now += hegel.Draw(ht, hegel.Integers(0, 3))
			action := actions[hegel.Draw(ht, hegel.Integers(0, len(actions)-1))]
			if near {
				action = "tick" // the run conforms: its within is judged
			}
			a = append(a, qtrace.Step{Action: action, Recorder: "n", Seq: uint64(len(a) + 1), Time: at(now)})
		}
		if hegel.Draw(ht, hegel.Booleans()) {
			a = append(a, qtrace.Step{Action: "settled", Settled: true, Recorder: "n", Seq: uint64(len(a) + 1), Time: at(now)})
		}
		b := append([]qtrace.Step(nil), a...)
		i := hegel.Draw(ht, hegel.Integers(0, len(b)-1))
		switch change := hegel.Draw(ht, hegel.Integers(0, 4)); change {
		case 0: // every time shifted: the same run, started later
			d := time.Duration(hegel.Draw(ht, hegel.Integers(1, 100))) * time.Second
			for j := range b {
				b[j].Time = b[j].Time.Add(d)
			}
		case 1: // one step's time moved, the order kept (near the bound, the answer's)
			if near {
				i = 2
			}
			b[i].Time = b[i].Time.Add(time.Duration(hegel.Draw(ht, hegel.Integers(1, 4))) * time.Second)
			for j := i + 1; j < len(b); j++ {
				if b[j].Time.Before(b[i].Time) {
					b[j].Time = b[i].Time
				}
			}
		case 2: // the recorder renamed
			for j := range b {
				b[j].Recorder = "m"
			}
		case 3: // a settle added, or dropped
			if last := b[len(b)-1]; last.Settled {
				b = b[:len(b)-1]
			} else {
				b = append(b, qtrace.Step{Action: "settled", Settled: true, Recorder: "n", Seq: last.Seq + 1, Time: last.Time})
			}
		case 4: // an action replaced
			if !b[i].Settled {
				b[i].Action = actions[hegel.Draw(ht, hegel.Integers(0, len(actions)-1))]
			}
		}
		if len(b) == 0 || c.Key(a) != c.Key(b) {
			differ++
			return
		}
		same++
		ra, err := c.Check(context.Background(), a)
		if err != nil {
			ht.Fatal(err)
		}
		rb, err := c.Check(context.Background(), b)
		if err != nil {
			ht.Fatal(err)
		}
		if va, vb := verdictOf(ra), verdictOf(rb); va != vb {
			ht.Fatalf("one key, two verdicts:\n  %v\n  %s\n  %v\n  %s", a, va, b, vb)
		}
	}, hegel.WithTestCases(60))
	t.Logf("%d pairs with one key judged, %d with two keys", same, differ)
	if same == 0 || differ == 0 {
		t.Errorf("the property needs pairs of both: %d with one key, %d with two", same, differ)
	}
}
