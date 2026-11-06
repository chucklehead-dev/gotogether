package pipeline

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/chucklehead-dev/gotogether/qenv/simlog"
)

type partialPollReader struct {
	cancel    context.CancelFunc
	polls     int
	committed []int
}

func (r *partialPollReader) Poll(ctx context.Context) ([]simlog.Record, error) {
	r.polls++
	switch r.polls {
	case 1:
		return []simlog.Record{{Partition: 0, Offset: 0, Key: "a", Value: Encode(1, 1)}}, errors.New("another partition's fetch failed")
	case 2:
		return []simlog.Record{{Partition: 0, Offset: 1, Key: "a", Value: Encode(2, 2)}}, nil
	default:
		r.cancel()
		return nil, ctx.Err()
	}
}

func (r *partialPollReader) Commit(_ context.Context, _ int, next int) error {
	r.committed = append(r.committed, next)
	if next == 2 {
		r.cancel()
	}
	return nil
}

// A fetch error on another partition cannot discard a healthy record whose
// cursor already advanced: committing a later batch would make that loss durable.
func TestConsumerProcessesRecordsAlongsidePollError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		reader := &partialPollReader{cancel: cancel}
		db := NewMemDB()
		consumer := &Consumer{Reader: reader, DB: db}
		done := make(chan struct{})
		go func() { defer close(done); consumer.Run(ctx) }()
		<-done
		total, err := Total(t.Context(), db, "a")
		if err != nil || total != 3 {
			t.Fatalf("projection total %d, %v; want both records applied (3)", total, err)
		}
		if reader.polls != 2 || !slices.Equal(reader.committed, []int{1, 2}) {
			t.Fatalf("polls=%d commits=%v; want two polls and offsets 1,2", reader.polls, reader.committed)
		}
	})
}
