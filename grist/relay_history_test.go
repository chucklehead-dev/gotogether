package grist_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A restore can land between reading native rows and rendering their events.
// A stale candidate cannot permanently stop the relay, while unchanged invalid
// evidence must still fail. These wrappers make that seam deterministic.
func TestRelayHistoryChangesDuringRendering(t *testing.T) {
	for _, mode := range []string{"lost candidate", "unchanged invalid", "replacement invalid", "reread error"} {
		t.Run(mode, func(t *testing.T) {
			app, _, command := app(false)
			original := newStore(t)
			ctx := context.Background()
			for i, name := range []string{"one", "two"} {
				_, err := command.Execute(ctx, original, grist.Envelope{ID: name, Tenant: "t1"}, Create{Counter: name, Name: name})
				if err != nil {
					t.Fatalf("command %d: %v", i, err)
				}
			}
			rows, err := original.Steps(ctx, "t1", 0, 0)
			if err != nil || len(rows) != 2 {
				t.Fatalf("original rows: %v %v", rows, err)
			}
			events, _, err := original.Read(ctx, "t1", store.Selector{Types: []string{"counter-created"}}, 0)
			if err != nil || len(events) != 2 {
				t.Fatalf("original events: %v %v", events, err)
			}
			restored := newStore(t)
			first := events[0]
			if _, err := restored.Append(ctx, "t1", store.Batch{Events: []store.Event{first.Event}, RetainedIDs: []string{first.ID}, Time: first.Time, Cause: first.Cause}); err != nil {
				t.Fatal(err)
			}
			s := &renderHistoryStore{Store: restored, initial: rows, rows: rows[:1]}
			readErr := errors.New("step log read unavailable")
			switch mode {
			case "unchanged invalid":
				// The native current row still claims an event absent from
				// its history: this is real retained-data corruption.
				s.rows = rows
			case "replacement invalid":
				bad := rows[1]
				bad.Step = []byte(`{"v":999,"steps":[]}`)
				s.rows = []store.StepRow{rows[0], bad}
			case "reread error":
				s.readErr = readErr
			}
			relay, err := grist.NewRelay(s)
			if err != nil {
				t.Fatal(err)
			}
			relay.RenderAs(app)
			sink := &qobs.MemorySink{}
			err = relay.Flush(qobs.WithRecorder(ctx, qobs.NewRecorder(sink)))
			switch mode {
			case "lost candidate":
				if err != nil {
					t.Fatalf("stale candidate permanently stopped relay: %v", err)
				}
				if behind, err := relay.Behind(ctx); err != nil || behind != 0 {
					t.Fatalf("relay failed to catch up to retained history: %d %v", behind, err)
				}
			case "unchanged invalid":
				if err == nil || !strings.Contains(err.Error(), "the store 0") {
					t.Fatalf("unchanged malformed row lost its data error: %v", err)
				}
			case "replacement invalid":
				if err == nil || !strings.Contains(err.Error(), "999") {
					t.Fatalf("replacement was not checked for its own error: %v", err)
				}
			case "reread error":
				if !errors.Is(err, readErr) {
					t.Fatalf("coherence read error became success or corruption: %v", err)
				}
			}
			if steps := sink.Steps(); len(steps) != 1 || steps[0].Args["id"] != "one" {
				t.Fatalf("relay emitted stale or invalid evidence: %v", steps)
			}
		})
	}
}

type renderHistoryStore struct {
	*memstore.Store
	initial, rows []store.StepRow
	readErr       error
	reads         int
}

func (s *renderHistoryStore) Steps(_ context.Context, _ string, after int64, limit int) ([]store.StepRow, error) {
	s.reads++
	rows := s.rows
	if s.reads == 1 {
		rows = s.initial
	} else if s.readErr != nil {
		return nil, s.readErr
	}
	var out []store.StepRow
	for _, row := range rows {
		if row.Seq > after {
			out = append(out, row)
			if limit > 0 && len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (s *renderHistoryStore) StepCount(context.Context, string) (int64, error) {
	return int64(len(s.rows)), nil
}
