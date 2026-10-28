// Package auditsteps reads the legacy payment handler's audit log as model
// steps, so its runs are judged by model/payments.qnt without a change to
// its code: the migration's one adapter (README.md, stage 4).
//
// The handler writes one slog record per request, under its lock, with a
// per-process sequence number (audit.go). Each record's message names its
// operation (charge.refund), and its attributes are the step's fields:
//
//	{"time":"2026-10-02T09:00:04Z","level":"INFO","msg":"charge.refund","audit_seq":10,
//	 "instance":"payments-1","charge_id":"ch_3","result":"ok","amount":700,"refunded":700}
//
// is the step refund(id: "ch_3", amount: 700) with outcome ok, seq 10 of
// recorder payments-1. Handler records the live records of a run (a legacy
// test wrapped in qtest.Record); ReadLog reads a log file, keeping its
// sequence numbers, so a rotated log shows its lost prefix.
package auditsteps

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Actor is the model instance every step is a step of: one service.
const Actor = "payments"

// fields are one record's attributes, by name.
type fields map[string]any

func (f fields) str(k string) string {
	if v, ok := f[k]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func (f fields) int(k string) int64 {
	switch v := f[k].(type) {
	case int64:
		return v
	case float64: // a log line's number, read back by encoding/json
		return int64(v)
	case uint64:
		return int64(v)
	}
	return 0
}

// record records the step of one audit record into ctx's run, if its
// message names an operation (false otherwise). The thread is the
// instance: its records are in the order its lock gave them.
func record(ctx context.Context, msg string, f fields) bool {
	id := []any{"thread", f.str("instance"), "outcome", f.str("result"), "id", f.str("charge_id")}
	switch msg {
	case "charge.authorize":
		qobs.Record(ctx, "authorize", nil, append(id, "key", f.str("idempotency_key"), "amount", f.int("amount"))...)
	case "charge.capture":
		qobs.Record(ctx, "capture", nil, append(id, "amount", f.int("amount"))...)
	case "charge.refund":
		qobs.Record(ctx, "refund", nil, append(id, "amount", f.int("amount"))...)
	case "charge.void":
		qobs.Record(ctx, "void", nil, id...)
	case "charge.lookup":
		qobs.Record(ctx, "lookup", nil, append(id, "obs.status", f.str("status"), "obs.refunded", f.int("refunded"))...)
	default:
		return false
	}
	return true
}

// Handler returns a slog handler that records each audit record as a
// step of the run its context (or goroutine) belongs to, and passes every
// record on to next (nil: none). Give it to the service as its logger.
func Handler(next slog.Handler) slog.Handler { return &handler{next: next} }

type handler struct {
	next  slog.Handler
	attrs []slog.Attr
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool { return true }

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	f := fields{}
	add := func(a slog.Attr) bool { f[a.Key] = a.Value.Resolve().Any(); return true }
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(add)
	record(ctx, r.Message, f)
	if h.next != nil && h.next.Enabled(ctx, r.Level) {
		return h.next.Handle(ctx, r)
	}
	return nil
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	c := &handler{next: h.next, attrs: append(append([]slog.Attr(nil), h.attrs...), as...)}
	if h.next != nil {
		c.next = h.next.WithAttrs(as)
	}
	return c
}

// WithGroup is not used by the service: its records' attributes stay flat.
func (h *handler) WithGroup(string) slog.Handler { return h }

// ReadLog reads an audit log (JSON lines, as the service writes them in
// production) as steps: each operation's record a step, numbered by its
// audit_seq in the recorder of its instance, at its time. Other records
// are skipped.
func ReadLog(r io.Reader) ([]qtrace.Step, error) {
	var out []qtrace.Step
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; sc.Scan(); n++ {
		var f fields
		if len(sc.Bytes()) == 0 {
			continue
		}
		if err := json.Unmarshal(sc.Bytes(), &f); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		mem := &qobs.MemorySink{}
		inst := f.str("instance")
		ctx := qobs.WithRecorder(context.Background(), qobs.NewRecorder(mem, qobs.WithActor(Actor), qobs.WithProcess(inst), qobs.WithID(inst)))
		if !record(ctx, f.str("msg"), f) {
			continue
		}
		s := mem.Steps()[0]
		s.Seq = uint64(f.int("audit_seq"))
		if s.Seq == 0 {
			return nil, fmt.Errorf("line %d: %s has no audit_seq: a log without sequence numbers cannot show what it lost", n, f.str("msg"))
		}
		t, err := time.Parse(time.RFC3339Nano, f.str("time"))
		if err != nil {
			return nil, fmt.Errorf("line %d: time: %w", n, err)
		}
		s.Time, s.Source = t, fmt.Sprintf("line %d", n)
		out = append(out, s)
	}
	return out, sc.Err()
}

// ReadFile is ReadLog of a file.
func ReadFile(path string) ([]qtrace.Step, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	steps, err := ReadLog(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return steps, nil
}
