package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func TestDownProcessesRemainWholeRunRuntimeEvidence(t *testing.T) {
	n := &navproto.Node{
		Run: "seed=1", Receipt: receipt.Summary{Status: receipt.Inconclusive, Scope: "collected_history", OtherExecutions: receipt.NotChecked},
		Execution: &runprofile.Execution{Run: "seed=1", Settlement: &runprofile.Settlement{
			After: "1s", DownProcesses: []runprofile.ProcessIdentity{{Name: "worker", Incarnation: 2, Index: 3}},
		}},
		Model: []navproto.ActorState{{Actor: "app", State: []navproto.NamedValue{{Name: "done", Value: "false"}}}},
	}
	m := New(Config{NoColor: true})
	m.w, m.h = 100, 30
	for _, cursor := range []int{0, 7} {
		n.At = cursor
		detail := strings.Join(m.runScreen(&entry{r: n}).detail, "\n")
		for _, want := range []string{"runtime did not settle (bound 1s)", "required processes down", `"worker"#2 (process 3)`} {
			if !strings.Contains(detail, want) {
				t.Fatalf("cursor %d lost %q:\n%s", cursor, want, detail)
			}
		}
		if n.Receipt.Status != receipt.Inconclusive {
			t.Fatal("runtime availability changed the model judgment")
		}
	}
	data, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var copied navproto.Node
	if err := json.Unmarshal(data, &copied); err != nil {
		t.Fatal(err)
	}
	p := copied.Execution.Settlement.DownProcesses[0]
	if p.Name != "worker" || p.Incarnation != 2 || p.Index != 3 {
		t.Fatalf("agent JSON lost native process identity: %+v", p)
	}
}
