package ordersync

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/chucklehead-dev/gotogether/qenv/simlog"
)

type partialReader struct {
	cancel    context.CancelFunc
	polls     int
	committed []int
}

func (r *partialReader) Poll(ctx context.Context) ([]simlog.Record, error) {
	r.polls++
	switch r.polls {
	case 1:
		return []simlog.Record{{Partition: 0, Offset: 0, Value: []byte("1")}}, errors.New("another partition failed")
	case 2:
		return []simlog.Record{{Partition: 0, Offset: 1, Value: []byte("2")}}, nil
	default:
		r.cancel()
		return nil, ctx.Err()
	}
}
func (r *partialReader) Commit(_ context.Context, _ int, next int) error {
	r.committed = append(r.committed, next)
	if next == 2 {
		r.cancel()
	}
	return nil
}

func TestSyncRetainsRecordsReturnedWithPollError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		reader := &partialReader{cancel: cancel}
		done := make(chan struct{})
		go func() { defer close(done); Sync(ctx, reader) }()
		<-done
		if reader.polls != 2 || !slices.Equal(reader.committed, []int{1, 2}) {
			t.Fatalf("polls=%d commits=%v; want both returned batches handled before committing past them", reader.polls, reader.committed)
		}
	})
}
