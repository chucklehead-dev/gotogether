package crossdb

import (
	"context"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"

	"github.com/chucklehead-dev/gotogether/examples/crossdb/apis/v1alpha1"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// A create-pending stamp the reconciler reads and writes back unchanged is
// stored as it was, however much virtual time passed in between: the grace
// period counts from the create, not from its last write-back. A stamp the
// reconciler makes keeps its fraction of a second.
func TestStampsRoundTrip(t *testing.T) {
	s := qenv.New(qenv.NewSeeded(1))
	defer s.Close()
	st := &stamps{s: s, virtual: map[string]string{}}
	stored := "2026-01-01T00:00:10.25Z"
	s.Spawn("reconciler", func(ctx context.Context) {
		db := &v1alpha1.Database{}
		db.SetAnnotations(map[string]string{meta.AnnotationKeyExternalCreatePending: stored})
		for range 5 {
			st.toReal(db)
			qenv.Sleep(ctx, 7*time.Second+300*time.Millisecond) // a reconcile's calls to the cloud
			st.toVirtual(db)
			if got := db.GetAnnotations()[meta.AnnotationKeyExternalCreatePending]; got != stored {
				t.Fatalf("written back unchanged, the stamp moved from %s to %s", stored, got)
			}
		}
		// crossplane-runtime's pending and succeeded stamps of one create:
		// the same wall-clock second, written with no virtual time between, each
		// write converted to virtual time and back (the interceptor's defer)
		now := s.ToReal(s.Now().Add(1500 * time.Millisecond))
		meta.SetExternalCreatePending(db, now)
		st.toVirtual(db)
		pending := db.GetAnnotations()[meta.AnnotationKeyExternalCreatePending]
		st.toReal(db)
		time.Sleep(time.Millisecond) //qenv:ok real time passes between the two writes, virtual time need not
		meta.SetExternalCreateSucceeded(db, now)
		st.toVirtual(db)
		if meta.ExternalCreateIncomplete(db) {
			t.Error("a create's pending and succeeded stamps of one second read as an incomplete create")
		}
		if got := db.GetAnnotations()[meta.AnnotationKeyExternalCreatePending]; got != pending {
			t.Errorf("the pending stamp moved from %s to %s when the succeeded one was written", pending, got)
		}
		got, err := time.Parse(time.RFC3339Nano, pending)
		if err != nil {
			t.Fatal(err)
		}
		// crossplane-runtime writes whole seconds; the translation must not
		// lose more than that
		if d := got.Sub(s.Now()); d < 500*time.Millisecond || d > 1500*time.Millisecond+10*time.Millisecond {
			t.Errorf("a new stamp 1.5 s ahead is stored %v from then", d)
		}
	})
	if err := s.Run(time.Hour); err != nil {
		t.Fatal(err)
	}
}
