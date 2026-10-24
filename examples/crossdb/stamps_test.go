package crossdb

import (
	"context"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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

// crossplane-runtime's critical annotation updater keeps the annotations
// it writes and, on a conflict (a client deleted the resource during its
// create), gets the resource and adds them back. The stamps it adds back
// are the real ones it was handed, and are stored as the virtual ones
// they stand for: once converted in place, the kept map held virtual
// stamps, converted a second time, and the create-pending stamp was
// months old.
func TestStampsSurviveConflictRetry(t *testing.T) {
	seeded := qenv.NewSeeded(1)
	seeded.Rates = qenv.NoFaults
	s := qenv.New(seeded)
	defer s.Close()
	raw := fake.NewClientBuilder().WithScheme(Scheme).WithStatusSubresource(&v1alpha1.Database{}).Build()
	kube := kubeAPI(s, raw)
	s.Spawn("reconciler", func(ctx context.Context) {
		if err := raw.Create(ctx, NewDatabase("default", "db1")); err != nil {
			t.Fatal(err)
		}
		db := &v1alpha1.Database{}
		key := client.ObjectKey{Namespace: "default", Name: "db1"}
		if err := kube.Get(ctx, key, db); err != nil {
			t.Fatal(err)
		}
		start := s.Now()
		meta.SetExternalCreatePending(db, s.ToReal(start))
		if err := kube.Update(ctx, db); err != nil {
			t.Fatal(err)
		}
		qenv.Sleep(ctx, time.Second) // the create's call to the cloud
		// meanwhile, a client changes the resource: the next update conflicts
		other := &v1alpha1.Database{}
		if err := raw.Get(ctx, key, other); err != nil {
			t.Fatal(err)
		}
		other.SetLabels(map[string]string{"touched": "yes"})
		if err := raw.Update(ctx, other); err != nil {
			t.Fatal(err)
		}
		meta.SetExternalCreateSucceeded(db, s.ToReal(s.Now()))
		if err := managed.NewRetryingCriticalAnnotationUpdater(kube).UpdateCriticalAnnotations(ctx, db); err != nil {
			t.Fatal(err)
		}
		stored := &v1alpha1.Database{}
		if err := raw.Get(ctx, key, stored); err != nil {
			t.Fatal(err)
		}
		// both written in this second of virtual time, give or take
		// crossplane-runtime's whole seconds (one real second may stand
		// for both, as in TestStampsRoundTrip)
		for _, k := range []string{meta.AnnotationKeyExternalCreatePending, meta.AnnotationKeyExternalCreateSucceeded} {
			got, err := time.Parse(time.RFC3339Nano, stored.GetAnnotations()[k])
			if err != nil {
				t.Fatal(err)
			}
			if got.Before(start.Add(-time.Second)) || got.After(s.Now().Add(time.Second)) {
				t.Errorf("%s stored at %v, the stamps were written from %v to %v", k, got, start, s.Now())
			}
		}
	})
	if err := s.Run(time.Hour); err != nil {
		t.Fatal(err)
	}
}
