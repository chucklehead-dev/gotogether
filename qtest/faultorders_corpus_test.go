package qtest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/validate/navproto"
)

func TestFaultOrdersCorpusChild(t *testing.T) {
	mode := os.Getenv("GO_WANT_ORDER_CORPUS")
	if mode == "" {
		return
	}
	root := t.TempDir()
	t.Setenv("GOTOGETHER_CORPUS", root)
	t.Setenv("GOTOGETHER_KEEP", t.TempDir())
	first := "test.ordered.a"
	second := "test.ordered.b"
	orderName := t.Name() + "/" + first + "_then_" + second
	ref := orderName + "#1"
	dir := filepath.Join(root, strings.NewReplacer("/", "_", " ", "_").Replace(orderName))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	entry := corpusEntry{Format: corpusFormat, Exploration: ref, Seed: 1, Choices: []qenv.Choice{
		{Kind: "fault." + first, N: 2, Value: 1, Name: "failed"},
		{Kind: "fault." + second, N: 2, Value: 1, Name: "failed"},
	}}
	if mode == "stale" {
		entry.Choices[0].Kind = "fault.no.longer.present"
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "corrupt" {
		data = []byte("{")
	}
	if err := os.WriteFile(filepath.Join(dir, "regression.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if mode == "alone" || mode == "navigate" {
		t.Setenv("GOTOGETHER_RUN", ref+":corpus=regression.json")
	}
	navPath := filepath.Join(t.TempDir(), "node.json")
	if mode == "navigate" {
		t.Setenv("GOTOGETHER_NAV", "start")
		t.Setenv("GOTOGETHER_NAV_OUT", navPath)
	}
	if mode == "seed" {
		t.Setenv("GOTOGETHER_RUN", ref+":seed=1")
	}
	a, b := qenv.DeclareFaults(first, "failed"), qenv.DeclareFaults(second, "failed")
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		failedA, failedB := s.FaultOf(a) != "", s.FaultOf(b) != ""
		balance := 10
		if mode == "regression" && failedA && failedB {
			balance = 11
		}
		qobs.Record(s.Context(), "deposit", nil, "amount", 10, "obs.balance", balance)
		return nil
	}}
	after := 999
	if mode == "alone" || mode == "navigate" {
		after = 0
	}
	result := sys.FaultOrders(t, FaultOrderConfig{Seeds: 2, Faults: []qenv.Scripted{
		{Kind: first, Name: "failed", After: after}, {Kind: second, Name: "failed", After: after},
	}})
	if t.Failed() {
		return
	}
	order := result.Orders[0]
	evidence := result.Evidence()
	if order.Status != "unreached" || order.Witness != "" {
		t.Fatalf("corpus became directed witness: %+v", order)
	}
	saved := filepath.Join(t.TempDir(), "roundtrip.json")
	if err := evidence.WriteJSON(saved); err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadEvidence(saved)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Runs != evidence.Runs || len(decoded.FaultOrders[0].Attempts) != len(evidence.FaultOrders[0].Attempts) {
		t.Fatal("evidence roundtrip changed campaign")
	}
	attempts := len(evidence.FaultOrders[0].Attempts)
	wantRuns, wantAttempts := 3, 2
	if mode == "alone" || mode == "navigate" {
		wantRuns, wantAttempts = 1, 0
		if evidence.Role != RoleReplay {
			t.Fatalf("corpus reproduction role=%q", evidence.Role)
		}
	}
	if mode == "navigate" {
		wantRuns = 0
	}
	if mode == "seed" {
		wantRuns, wantAttempts = 1, 1
	}
	if len(order.Exploration.Runs) != wantRuns || attempts != wantAttempts {
		t.Fatalf("runs=%d attempts=%d", len(order.Exploration.Runs), attempts)
	}
	for _, attempt := range evidence.FaultOrders[0].Attempts {
		if attempt.Seed == nil || *attempt.Seed > 2 {
			t.Fatalf("corpus inflated attempt: %+v", attempt)
		}
	}
	if mode == "supplemental" {
		if evidence.Runs != 5 || evidence.Conform != 5 || len(evidence.Profiles) != 2 || evidence.Profiles[0].Seeds == nil || evidence.Profiles[0].Seeds.Count != 2 {
			t.Fatalf("supplemental corpus distorted inclusive totals/profile: %+v", evidence)
		}
	}
	if mode != "alone" && mode != "navigate" && mode != "seed" {
		replay := order.Exploration.Runs[2]
		if replay.origin.kind != "corpus" || replay.Report == nil || !replay.Report.OK() {
			t.Fatalf("corpus not model checked: %+v", replay)
		}
		if hint := order.Exploration.reproduce(replay); !strings.Contains(hint, ":corpus=regression.json") {
			t.Fatalf("replay hint=%s", hint)
		}
	}
	if mode == "navigate" {
		data, err := os.ReadFile(navPath)
		if err != nil {
			t.Fatal(err)
		}
		var node navproto.Node
		if err := json.Unmarshal(data, &node); err != nil {
			t.Fatal(err)
		}
		if node.Run != ref+":corpus=regression.json" || !strings.Contains(strings.Join(node.Notes, "\n"), "requested fault order:") {
			t.Fatalf("navigation lost corpus identity/scope: %+v", node)
		}
	}
}

func TestFaultOrdersCorpusRegressions(t *testing.T) {
	RequireQuint(t)
	for _, test := range []struct{ mode, want string }{
		{"supplemental", ""}, {"alone", ""}, {"navigate", ""}, {"seed", ""},
		{"regression", "mismatch after deposit"}, {"stale", "is stale"}, {"corrupt", "regression.json"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFaultOrdersCorpusChild$", "-test.v")
			for _, v := range os.Environ() {
				if !strings.HasPrefix(v, "GOTOGETHER_") && !strings.HasPrefix(v, "GO_WANT_ORDER_CORPUS=") {
					cmd.Env = append(cmd.Env, v)
				}
			}
			cmd.Env = append(cmd.Env, "GO_WANT_ORDER_CORPUS="+test.mode)
			out, err := cmd.CombinedOutput()
			if test.want == "" {
				if err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			} else if err == nil || !strings.Contains(string(out), test.want) {
				t.Fatalf("want %q, err=%v\n%s", test.want, err, out)
			}
		})
	}
}
