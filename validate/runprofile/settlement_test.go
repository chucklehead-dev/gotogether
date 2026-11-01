package runprofile

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSettlementSnapshotAndAggregateRendering(t *testing.T) {
	e := Execution{Run: "seed=1", Simulations: []Simulation{}, Settlement: &Settlement{After: "1s", Tasks: []string{"client: waiting"}, DownProcesses: []ProcessIdentity{{Name: "worker", Incarnation: 1, Index: 1}, {Name: "worker", Incarnation: 1, Index: 2}}}}
	copy := e.Clone()
	e.Settlement.Tasks[0] = "changed"
	e.Settlement.DownProcesses[0].Index = 99
	if copy.Settlement.Tasks[0] != "client: waiting" || copy.Settlement.DownProcesses[0].Index != 1 {
		t.Fatal("runtime evidence aliases its source")
	}
	data, err := json.Marshal(copy)
	if err != nil {
		t.Fatal(err)
	}
	var round Execution
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatal(err)
	}
	if len(round.Settlement.DownProcesses) != 2 || round.Settlement.DownProcesses[1].Index != 2 {
		t.Fatalf("duplicate names lost local identity: %s", data)
	}
	for _, text := range []string{round.String(), ExecutionsString([]Execution{round, {Run: "seed=2"}})} {
		for _, want := range []string{"settlement seed=1", "bound 1s", `"worker"#1 (process 1)`, `"worker"#1 (process 2)`, "client: waiting"} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q in %s", want, text)
			}
		}
	}
}
