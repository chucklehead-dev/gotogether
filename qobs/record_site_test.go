package qobs

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qrun"
)

type recordSiteStringer func() string

func (f recordSiteStringer) String() string { return f() }

// Attribute normalization can run caller code. Discard must be read after that
// code, even when finding the recorder already resolved the goroutine's run.
// Not parallel: the site hook is process-wide.
func TestRecordSiteIsFreshAfterNormalization(t *testing.T) {
	mem := &MemorySink{}
	rec := NewRecorder(mem)
	end := qrun.Start(WithRecorder(context.Background(), rec))
	defer end()
	discard := false
	restore := hooks.SetSite(func(_ context.Context, id int64) hooks.Site {
		return hooks.Site{Discard: discard, GoroutineID: id}
	})
	defer restore()
	Record(context.Background(), "interrupted", nil, "value", recordSiteStringer(func() string {
		discard = true
		return "normalized"
	}))
	if len(mem.Steps()) != 0 {
		t.Fatalf("a stale site published an interrupted completion: %+v", mem.Steps())
	}
	interrupted := rec.Interrupted()
	if len(interrupted) != 1 || interrupted[0].Action != "interrupted" || interrupted[0].Args["value"] != "normalized" {
		t.Fatalf("interrupted evidence: %+v", interrupted)
	}
}
