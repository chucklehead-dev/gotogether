package qhistory_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qhistory"
)

func TestSessionOrderEvidence(t *testing.T) {
	h := []qhistory.Txn{
		{ID: 90, Outcome: qhistory.Committed, Session: "caller", SessionOrder: 10, Ops: []qhistory.Op{{Kind: qhistory.Append, Key: "x", Value: 1}}},
		{ID: 2, Outcome: qhistory.Committed, Session: "caller", SessionOrder: 30, Ops: []qhistory.Op{{Kind: qhistory.Read, Key: "x"}}},
	}
	// Neither transaction IDs nor input position establish caller order.
	slices.Reverse(h)
	for _, level := range []qhistory.Level{qhistory.Serializable, qhistory.StrongSessionSerializable, qhistory.StrictSerializable} {
		r, err := qhistory.Check(level, h)
		if err != nil {
			t.Fatal(err)
		}
		if level == qhistory.Serializable {
			if !r.OK() {
				t.Fatal(r)
			}
			continue
		}
		if r.OK() || !r.Has("G-single-session") {
			t.Fatal(r)
		}
		a := r.Anomalies[0]
		if len(a.Dependencies) != len(a.Txns) || len(a.Dependencies) != 2 {
			t.Fatalf("missing cycle evidence: %+v", a)
		}
		for i, d := range a.Dependencies {
			if d.From != a.Txns[i] || d.To != a.Txns[(i+1)%len(a.Txns)] {
				t.Fatalf("unordered evidence: %+v", a)
			}
		}
		d := a.Dependencies[0]
		if d.Kind != qhistory.SessionOrder || d.From != 90 || d.To != 2 || d.Session != "caller" || d.FromOrder != 10 || d.ToOrder != 30 {
			t.Fatalf("session provenance: %+v", d)
		}
		if !strings.Contains(a.String(), `session("caller":10<30)`) {
			t.Fatal(a)
		}
		encoded, err := json.Marshal(a)
		if err != nil || !strings.Contains(string(encoded), `"FromOrder":10`) {
			t.Fatalf("JSON evidence: %s %v", encoded, err)
		}
	}
	h[0].Session = "another caller"
	r, err := qhistory.Check(qhistory.StrongSessionSerializable, h)
	if err != nil || !r.OK() {
		t.Fatalf("different sessions: %v %v", r, err)
	}
}

func TestSessionUnknownAndAbortedAttempts(t *testing.T) {
	appendX := qhistory.Op{Kind: qhistory.Append, Key: "x", Value: 1}
	emptyX := qhistory.Op{Kind: qhistory.Read, Key: "x"}
	readX := qhistory.Op{Kind: qhistory.Read, Key: "x", List: []int{1}}
	t.Run("inferred unknown cannot precede", func(t *testing.T) {
		h := []qhistory.Txn{
			{ID: 1, Outcome: qhistory.Unknown, Session: "a", SessionOrder: 1, Ops: []qhistory.Op{appendX}},
			{ID: 2, Outcome: qhistory.Committed, Session: "a", SessionOrder: 2, Ops: []qhistory.Op{emptyX}},
			{ID: 3, Outcome: qhistory.Committed, Session: "b", SessionOrder: 1, Ops: []qhistory.Op{readX}},
		}
		for _, level := range []qhistory.Level{qhistory.StrongSessionSerializable, qhistory.StrictSerializable} {
			r, err := qhistory.Check(level, h)
			if err != nil || !r.OK() {
				t.Fatalf("unknown completion: %v %v", r, err)
			}
		}
		h[0].Outcome = qhistory.Committed
		r, err := qhistory.Check(qhistory.StrongSessionSerializable, h)
		if err != nil || r.OK() {
			t.Fatalf("known completion: %v %v", r, err)
		}
	})
	t.Run("inferred unknown can follow", func(t *testing.T) {
		h := []qhistory.Txn{
			{ID: 1, Outcome: qhistory.Committed, Session: "a", SessionOrder: 1, Ops: []qhistory.Op{appendX}},
			{ID: 2, Outcome: qhistory.Unknown, Session: "a", SessionOrder: 2, Ops: []qhistory.Op{emptyX, {Kind: qhistory.Append, Key: "y", Value: 2}}},
			{ID: 3, Outcome: qhistory.Committed, Session: "b", SessionOrder: 1, Ops: []qhistory.Op{{Kind: qhistory.Read, Key: "y", List: []int{2}}}},
		}
		r, err := qhistory.Check(qhistory.StrongSessionSerializable, h)
		if err != nil || !r.Has("G-single-session") {
			t.Fatalf("unknown successor: %v %v", r, err)
		}
	})
	t.Run("bridge gaps and unsuccessful attempts", func(t *testing.T) {
		h := []qhistory.Txn{
			{ID: 1, Outcome: qhistory.Committed, Session: "a", SessionOrder: 10, Ops: []qhistory.Op{appendX}},
			{ID: 2, Outcome: qhistory.Unknown, Session: "a", SessionOrder: 20},
			{ID: 3, Outcome: qhistory.Aborted, Session: "a", SessionOrder: 30},
			{ID: 4, Outcome: qhistory.Committed, Session: "a", SessionOrder: 50, Ops: []qhistory.Op{emptyX}},
		}
		r, err := qhistory.Check(qhistory.StrongSessionSerializable, h)
		if err != nil || !r.Has("G-single-session") {
			t.Fatalf("bridge: %v %v", r, err)
		}
	})
}

func TestSessionMetadataValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		level qhistory.Level
		h     []qhistory.Txn
		want  string
	}{
		{"invalid level", qhistory.Level(99), nil, "invalid isolation level"},
		{"negative level", qhistory.Level(-1), nil, "invalid isolation level"},
		{"missing metadata", qhistory.StrongSessionSerializable, []qhistory.Txn{{ID: 1, Outcome: qhistory.Committed}}, "requires Session"},
		{"unknown needs metadata", qhistory.StrongSessionSerializable, []qhistory.Txn{{ID: 1}}, "requires Session"},
		{"half session", qhistory.Serializable, []qhistory.Txn{{ID: 1, Session: "a"}}, "supplied together"},
		{"half order", qhistory.StrictSerializable, []qhistory.Txn{{ID: 1, SessionOrder: 1}}, "supplied together"},
		{"duplicate including aborted", qhistory.Serializable, []qhistory.Txn{{ID: 1, Outcome: qhistory.Aborted, Session: "a", SessionOrder: 1}, {ID: 2, Session: "a", SessionOrder: 1}}, "duplicate session"},
		{"invalid outcome", qhistory.Serializable, []qhistory.Txn{{ID: 1, Outcome: qhistory.Outcome(9)}}, "invalid outcome"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := qhistory.Check(tc.level, tc.h)
			if err == nil || r != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v %v; want %q", r, err, tc.want)
			}
		})
	}
	for _, level := range []qhistory.Level{qhistory.Serializable, qhistory.StrongSessionSerializable, qhistory.StrictSerializable} {
		r, err := qhistory.Check(level, []qhistory.Txn{{ID: 1, Outcome: qhistory.Aborted}})
		if err != nil || !r.OK() {
			t.Fatalf("aborted needs no metadata: %v %v", r, err)
		}
	}
	if got := qhistory.Level(99).String(); got != "Level(99)" {
		t.Fatal(got)
	}
}

func TestDataCycleDependencyEvidence(t *testing.T) {
	h := []qhistory.Txn{
		{ID: 1, Outcome: qhistory.Committed, Ops: []qhistory.Op{{Kind: qhistory.Append, Key: "x", Value: 1}, {Kind: qhistory.Read, Key: "y", List: []int{2}}}},
		{ID: 2, Outcome: qhistory.Committed, Ops: []qhistory.Op{{Kind: qhistory.Append, Key: "y", Value: 2}, {Kind: qhistory.Read, Key: "x", List: []int{1}}}},
	}
	r, err := qhistory.Check(qhistory.Serializable, h)
	if err != nil || !r.Has("G1c") {
		t.Fatalf("data cycle: %v %v", r, err)
	}
	a := r.Anomalies[0]
	if len(a.Dependencies) != 2 {
		t.Fatalf("missing data evidence: %+v", a)
	}
	for i, d := range a.Dependencies {
		if d.From != a.Txns[i] || d.To != a.Txns[(i+1)%len(a.Txns)] || d.Kind != qhistory.WriteRead || d.Key == "" || d.Session != "" || d.FromOrder != 0 || d.ToOrder != 0 {
			t.Fatalf("invalid data evidence: %+v", a)
		}
	}
}
