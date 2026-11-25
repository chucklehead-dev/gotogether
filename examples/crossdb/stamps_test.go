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
// reconciler makes (time.Now(), cut to the second) is stored as the
// virtual instant it was written, whatever fraction of a second the wall
// clock was at: kept, that fraction moved the end of the creation grace
// from run to run, and seed 1 run twice made two runs.
func TestStampsRoundTrip(t *testing.T) {
	s := qenv.New(qenv.NewSeeded(1))
	defer s.Close()
	st := &stamps{s: s, handed: map[string]string{}}
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
		now := time.Now() //qenv:ok the reconciler's own clock, which the seam translates
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
		// crossplane-runtime writes whole seconds of the wall clock; the
		// stamp is the virtual instant of its write, with none of the wall
		// clock's fraction of a second
		if !got.Equal(s.Now()) {
			t.Errorf("a new stamp written at %v is stored as %v", s.Now(), got)
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

// A stamp handed out is judged by time.Since after the reconciler's calls
// to the cloud, when real time has passed that virtual time need not have.
// It is judged at its age rounded up to a second, so a grace of whole
// seconds decides as virtual time does however slow the machine was (up
// to a second): a stamp 29 s old reads as under 30 s, one a microsecond
// older as 30 s. Handed out at their exact ages, both read as under the
// grace, until the slack passed the second's last microsecond: seed 1 run
// twice then made a call to the cloud at 30 s in one run only. Two stamps
// of one second stay distinct and in order, and come back as they were.
func TestStampsJudgedLater(t *testing.T) {
	s := qenv.New(qenv.NewSeeded(1))
	defer s.Close()
	st := &stamps{s: s, handed: map[string]string{}}
	const grace = 30 * time.Second
	s.Spawn("reconciler", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Minute)
		at := func(age time.Duration) string { return s.Now().Add(-age).Format(time.RFC3339Nano) }
		young, old := at(grace-time.Second), at(grace-time.Second+time.Microsecond)
		db := &v1alpha1.Database{}
		db.SetAnnotations(map[string]string{meta.AnnotationKeyExternalCreatePending: young, meta.AnnotationKeyExternalCreateSucceeded: old})
		st.toReal(db)
		time.Sleep(5 * time.Millisecond) //qenv:ok real time passes during a reconcile's calls, virtual time need not
		if meta.ExternalCreateSucceededDuring(db, grace) {
			t.Error("a stamp over 29 s old, judged after a few real milliseconds, reads as within the 30 s grace")
		}
		if d := time.Since(meta.GetExternalCreatePending(db)); d >= grace { //qenv:ok the check crossplane-runtime and the provider make, on the real clock
			t.Errorf("a stamp 29 s old, judged after a few real milliseconds, reads as %v old", d)
		}
		// pending a third of a second after succeeded, in one second
		db.SetAnnotations(map[string]string{meta.AnnotationKeyExternalCreatePending: at(1200 * time.Millisecond), meta.AnnotationKeyExternalCreateSucceeded: at(1500 * time.Millisecond)})
		st.toReal(db)
		if p, ok := meta.GetExternalCreatePending(db), meta.GetExternalCreateSucceeded(db); !p.After(ok) {
			t.Errorf("handed out, a pending stamp after its succeeded one reads as %v, the succeeded %v", p, ok)
		}
		st.toVirtual(db)
		if got := db.GetAnnotations()[meta.AnnotationKeyExternalCreatePending]; got != at(1200*time.Millisecond) {
			t.Errorf("written back unchanged, the pending stamp moved to %s", got)
		}
		if got := db.GetAnnotations()[meta.AnnotationKeyExternalCreateSucceeded]; got != at(1500*time.Millisecond) {
			t.Errorf("written back unchanged, the succeeded stamp moved to %s", got)
		}
	})
	if err := s.Run(time.Hour); err != nil {
		t.Fatal(err)
	}
}

// Stamps the reconciler makes in one wall-clock second, for two resources
// half a second of virtual time apart, are the same string. Each is
// stored as the virtual instant of its own write: looked up by its
// string, the second took the first's virtual time, unless the wall clock
// crossed a second between the two writes, and the second resource's
// creation grace ended at another reconcile from run to run.
func TestStampsOfOneSecond(t *testing.T) {
	s := qenv.New(qenv.NewSeeded(1))
	defer s.Close()
	st := &stamps{s: s, handed: map[string]string{}}
	s.Spawn("reconciler", func(ctx context.Context) {
		now := time.Now() //qenv:ok the reconciler's own clock, which the seam translates
		first, second := &v1alpha1.Database{}, &v1alpha1.Database{}
		meta.SetExternalCreatePending(first, now)
		st.toVirtual(first)
		written := s.Now()
		qenv.Sleep(ctx, 500*time.Millisecond) // the next resource's reconcile
		meta.SetExternalCreatePending(second, now)
		st.toVirtual(second)
		for _, c := range []struct {
			db *v1alpha1.Database
			at time.Time
		}{{first, written}, {second, s.Now()}} {
			got, err := time.Parse(time.RFC3339Nano, c.db.GetAnnotations()[meta.AnnotationKeyExternalCreatePending])
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(c.at) {
				t.Errorf("a stamp written at %v is stored as %v", c.at, got)
			}
		}
	})
	if err := s.Run(time.Hour); err != nil {
		t.Fatal(err)
	}
}
