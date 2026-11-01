package validate

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPartitionDocumentLinksSharedScope(t *testing.T) {
	c := ledgerChecker(t, "ledger.yaml")
	defer c.Close()
	rep, err := c.Check(t.Context(), ledgerLog(op("open", "acct", "a"), op("open", "acct", "b"), op("deposit", "acct", "a", "amount", 3), op("withdraw", "acct", "b", "amount", 7)))
	if err != nil {
		t.Fatal(err)
	}
	d := NewDocument("validate")
	d.AddReport(rep)
	d.AddReport(rep)
	if len(d.Receipt.PartitionInstances) != 2 || len(d.Receipt.Actors) != 4 || len(d.Actors) != 2 || len(d.Partitions) != 2 {
		t.Fatalf("missing scope groups: %+v", d)
	}
	for i, group := range d.Receipt.PartitionInstances {
		if group.ID != i+1 || group.SourceActor != "ledger" || group.Key != "acct" || group.OriginalConstants["ACCTS"] != `Set("a", "b")` || group.OriginalConstants["LIMIT"] != "7" || len(group.ReplaySubstitutions) != 1 || group.ReplaySubstitutions[0].Name != "ACCTS" || group.ReplaySubstitutions[0].Value != `Set("a")` {
			t.Fatalf("lost original/replay provenance: %+v", group)
		}
		if len(d.Partitions[i].Instances) != 1 || d.Partitions[i].Instances[0] != group.ID {
			t.Fatalf("folded group lost shared instance: %+v", d.Partitions[i])
		}
		visible := d.Actors[i]
		if len(visible.Constants) != 0 || visible.Receipt == nil || visible.Receipt.SourceActor != "ledger" || visible.Receipt.PartitionInfo == nil || visible.Receipt.PartitionInfo.Instance != group.ID {
			t.Fatalf("visible partition scope: %+v", visible)
		}
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"originalConstants"`) != 2 {
		t.Fatalf("instance table duplicated: %s", data)
	}
}

func TestPartitionDocumentSizeGrowsWithJudgments(t *testing.T) {
	c := ledgerChecker(t, "ledger.yaml")
	defer c.Close()
	size := func(n int) int {
		t.Helper()
		ops := make([][]any, 0, n)
		for i := 0; i < n; i++ {
			ops = append(ops, op("open", "acct", fmt.Sprintf("account-%03d", i)))
		}
		rep, err := c.Check(t.Context(), ledgerLog(ops...))
		if err != nil {
			t.Fatal(err)
		}
		d := NewDocument("validate")
		d.AddReport(rep)
		if len(d.Receipt.PartitionInstances) != 1 || len(d.Receipt.Actors) != n || len(d.Partitions) != 1 || len(d.Partitions[0].Instances) != 1 {
			t.Fatalf("wrong folded provenance for %d partitions", n)
		}
		data, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		return len(data)
	}
	small, large := size(16), size(32)
	if large > small*5/2 {
		t.Fatalf("doubling partitions grew JSON from %d to %d bytes; shared constants must occur once", small, large)
	}
}
