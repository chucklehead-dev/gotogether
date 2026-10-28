package payments_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtest"
)

// dayBinding is payments.binding.yaml partitioned by charge (partition:
// id): the model factors by it since R2's key lookup is an index keyed
// by the key (byKey), not a scan of every charge.
func dayBinding(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(bindingFile)
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := filepath.Abs("model/payments.qnt")
	text := strings.Replace(string(data), "spec: model/payments.qnt\n", "spec: "+spec+"\npartition: id\n", 1)
	path := filepath.Join(t.TempDir(), "payments.day.binding.yaml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// syntheticDay writes an audit log of n charges, each authorized,
// captured and refunded in part, but ch_<rejected>'s refund answered
// invalid_state (a refusal the model does not allow: the charge is
// captured) and ch_<over>'s refunded past its capture (R5), in that
// order.
func syntheticDay(t *testing.T, n, rejected, over int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "day.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	seq := 0
	rec := func(msg string, kv ...any) {
		seq++
		r := map[string]any{"time": time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC).Add(time.Duration(seq) * time.Second).Format(time.RFC3339Nano),
			"level": "INFO", "msg": msg, "audit_seq": seq, "instance": "payments-1"}
		for i := 0; i < len(kv); i += 2 {
			r[kv[i].(string)] = kv[i+1]
		}
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	for i := range n {
		id := fmt.Sprintf("ch_%d", i)
		rec("charge.authorize", "charge_id", id, "result", "ok", "idempotency_key", fmt.Sprintf("ord-%d", i), "amount", 1000)
		rec("charge.capture", "charge_id", id, "result", "ok", "amount", 600)
		switch i {
		case rejected:
			rec("charge.refund", "charge_id", id, "result", "invalid_state", "amount", 300, "refunded", 0)
		case over:
			rec("charge.refund", "charge_id", id, "result", "ok", "amount", 900, "refunded", 900)
		default:
			rec("charge.refund", "charge_id", id, "result", "ok", "amount", 300, "refunded", 300)
		}
	}
	return path
}

// A day at production volume: 10,000 steps of 3,334 charges, judged
// charge by charge within a budget, and every disagreement reported,
// where the day judged whole takes over 20 minutes (each step's
// invariants over every charge so far) and stops at its first failure:
// here ch_10's rejected refund, hiding ch_2000's breach of R5 after it.
func TestProductionDayAtVolume(t *testing.T) {
	qtest.RequireQuint(t)
	if m := os.Getenv("GOTOGETHER_EVAL"); m == "quint" || m == "shadow" {
		t.Skipf("GOTOGETHER_EVAL=%s: quint judges each of 3,334 charges, and the budget is the Go evaluator's", m)
	}
	steps := logSteps(t, syntheticDay(t, 3334, 10, 2000))
	start := time.Now()
	v := qtest.Validate(t, dayBinding(t), steps)
	v.Expect(t, qtest.Rejects("refund"), qtest.Violates("refundsWithinCapture").At("refund"))
	took := time.Since(start)
	t.Logf("%d steps: %v", len(steps), took)
	if took > 60*time.Second { // about 10 s here, quint's replays of the failures and of a sample of the passes most of it
		t.Errorf("%d steps took %v, over the budget of 60 s", len(steps), took)
	}
}
