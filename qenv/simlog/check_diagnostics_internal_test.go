package simlog

import (
	"fmt"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

func diagnosticFixture(t *testing.T, check bool) *Log {
	t.Helper()
	s := qenv.New(qenv.Nominal{})
	t.Cleanup(s.Close)
	s.Check = check
	l := New(s, "diagnostics", 1)
	if err := l.append(1, 1, true, Record{Partition: 0, Key: "key", Value: []byte("original")}); err != nil {
		t.Fatal(err)
	}
	return l
}

func diagnosticPanic(fn func()) (caught any) {
	defer func() { caught = recover() }()
	fn()
	return nil
}

func requireDiagnostic(t *testing.T, caught any, want string) {
	t.Helper()
	if want == "" {
		if caught != nil {
			t.Fatalf("unexpected diagnostic or incidental panic: %v", caught)
		}
		return
	}
	// A runtime bounds panic is not evidence that the intended contract
	// diagnostic worked, even though both stop the operation.
	if message, ok := caught.(string); !ok || message != want {
		t.Fatalf("panic = %#v, want diagnostic %q", caught, want)
	}
}

// These intentionally malformed fixtures test the diagnostic predicate's
// input domain. They do not claim ordinary public log operations can create
// invalid metadata, or replace the public producer/consumer contract tests.
func TestLogCheckDiagnosesRecordMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Record)
		want   string
	}{
		{"valid", func(*Record) {}, ""},
		{"partition", func(r *Record) { r.Partition = 2 }, "partition 0's record at offset 0 says partition 2, offset 0"},
		{"offset", func(r *Record) { r.Offset = 7 }, "partition 0's record at offset 0 says partition 0, offset 7"},
	} {
		for _, check := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/check=%t", tc.name, check), func(t *testing.T) {
				l := diagnosticFixture(t, check)
				tc.change(&l.parts[0][0])
				want := ""
				if check && tc.want != "" {
					want = "simlog: log diagnostics, after a diagnostic fixture: " + tc.want + " (GOTOGETHER_CHECK)"
				}
				requireDiagnostic(t, diagnosticPanic(func() { l.check("a diagnostic fixture") }), want)
			})
		}
	}
}

// Delivery integrity has a separate diagnostic boundary: a record can keep
// its offset while its key or payload differs from what was appended.
func TestLogCheckDeliveryDiagnosesChangedRecord(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Record)
	}{
		{"valid", func(*Record) {}},
		{"key", func(r *Record) { r.Key = "changed" }},
		{"value", func(r *Record) { r.Value = []byte("changed") }},
	} {
		for _, check := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/check=%t", tc.name, check), func(t *testing.T) {
				l := diagnosticFixture(t, check)
				r := l.parts[0][0]
				tc.change(&r)
				want := ""
				if check && tc.name != "valid" {
					want = "simlog: log diagnostics, after a poll: partition 0's record at offset 0 changed since it landed (GOTOGETHER_CHECK)"
				}
				requireDiagnostic(t, diagnosticPanic(func() { l.checkDelivery(0, 0, []Record{r}) }), want)
			})
		}
	}
}
