package auditsteps

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qobs"
)

// A log line is the step its live record would be: the same action,
// arguments, observations and result, numbered by its audit_seq.
func TestLogLineIsItsLiveStep(t *testing.T) {
	line := `{"time":"2026-10-02T09:00:40.000Z","level":"INFO","msg":"charge.lookup","audit_seq":13,"instance":"payments-1","charge_id":"ch_3","result":"ok","status":"captured","refunded":1200}` + "\n" +
		`{"time":"2026-10-02T09:00:41.000Z","level":"INFO","msg":"listening","addr":":8080"}` + "\n"
	steps, err := ReadLog(strings.NewReader(line))
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want the lookup alone (a record naming no operation is no step)", len(steps))
	}
	s := steps[0]
	if s.Action != "lookup" || s.Seq != 13 || s.Recorder != "payments-1" || s.Outcome != "ok" || s.Args["id"] != "ch_3" || s.Obs["refunded"] != int64(1200) || s.Obs["status"] != "captured" {
		t.Fatalf("got %+v", s)
	}

	mem := &qobs.MemorySink{}
	ctx := qobs.WithRecorder(context.Background(), qobs.NewRecorder(mem))
	slog.New(Handler(nil)).InfoContext(ctx, "charge.lookup", "audit_seq", 13, "instance", "payments-1", "charge_id", "ch_3", "result", "ok", "status", "captured", "refunded", int64(1200))
	live := mem.Steps()
	if len(live) != 1 || live[0].Action != s.Action || live[0].Outcome != s.Outcome || live[0].Args["id"] != s.Args["id"] || live[0].Obs["refunded"] != s.Obs["refunded"] {
		t.Fatalf("the live record's step %+v, the log line's %+v", live, s)
	}
}

// A log without sequence numbers cannot show a lost prefix: refused.
func TestLogWithoutSeqsRefused(t *testing.T) {
	_, err := ReadLog(strings.NewReader(`{"time":"2026-10-02T09:00:40Z","msg":"charge.void","instance":"p","charge_id":"ch_1","result":"ok"}` + "\n"))
	if err == nil {
		t.Fatal("a record without audit_seq was read")
	}
}
