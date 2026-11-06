package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
)

func TestEvidenceFaultOrdersKeepCoverageSeparate(t *testing.T) {
	order := qtest.FaultOrderEvidence{First: qtest.FaultOrderSpec{Kind: "rpc.a", Option: 1}, Second: qtest.FaultOrderSpec{Kind: "rpc.b", Option: 1}, Budget: 3, Status: "unreached", Attempts: []qtest.FaultOrderAttempt{{Run: "TestOrders/a#1:seed=1"}}}
	absent := &qtest.Evidence{Format: qtest.EvidenceFormat, Role: qtest.RoleFaithful, Test: "TestOrders/a#1", Runs: 1, Conform: 1, FaultOrders: []qtest.FaultOrderEvidence{order}}
	witnessed := *absent
	witnessed.Test = "TestOther#1"
	witnessed.FaultOrders = append([]qtest.FaultOrderEvidence(nil), absent.FaultOrders...)
	witnessed.FaultOrders[0].Status = "witnessed"
	witnessed.FaultOrders[0].Witness = order.Attempts[0].Run
	text, gaps := suiteReport(views(t, absent, &witnessed))
	if gaps != 1 || !strings.Contains(text, "1 runs, 1 conform") || !strings.Contains(text, "unreached (1 attempts, seed budget 3)") || !strings.Contains(text, "bounded absence, not impossibility") || !strings.Contains(text, "witness TestOrders/a#1:seed=1") {
		t.Fatalf("coverage conflated with conformance: gaps %d\n%s", gaps, text)
	}
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := absent.WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	code, _ := capture(t, func() (int, error) { return evidence([]string{"-history", "-strict", path}) })
	if code != 1 {
		t.Fatalf("strict missing directed goal exit %d, want1", code)
	}
	absent.FaultOrders[0].Status = "witnessed"
	absent.FaultOrders[0].Witness = "forged"
	raw, err := json.Marshal(absent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	code, err = evidence([]string{"-history", "-strict", path})
	if code != 2 || err == nil {
		t.Fatalf("malformed coverage exit %d, want operational error", code)
	}
}
