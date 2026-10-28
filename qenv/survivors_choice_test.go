package qenv_test

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qrun"
)

// survSeq is a chooser that answers its values in turn, then 0.
type survSeq struct{ vs []int }

func (s *survSeq) Choose(string, int) int {
	if len(s.vs) == 0 {
		return 0
	}
	v := s.vs[0]
	s.vs = s.vs[1:]
	return v
}

// survPanics reports whether f panics, and with what.
func survPanics(f func()) (msg string, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			msg, panicked = fmt.Sprint(r), true
		}
	}()
	f()
	return "", false
}

// The byte rule reads its stream a chunk at a time, and every chunk
// counts: with one byte per chunk, the third choice is the third byte, not
// nominal because the first chunk ran out. A fault choice past the end
// threshold is reported to OnFault (Hegel notes the faults it drew), and
// entropy is the stream's next bytes, zeros past its end.
func TestBytesRule(t *testing.T) {
	data := []byte{3, 5, 7}
	calls := 0
	b := &qenv.Bytes{Chunk: 1, Rates: qenv.DefaultRates, Next: func(n int) []byte {
		calls++
		n = min(n, len(data))
		out := data[:n]
		data = data[n:]
		return out
	}}
	var got []int
	for range 4 {
		got = append(got, b.Choose("schedule", 10))
	}
	if want := []int{3, 5, 7, 0}; !reflect.DeepEqual(got, want) {
		t.Errorf("one-byte chunks gave %v, want %v", got, want)
	}
	// the stream has ended: it is not asked again
	before := calls
	b.Choose("schedule", 10)
	if calls != before {
		t.Errorf("an ended stream was asked for more (%d calls, then %d)", before, calls)
	}

	// 256 options take one byte, 257 two
	b = qenv.FromBytes([]byte{200, 9, 1, 2}, qenv.DefaultRates)
	if v := b.Choose("schedule", 256); v != 200 {
		t.Errorf("256 options: %d, want 200 (one byte)", v)
	}
	if v := b.Choose("schedule", 10); v != 9 {
		t.Errorf("after 256 options: %d, want 9 (the next byte)", v)
	}
	if v := b.Choose("schedule", 1000); v != (1<<8|2)%1000 {
		t.Errorf("1000 options: %d, want %d (two bytes)", v, (1<<8|2)%1000)
	}

	var faults []string
	b = qenv.FromBytes([]byte{255, 0, 1, 2, 3}, qenv.DefaultRates)
	b.OnFault = func(kind string, v int) { faults = append(faults, fmt.Sprintf("%s=%d", kind, v)) }
	if v := b.Choose("fault.test.surv.bytes", 2); v != 1 {
		t.Errorf("byte 255 at rate 0.05: option %d, want 1", v)
	}
	if v := b.Choose("fault.test.surv.bytes", 2); v != 0 {
		t.Errorf("byte 0: option %d, want nominal", v)
	}
	if want := []string{"fault.test.surv.bytes=1"}; !reflect.DeepEqual(faults, want) {
		t.Errorf("OnFault told %v, want %v", faults, want)
	}
	// entropy is the stream's own (gosim's chunks), not the buffered choices'
	if e := qenv.FromBytes([]byte{1, 2, 3}, qenv.DefaultRates).Entropy(5); !bytes.Equal(e, []byte{1, 2, 3, 0, 0}) {
		t.Errorf("entropy %v, want the stream's bytes then zeros", e)
	}
}

// An encoded run replays its choices by the byte rule, a schedule choice
// of 256 options in one byte (two only above 256): a corpus entry encoded
// with the wrong width decodes as another run.
func TestEncodeChoicesWidths(t *testing.T) {
	recorded := []qenv.Choice{
		{Kind: "schedule", N: 256, Value: 200},
		{Kind: "schedule", N: 3, Value: 2},
		{Kind: "schedule", N: 300, Value: 299},
		{Kind: "fault.test.surv.enc", N: 3, Value: 2},
		{Kind: "fault.test.surv.enc", N: 3, Value: 0},
	}
	data, err := qenv.EncodeChoices(recorded, qenv.DefaultRates)
	if err != nil {
		t.Fatal(err)
	}
	if want := 4 + 1 + 1 + 2 + 2 + 1; len(data) != want {
		t.Errorf("encoded in %d bytes, want %d", len(data), want)
	}
	b := qenv.FromBytes(data, qenv.DefaultRates)
	for i, c := range recorded {
		if v := b.Choose(c.Kind, c.N); v != c.Value {
			t.Errorf("choice %d (%s/%d): replayed %d, recorded %d", i, c.Kind, c.N, v, c.Value)
		}
	}
}

// A replay that is asked for another choice than was recorded (another
// kind, another count, a fault where entropy was) stops and says where:
// the run is not deterministic, and replaying on would be another run.
func TestReplayDivergence(t *testing.T) {
	r := qenv.Replay([]qenv.Choice{{Kind: "schedule", N: 2, Value: 1}, {Kind: "schedule", N: 2, Value: 1}})
	if v := r.Choose("schedule", 3); v != 0 || r.Err() == nil || !strings.Contains(r.Err().Error(), "replay diverged at choice 0") {
		t.Errorf("another count: %d, err %v", v, r.Err())
	}
	if v := r.Choose("schedule", 2); v != 0 {
		t.Errorf("after the divergence: %d, want nominal", v)
	}

	r = qenv.Replay([]qenv.Choice{{Kind: "fault.test.surv.replay", N: 2, Value: 1}})
	e := qenv.EntropyOf(r)(4)
	if !bytes.Equal(e, make([]byte, 4)) || r.Err() == nil || !strings.Contains(r.Err().Error(), "asked 4 bytes of entropy") {
		t.Errorf("entropy where a fault was recorded: %v, err %v", e, r.Err())
	}
}

// A choice log names the fault options it records from their kind's
// declaration (the first option too), never a nominal or schedule choice;
// Faults are the non-nominal fault choices; Name names the last choice
// (the nemesis the simulation picked), and does nothing on an empty log.
func TestChoiceLogNames(t *testing.T) {
	qenv.DeclareFaults("test.surv.log", "unavailable", "lost")
	log := &qenv.ChoiceLog{C: &survSeq{vs: []int{1, 2, 0, 1}}}
	log.Name("nothing yet")
	log.Choose("fault.test.surv.log", 3)
	log.Choose("fault.test.surv.log", 3)
	log.Choose("fault.test.surv.log", 3)
	log.Choose("schedule", 2)
	var names []string
	for _, c := range log.Choices {
		names = append(names, c.Name)
	}
	if want := []string{"unavailable", "lost", "", ""}; !reflect.DeepEqual(names, want) {
		t.Errorf("names %q, want %q", names, want)
	}
	if f := log.Faults(); len(f) != 2 || f[0].Value != 1 || f[1].Value != 2 {
		t.Errorf("faults %v, want the two non-nominal fault choices", f)
	}
	log.Name("crash.writer")
	if n := log.Choices[len(log.Choices)-1].Name; n != "crash.writer" {
		t.Errorf("last choice named %q", n)
	}
}

// A kind written with the chooser's "fault." prefix matches nothing, so
// every place a user names one refuses it with the fix: a rate or a
// switch that silently did not apply looked like a run without the fault.
func TestFaultPrefixRefused(t *testing.T) {
	for name, f := range map[string]func(){
		"Rates.Set": func() { qenv.DefaultRates.Set("fault.rpc", 1) },
		"Rates key": func() { qenv.Rates{Kinds: map[string]float64{"fault.rpc": 1}}.Of("fault.rpc.x") },
		"Swarm":     func() { qenv.Swarm(qenv.NewSeeded(1), "fault.rpc") },
		"Without":   func() { qenv.Without(qenv.NewSeeded(1), "fault.rpc") },
	} {
		if msg, ok := survPanics(f); !ok || !strings.Contains(msg, `without the "fault." prefix ("rpc")`) {
			t.Errorf("%s: panicked %v: %s", name, ok, msg)
		}
	}
}

// A kind declared outside any run (a woven point's at init, a double built
// before its simulation) is known to every run, its options too; one
// declared in a run is that run's (its FaultNames, the kind and each
// option), not static; a name that covers nothing is unknown.
func TestDeclaredNamesAndStatic(t *testing.T) {
	qenv.DeclareFaults("test.surv.static", "boom")
	qenv.DeclareFaultSite("test.surv.sited", "pkg.F")

	f := &hooks.FaultNames{}
	end := qrun.Start(hooks.WithFaultNames(context.Background(), f))
	qenv.DeclareFaults("test.surv.inrun", "x")
	qenv.DeclareFaults("test.surv.inrunbare")
	end()

	for name, want := range map[string]bool{
		"test.surv.static":      true,
		"test.surv.static.boom": true,
		"test.surv.sited":       true,
		"test.surv.inrun":       false,
		"test.surv.inrun.x":     false,
		"test.surv.nothing":     false,
		"test.surv.static.bang": false,
	} {
		if got := hooks.KnownStatic(name); got != want {
			t.Errorf("KnownStatic(%s) = %v, want %v", name, got, want)
		}
	}
	for name, want := range map[string]bool{
		"test.surv.inrun":     true,
		"test.surv.inrun.x":   true,
		"test.surv.inrunbare": true,
		"test.surv.static":    false,
	} {
		if got := f.Covers(name); got != want {
			t.Errorf("the run's names cover %s: %v, want %v", name, got, want)
		}
	}
}

// A simulation's nemeses and fault points are its run's names, so a Rates
// key or a Script naming them is checked against what this run declared.
func TestSimDeclaresIntoItsRun(t *testing.T) {
	f := &hooks.FaultNames{}
	s := qenv.New(qenv.WithContext(qenv.NewSeeded(1), hooks.WithFaultNames(context.Background(), f)))
	s.Process("survwriter", true, func(ctx context.Context) { _ = qenv.FaultPoint(ctx, "test.surv.simpoint", false) })
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close()
	for _, name := range []string{"nemesis.crash.survwriter", "test.surv.simpoint", "test.surv.simpoint.injected"} {
		if !f.Covers(name) {
			t.Errorf("the run's names lack %s", name)
		}
	}
}

// A kind a simulation chose without declaring it is a kind all the same:
// a Script names it without being misnamed, and a misspelling of it is
// pointed at it. Among names as near, the first in order is suggested, so
// the hint does not change from run to run.
func TestScriptNamesChosenKinds(t *testing.T) {
	s := qenv.New(qenv.NewSeeded(1))
	s.Spawn("w", func(ctx context.Context) {
		s.FaultOf(qenv.FaultKind{Name: "test.surv.seenonly", Options: []string{"boom"}})
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close()
	qenv.DeclareFaults("test.surv.tie.a", "x")
	qenv.DeclareFaults("test.surv.tie.b", "x")

	sc := &qenv.Script{Faults: []qenv.Scripted{{Kind: "test.surv.seenonly", Option: 1}}}
	if m := sc.Misnamed(); len(m) != 0 {
		t.Errorf("a chosen kind is misnamed: %v", m)
	}
	sc = &qenv.Script{Faults: []qenv.Scripted{{Kind: "test.surv.seenonlx", Option: 1}}}
	if m := strings.Join(sc.Misnamed(), "\n"); !strings.Contains(m, "(did you mean test.surv.seenonly?)") {
		t.Errorf("a misspelt chosen kind: %s", m)
	}
	sc = &qenv.Script{Faults: []qenv.Scripted{{Kind: "test.surv.tie.c", Option: 1}}}
	for range 30 {
		if m := strings.Join(sc.Misnamed(), "\n"); !strings.Contains(m, "(did you mean test.surv.tie.a?)") {
			t.Fatalf("a tie: %s", m)
		}
	}
}

// A Script's "b follows a" holds for the script's first fault too (index
// 0 is a fault like any other): b waits for a, then happens; Misnamed
// does not call it a follow of no fault.
func TestScriptFollowsTheFirstFault(t *testing.T) {
	qenv.DeclareFaults("test.surv.first", "boom")
	qenv.DeclareFaults("test.surv.then", "boom")
	sc := &qenv.Script{
		Faults:   []qenv.Scripted{{Kind: "test.surv.first", Option: 1}, {Kind: "test.surv.then", Option: 1, Follows: []int{0}}},
		Progress: func() int { return 0 },
	}
	got := []int{
		sc.Choose("fault.test.surv.then", 2),
		sc.Choose("fault.test.surv.first", 2),
		sc.Choose("fault.test.surv.then", 2),
	}
	if want := []int{0, 1, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("choices %v, want %v", got, want)
	}
	if r := sc.Remaining(); len(r) != 0 {
		t.Errorf("remaining %v", r)
	}
	if m := sc.Misnamed(); len(m) != 0 {
		t.Errorf("misnamed %v", m)
	}
}

// A scripted fault whose option is no option of its kind (a Name it lacks,
// option 0) never happens and stays Remaining: marking it done would make
// a run that skipped it read as one that followed the trace.
func TestScriptUnmatchedOptionStaysRemaining(t *testing.T) {
	qenv.DeclareFaults("test.surv.unmatched", "unavailable")
	sc := &qenv.Script{
		Faults:   []qenv.Scripted{{Kind: "test.surv.unmatched", Name: "nosuch"}, {Kind: "test.surv.unmatched"}},
		Progress: func() int { return 0 },
	}
	for range 3 {
		if v := sc.Choose("fault.test.surv.unmatched", 2); v != 0 {
			t.Errorf("chose %d", v)
		}
	}
	if r := sc.Remaining(); len(r) != 2 {
		t.Errorf("remaining %v, want both", r)
	}
}

// A Script's entropy is its Base's, so a gosim run under a script draws
// the same bytes as without it; with no Base, zeros (the nominal schedule).
func TestScriptEntropy(t *testing.T) {
	want := qenv.NewSeeded(7).Entropy(16)
	if got := (&qenv.Script{Base: qenv.NewSeeded(7)}).Entropy(16); !bytes.Equal(got, want) {
		t.Errorf("entropy %v, want Base's %v", got, want)
	}
	if got := (&qenv.Script{}).Entropy(4); !bytes.Equal(got, make([]byte, 4)) {
		t.Errorf("entropy without Base %v", got)
	}
}

// PCT's Steps defaults to 1000 when none is given: change points fall in
// 1..Steps, and with 0 there would be nowhere to put them.
func TestPCTDefaultSteps(t *testing.T) {
	if p := qenv.NewPCT(qenv.NewSeeded(1), 1, 3, 0); p.Steps != 1000 || p.Depth != 3 {
		t.Errorf("steps %d, depth %d", p.Steps, p.Depth)
	}
	if p := qenv.NewPCT(qenv.NewSeeded(1), 1, 0, 50); p.Steps != 50 || p.Depth != 1 {
		t.Errorf("steps %d, depth %d", p.Steps, p.Depth)
	}
}

// Coverage counts faults as the options past nominal, sorts its points by
// kind, says which reached points were never faulted (not the unreached
// ones, which Unreached names), and prints each point on its line with its
// sites, then the pairs of faulted kinds never tried together.
func TestCoverageReport(t *testing.T) {
	if f := (&qenv.PointCoverage{Injected: []int{3, 1, 2}}).Faulted(); f != 3 {
		t.Errorf("faulted %d, want 3 (nominal not counted)", f)
	}
	// the runs' own sites (Sim.FaultSites): declared for the process, they
	// were every later test's unreached points
	sites := map[string][]string{"test.surv.cov.unreached": {"pkg.Unreached"}, "test.surv.cov.a": {"pkg.A"}}
	fc := func(k string, v int) qenv.Choice {
		return qenv.Choice{Kind: "fault.test.surv.cov." + k, N: 2, Value: v}
	}
	cov := qenv.CoverageWith([][]qenv.Choice{
		{fc("e", 0), fc("a", 1)},
		{fc("b", 1), fc("d", 0)},
		{fc("a", 1), fc("c", 1)},
	}, sites)
	if !sort.SliceIsSorted(cov.Points, func(i, j int) bool { return cov.Points[i].Kind < cov.Points[j].Kind }) {
		t.Errorf("points not sorted by kind")
	}
	var unfaulted []string
	for _, p := range cov.Unfaulted() {
		if strings.HasPrefix(p.Kind, "test.surv.cov.") {
			unfaulted = append(unfaulted, p.Kind)
		}
	}
	if want := []string{"test.surv.cov.d", "test.surv.cov.e"}; !reflect.DeepEqual(unfaulted, want) {
		t.Errorf("unfaulted %v, want %v", unfaulted, want)
	}

	out := cov.String()
	if !strings.HasPrefix(out, "fault coverage over 3 runs:\n") {
		t.Errorf("no header:\n%s", out)
	}
	lines := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			lines[f[0]] = l
		}
	}
	for kind, want := range map[string]string{
		"test.surv.cov.a":         "  test.surv.cov.a                  faulted       runs 2/3, reached 2, options [0 2] (pkg.A)",
		"test.surv.cov.d":         "  test.surv.cov.d                  never faulted runs 1/3, reached 1, options [1]",
		"test.surv.cov.unreached": "  test.surv.cov.unreached          UNREACHED     runs 0/3, reached 0, options [] (pkg.Unreached)",
	} {
		if lines[kind] != want {
			t.Errorf("line of %s:\n got %q\nwant %q", kind, lines[kind], want)
		}
	}
	for _, want := range []string{
		"\n  pairs of faulted kinds: 3, 2 never together, 1 in one order only\n" +
			"    never together: test.surv.cov.a + test.surv.cov.b\n" +
			"    never together: test.surv.cov.b + test.surv.cov.c\n",
	} {
		if !strings.HasSuffix(out, want) {
			t.Errorf("pairs: got\n%s\nwant suffix\n%s", out, want)
		}
	}
}
