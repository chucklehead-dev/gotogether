package validate

import (
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func TestPartitionScopeRetainsOriginalAndEffectiveInstance(t *testing.T) {
	c := ledgerChecker(t, "ledger.yaml")
	defer c.Close()
	steps := ledgerLog(op("open", "acct", "a"), op("open", "acct", "b"), op("deposit", "acct", "b", "amount", 7), op("deposit", "acct", "a", "amount", 3))
	traces, _ := reconstruct(c.Binding, steps)
	traces[0].Actor = "ledger acct=misleading with spaces"
	pt, err := c.partition(traces[0])
	if err != nil {
		t.Fatal(err)
	}
	rs := c.partitionResults(pt)
	if len(rs) != 2 || rs[0].partitionInstance != rs[1].partitionInstance {
		t.Fatal("instance evidence must be shared once per group")
	}
	if len(rs[0].Constants) > 0 && &rs[0].Constants[0] != &rs[1].Constants[0] {
		t.Fatal("effective constant list copied per partition")
	}
	instance := rs[0].PartitionInstance()
	if instance.ID != 0 || instance.SourceActor != traces[0].Actor || instance.Key != "acct" || instance.Factoring != "checked_on_evaluated_prefixes" {
		t.Fatalf("identity: %+v", instance)
	}
	if instance.OriginalConstants["ACCTS"] != `Set("a", "b")` || instance.OriginalConstants["LIMIT"] != "7" {
		t.Fatalf("whole instance lost: %+v", instance)
	}
	effective := instance.ReplayConstants()
	if effective["ACCTS"] != `Set("a")` || effective["LIMIT"] != "7" {
		t.Fatalf("replay instance: %v", effective)
	}
	if len(instance.ReplaySubstitutions) != 1 || instance.ReplaySubstitutions[0].Name != "ACCTS" || instance.ReplaySubstitutions[0].Basis != "unread_by_evaluated_prefixes" {
		t.Fatalf("substitutions: %+v", instance.ReplaySubstitutions)
	}
	for i, r := range rs {
		if r.SourceActor != traces[0].Actor || partitionOf(r) != traces[0].Actor {
			t.Fatalf("parsed display identity: %+v", r)
		}
		if !reflect.DeepEqual(r.Constants, pt.plans[i].ConstantList()) {
			t.Fatalf("effective constants contain prose: %v", r.Constants)
		}
		r.Conforms = Pass
	}
	summary := (&Report{Actors: rs}).Receipt()
	if len(summary.PartitionInstances) != 1 || summary.PartitionInstances[0].ID != 1 || summary.Actors[0].PartitionInfo.Instance != 1 || summary.Actors[1].PartitionInfo.Instance != 1 {
		t.Fatalf("group reference: %+v", summary)
	}
	standalone := rs[0].Receipt()
	if standalone.PartitionInfo.Instance != 0 || standalone.PartitionInfo.Value != "a" {
		t.Fatalf("standalone identity: %+v", standalone.PartitionInfo)
	}
	instance.OriginalConstants["ACCTS"] = "changed"
	summary.PartitionInstances[0].OriginalConstants["LIMIT"] = "changed"
	if rs[0].PartitionInstance().OriginalConstants["ACCTS"] != `Set("a", "b")` || rs[0].PartitionInstance().OriginalConstants["LIMIT"] != "7" {
		t.Fatal("receipt mutated producer evidence")
	}
}

func TestPartitionValuePreservesSourceTypesAndPendingOnly(t *testing.T) {
	whole := &qtrace.Trace{Steps: []qtrace.Step{{Action: "snapshot", Args: map[string]any{"acct": "wrong snapshot identity"}}, {Args: map[string]any{"acct": int64(1)}}, {Args: map[string]any{"acct": "1"}}}}
	for _, tc := range []struct {
		at   []int
		part *qtrace.Trace
		want any
	}{{[]int{0, 1}, &qtrace.Trace{}, int64(1)}, {[]int{0, 2}, &qtrace.Trace{}, "1"}, {[]int{0}, &qtrace.Trace{Pending: []qtrace.Step{{Args: map[string]any{"acct": "pending"}}}}, "pending"}} {
		if got := partitionValue(whole, tc.at, tc.part, "acct", true); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("got %T %v, want %T %v", got, got, tc.want, tc.want)
		}
	}
}

func TestPartitionReceiptMergeKeepsSeparateLocalGroups(t *testing.T) {
	group := func(original string) *receipt.PartitionInstance {
		return &receipt.PartitionInstance{SourceActor: "same", Key: "acct", Factoring: "checked_on_evaluated_prefixes", OriginalConstants: map[string]string{"ACCTS": original}, ReplaySubstitutions: []receipt.ConstantSubstitution{{Name: "ACCTS", Value: `Set("a")`, Basis: "unread_by_evaluated_prefixes"}}}
	}
	first, second := group(`Set("a", "b")`), group(`Set("a", "c")`)
	result := func(g *receipt.PartitionInstance) *Result {
		return &Result{Actor: "same acct=a", SourceActor: "same", Partition: "acct=a", Conforms: Pass, partitionInstance: g, partitionInfo: &receipt.PartitionInfo{Key: "acct", Value: "a"}}
	}
	a := (&Report{Actors: []*Result{result(first)}}).Receipt()
	b := (&Report{Actors: []*Result{result(second)}}).Receipt()
	merged := mergeReceipts(a, b)
	if len(merged.PartitionInstances) != 2 || merged.Actors[0].PartitionInfo.Instance != 1 || merged.Actors[1].PartitionInfo.Instance != 2 {
		t.Fatalf("merged unrelated instances: %+v", merged)
	}
	merged.Actors[1].PartitionInfo.Value = "changed"
	merged.PartitionInstances[1].OriginalConstants["ACCTS"] = "changed"
	if b.Actors[0].PartitionInfo.Value != "a" || b.Actors[0].PartitionInfo.Instance != 1 || b.PartitionInstances[0].OriginalConstants["ACCTS"] != `Set("a", "c")` {
		t.Fatal("merge mutated input")
	}
}

func TestPartitionBatchKeepsOriginalInstancesSeparate(t *testing.T) {
	c := ledgerChecker(t, "ledger.yaml")
	defer c.Close()
	runs := [][]qtrace.Step{ledgerLog(op("open", "acct", "a"), op("open", "acct", "b")), ledgerLog(op("open", "acct", "a"), op("open", "acct", "c"))}
	reports, errs := c.CheckBatch(t.Context(), runs, 4)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if len(reports) != 2 {
		t.Fatalf("reports: %d", len(reports))
	}
	for i, want := range []string{`Set("a", "b")`, `Set("a", "c")`} {
		summary := reports[i].Receipt()
		if len(summary.PartitionInstances) != 1 || summary.PartitionInstances[0].OriginalConstants["ACCTS"] != want || summary.PartitionInstances[0].ReplayConstants()["ACCTS"] != `Set("a")` {
			t.Fatalf("run %d lost original instance: %+v", i, summary.PartitionInstances)
		}
	}
	merged := mergeReceipts(reports[0].Receipt(), reports[1].Receipt())
	if len(merged.PartitionInstances) != 2 || merged.Actors[2].PartitionInfo.Instance != 2 {
		t.Fatalf("batch groups conflated: %+v", merged)
	}
}

func TestPartitionDefaultSourceActorCountsOnce(t *testing.T) {
	c := ledgerChecker(t, "ledger.yaml")
	defer c.Close()
	steps := ledgerLog(op("open", "acct", "a"), op("open", "acct", "b"))
	traces, _ := reconstruct(c.Binding, steps)
	traces[0].Actor = ""
	pt, err := c.partition(traces[0])
	if err != nil {
		t.Fatal(err)
	}
	results := c.partitionResults(pt)
	for _, r := range results {
		r.Conforms = Pass
		if r.SourceActor != "" || partitionOf(r) != "default" {
			t.Fatalf("default source: %+v", r)
		}
	}
	counts := (&Report{Actors: results}).Receipt().Counts()
	if counts.Actors != 1 || counts.Partitions != 2 {
		t.Fatalf("default partitions counted as source actors: %+v", counts)
	}
}
