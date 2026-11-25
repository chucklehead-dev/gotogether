package qobs

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// TestMemorySinkChunks checks the sink's layout, which no behaviour shows
// (only its allocations do): a short run is one slice grown by append, a
// long one is chunks made whole and filled before the next, each sized a
// quarter of the run so far within the bounds, so that no step is copied
// as the run grows and the unused room is at most a quarter of the steps.
func TestMemorySinkChunks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := &MemorySink{}
	for i := range 20000 {
		m.Record(ctx, &qtrace.Step{Seq: uint64(i)})
		if i+1 == memFirstChunk && (len(m.head) != memFirstChunk || m.rest != nil) {
			t.Fatalf("%d steps, %d of them in head and %d more chunks: a short run's steps are one slice", i+1, len(m.head), len(m.rest))
		}
	}
	chunks := append([][]qtrace.Step{m.head}, m.rest...)
	if len(chunks) < 3 {
		t.Fatalf("20000 steps in %d chunks: a long run's are chunked", len(chunks))
	}
	held, room := 0, 0
	for k, c := range chunks {
		if k == 0 && len(c) < memFirstChunk {
			t.Errorf("the first chunk holds %d steps, under %d", len(c), memFirstChunk)
		}
		if k > 0 {
			if cap(c) < memMinChunk || cap(c) > memMaxChunk {
				t.Errorf("chunk %d's capacity is %d, outside [%d, %d]", k, cap(c), memMinChunk, memMaxChunk)
			}
			if want := min(max(held/4, memMinChunk), memMaxChunk); cap(c) != want {
				t.Errorf("chunk %d's capacity is %d after %d steps, want %d", k, cap(c), held, want)
			}
		}
		if k < len(chunks)-1 && len(c) != cap(c) {
			t.Errorf("chunk %d holds %d of %d: a chunk is filled before the next is made", k, len(c), cap(c))
		}
		held += len(c)
		room += cap(c) - len(c)
	}
	if held != m.n || held != 20000 {
		t.Fatalf("chunks hold %d steps, n %d, recorded 20000", held, m.n)
	}
	if room > held/4 {
		t.Errorf("%d steps' room unused of %d held: over a quarter", room, held)
	}
}
