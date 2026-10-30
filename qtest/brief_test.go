package qtest

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/validate/navproto"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A summary says who judged the passes in a word ("all conform (quint
// confirmed 3)"), the evaluator's breakdown under GOTOGETHER_VERBOSE=1:
// "passes judged by the Go evaluator 30, 2 of them also by quint" was the
// implementer's sentence on every line a newcomer read.
func TestSummaryInTheUsersWords(t *testing.T) {
	RequireQuint(t)
	t.Setenv("GOTOGETHER_EVAL_SAMPLE", "1") // quint judges every pass too
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		qobs.Record(s.Context(), "deposit", nil, "amount", 10, "obs.balance", 10)
		return s.Run(0)
	}}
	log := &logTB{TB: t}
	sys.Explore(t, ExploreConfig{Seeds: 3}).Expect(log, Conforms())
	if !strings.Contains(log.logs, "#1: 3 runs, all conform (quint confirmed 3); ") || strings.Contains(log.logs, "passes judged") {
		t.Errorf("summary:\n%s", log.logs)
	}
	t.Setenv("GOTOGETHER_VERBOSE", "1")
	log = &logTB{TB: t}
	sys.Explore(t, ExploreConfig{Seeds: 3}).Expect(log, Conforms())
	if !strings.Contains(log.logs, "#2: 3 runs, all conform; passes judged by the Go evaluator 3, 3 of them also by quint") {
		t.Errorf("verbose summary:\n%s", log.logs)
	}
}

// Runs rejected at their first step took no model action, and the
// summary says why rather than "3 reject ship; model actions taken 0 of
// 2", which read as a contradiction.
func TestSummaryRejectedAtTheFirstStep(t *testing.T) {
	rejected := func(i int) *Run {
		return &Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Fail,
			Failure: &validate.Failure{Kind: "disabled", Index: i, Step: &qtrace.Step{Action: "ship"}}}}}}
	}
	m := &binding.Model{Actions: map[string][]binding.Param{"init": nil, "step": nil, "ship": nil, "pay": nil}}
	x := &Exploration{Runs: []*Run{rejected(0), rejected(0)}, steps: stepActions(m, nil), given: true}
	if s, want := x.summary(false), "2 runs, 0 conform, 2 reject ship; every run rejected at its first step, ship, so no model action taken; fault"; !strings.Contains(s, want) {
		t.Errorf("summary %q: want %q", s, want)
	}
	x.Runs[1] = rejected(1) // one got further
	if s := x.summary(false); !strings.Contains(s, "model actions taken 0 of 2") {
		t.Errorf("summary %q: want the count", s)
	}
}

// An action only the binding's actions call (a grist model's appends,
// every command's part) is no step a run can take: it is not counted nor
// listed as never taken, where a dispatcher's own step still is.
func TestSummaryLeavesOutParts(t *testing.T) {
	RequireQuint(t)
	m, err := binding.LoadModel("quint", "../binding/testdata/parts.qnt", "parts")
	if err != nil {
		t.Fatal(err)
	}
	b := &binding.Binding{Actions: map[string]binding.Action{"add": {}, "bump": {}, "idle": {}}}
	got := stepActions(m, b)
	if len(got) != 4 || !got["add"] || !got["bump"] || !got["idle"] || !got["dispatch"] {
		t.Errorf("step actions %v: want add, bump, idle and dispatch (appendTo a part, same a frame)", got)
	}
	if all := stepActions(m, nil); !all["appendTo"] {
		t.Errorf("with no binding, appendTo is no part: %v", all)
	}
}

// A Record whose claims hold says so in a line, as an exploration does:
// a passing Record was silent, and a newcomer suspected nothing had been
// checked. An expected failure is its catch, in a line.
func TestRecordLogsItsVerdict(t *testing.T) {
	RequireQuint(t)
	rec := func(ops ...any) func(*qobs.Recorder) {
		return func(r *qobs.Recorder) {
			for i := 0; i+2 < len(ops); i += 3 {
				r.Record(context.Background(), ops[i].(string), nil, "amount", ops[i+1], "obs.balance", ops[i+2])
			}
		}
	}
	b := "../validate/testdata/account.yaml"
	log := &logTB{TB: t}
	Record(t, b, rec("deposit", 5, 5, "withdraw", 2, 3)).Expect(log, Conforms())
	if want := "gotogether: the run conforms: 2 steps; invariants held: "; !strings.HasPrefix(log.logs, want) {
		t.Errorf("logs %q, want %q", log.logs, want)
	}
	log = &logTB{TB: t}
	Record(t, b, rec("deposit", 5, 5, "withdraw", 9, -4)).Expect(log, Rejects("withdraw"))
	if want := "gotogether: as expected, a withdraw step the model cannot take: step 2, withdraw(amount=9): the model's withdraw(amount = 9) is disabled: in withdraw: "; !strings.HasPrefix(log.logs, want) {
		t.Errorf("logs %q, want %q", log.logs, want)
	}
}

// A client that starts before its server listens has every request
// refused and records no step: its report says so, in qhttp's words
// (hooks.Notes), with no faults said as "no faults", not as "a
// reconstruction issue", "faults []" and "a deadlock", the three wrong
// leads a newcomer followed.
func TestRefusedRunSaysSo(t *testing.T) {
	RequireQuint(t)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: refusedBank}
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
		defer close(done)
		sys.Explore(f, ExploreConfig{Seeds: 8}).Expect(f, Conforms())
	}()
	<-done
	msg := strings.Join(f.errs, "\n")
	for _, want := range []string{
		"(of the runs that fail: ", " record no step)",
		", no faults\n",
		"no step was recorded; the run's doubles saw a request to bank refused, nothing listening (once; the first: POST /deposit at 1ms: its process had not called Handle yet: start the server's process before its clients, or have them retry a refused connection)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the failure lacks %q:\n%s", want, msg)
		}
	}
	for _, wrong := range []string{"reconstruction issue", "faults []", "a deadlock"} {
		if strings.Contains(msg, wrong) {
			t.Errorf("the failure says %q:\n%s", wrong, msg)
		}
	}
}

// refusedBank is a bank behind qhttp whose server, in some runs, calls
// Handle only after its client's request: that client is refused and
// records no step.
func refusedBank(c qenv.Chooser) error {
	late := c.Choose("test.serverLate", 2) == 1 // some runs start the server after its client's request
	s := qenv.New(c)
	defer s.Close()
	var srv *qhttp.Server
	p := s.Process("bank", false, func(ctx context.Context) {
		if late {
			qenv.Sleep(ctx, time.Second)
		}
		srv.Handle(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			qobs.Record(r.Context(), "deposit", nil, "amount", 10, "obs.balance", 10)
		}))
	})
	srv = qhttp.NewServer(s, "bank", p)
	client := &http.Client{Transport: qhttp.NewTransport(srv)}
	s.Spawn("client", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL()+"/deposit", nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
		}
	})
	return s.Run(time.Minute)
}

// The run navigator at "fail" of a run that recorded no step opens its
// last choice and says so, with what its doubles saw, not a node that
// reads pass.
func TestNavRefused(t *testing.T) {
	RequireQuint(t)
	out := filepath.Join(t.TempDir(), "node.json")
	t.Setenv("GOTOGETHER_RUN", "TestNavRefused#1:seed=1")
	t.Setenv("GOTOGETHER_NAV", "fail")
	t.Setenv("GOTOGETHER_NAV_OUT", out)
	System{Binding: "../validate/testdata/account.yaml", Run: refusedBank}.Explore(t, ExploreConfig{Seeds: 8}).Expect(t, Conforms())
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var n navproto.Node
	if err := jsonUnmarshal(b, &n); err != nil {
		t.Fatal(err)
	}
	if n.Receipt.Reason != "no step recorded" || !slices.ContainsFunc(n.Notes, func(s string) bool { return strings.Contains(s, "a request to bank refused, nothing listening") }) {
		t.Errorf("the node: verdict %q, notes %q", n.Receipt.Reason, n.Notes)
	}
}

// A run node's title says its choices in words before its schedule id:
// "node 0:1,1:1,2:1,3:1@4" meant nothing to a newcomer.
func TestPathWords(t *testing.T) {
	path := []pathChoice{{kind: "sched.task", name: "client", value: 1}, {kind: "sched.task", name: "client", value: 1},
		{kind: "fault.rpc.counter.request", name: "duplicate", value: 2}, {kind: "fault.rpc.counter.response", name: "nominal"},
		{kind: "sched.task", name: "counter.main"}, {kind: "sched.task", name: "client", value: 1}}
	if got, want := pathWords(path), "after 6 choices: client ran 3 times, counter.main ran once, rpc.counter.request: duplicate"; got != want {
		t.Errorf("pathWords = %q, want %q", got, want)
	}
	if got := pathWords(nil); got != "at the start" {
		t.Errorf("no choices: %q", got)
	}
}
