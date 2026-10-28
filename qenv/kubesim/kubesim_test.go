package kubesim_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/kubesim"
)

var web = client.ObjectKey{Namespace: "default", Name: "web"}

// script makes each named fault kind's choice once, as the option given,
// and every other choice nominal (no status lag, the shortest pod time).
type script map[string]int

func (s script) Choose(kind string, n int) int {
	k := strings.TrimPrefix(kind, "fault.")
	if o, ok := s[k]; ok && qenv.IsFault(kind) {
		delete(s, k)
		return o
	}
	return 0
}

// run starts a cluster "c1" holding web at v1 (3 replicas) in a
// simulation chosen by c, runs fn as a task, and runs the simulation for
// d of virtual time.
func run(t *testing.T, c qenv.Chooser, d time.Duration, fn func(ctx context.Context, cl *kubesim.Cluster)) *kubesim.Cluster {
	t.Helper()
	s := qenv.New(c)
	defer s.Close()
	cl := kubesim.NewCluster(s, "c1", nil, kubesim.NewDeployment(web, "web:v1", 3))
	s.Spawn("test", func(ctx context.Context) { fn(ctx, cl) })
	if err := s.Run(d); err != nil {
		t.Fatal(err)
	}
	return cl
}

func setImage(ctx context.Context, c client.Client, image string) (*appsv1.Deployment, error) {
	d := &appsv1.Deployment{}
	if err := c.Get(ctx, web, d); err != nil {
		return nil, err
	}
	d.Spec.Template.Spec.Containers[0].Image = image
	return d, c.Update(ctx, d)
}

// An update through the main resource: the generation bumps only with
// the spec, the status stays as it was (observedGeneration behind), and a
// stale resourceVersion conflicts.
func TestUpdateContract(t *testing.T) {
	var gen, observed, relabelled int64
	var stale error
	run(t, script{}, time.Second, func(ctx context.Context, cl *kubesim.Cluster) {
		old := &appsv1.Deployment{}
		if err := cl.Client.Get(ctx, web, old); err != nil {
			t.Fatal(err)
		}
		d := old.DeepCopy()
		d.Spec.Template.Spec.Containers[0].Image = "web:v2"
		d.Status.ObservedGeneration = 99 // through the main resource: not taken
		if err := cl.Client.Update(ctx, d); err != nil {
			t.Fatal(err)
		}
		gen = d.Generation
		if err := cl.Client.Get(ctx, web, d); err != nil {
			t.Fatal(err)
		}
		observed = d.Status.ObservedGeneration
		d.Labels = map[string]string{"team": "a"}
		if err := cl.Client.Update(ctx, d); err != nil {
			t.Fatal(err)
		}
		relabelled = d.Generation
		old.Spec.Replicas = ptr.To[int32](5)
		stale = cl.Client.Update(ctx, old)
	})
	if gen != 2 || observed != 1 || relabelled != 2 {
		t.Errorf("generation %d after the image, %d after a label; observed %d: want 2, 2 and 1", gen, relabelled, observed)
	}
	if !apierrors.IsConflict(stale) {
		t.Errorf("an update at a stale resourceVersion: %v, want a conflict", stale)
	}
}

// The status lags the spec, then the Deployment controller replaces the
// pods one at a time, one unavailable while it does.
func TestRollout(t *testing.T) {
	minAvailable := int32(3)
	cl := run(t, script{}, 5*time.Second, func(ctx context.Context, cl *kubesim.Cluster) {
		if _, err := setImage(ctx, cl.Client, "web:v2"); err != nil {
			t.Fatal(err)
		}
		for range 40 {
			d := &appsv1.Deployment{}
			_ = cl.Stored(web, d)
			minAvailable = min(minAvailable, d.Status.AvailableReplicas)
			qenv.Sleep(ctx, 50*time.Millisecond)
		}
	})
	d := &appsv1.Deployment{}
	if err := cl.Stored(web, d); err != nil {
		t.Fatal(err)
	}
	if s := d.Status; s.ObservedGeneration != 2 || s.UpdatedReplicas != 3 || s.AvailableReplicas != 3 {
		t.Errorf("status %+v, want generation 2 rolled out", s)
	}
	if minAvailable != 2 {
		t.Errorf("at least %d available during the rollout, want 2 (one pod replaced at a time)", minAvailable)
	}
}

// A failed request is not applied; a write whose response is lost is,
// and OnWrite saw it land. The caller gets the same error for both.
func TestFaults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		option  int
		applied bool
	}{{"unavailable", 1, false}, {"lostResponse", 2, true}} {
		t.Run(tc.name, func(t *testing.T) {
			var writes []kubesim.Write
			var err error
			sc := script{}
			cl := run(t, sc, time.Second, func(ctx context.Context, cl *kubesim.Cluster) {
				cl.OnWrite = func(_ context.Context, w kubesim.Write) { writes = append(writes, w) }
				d := &appsv1.Deployment{}
				if err := cl.Client.Get(ctx, web, d); err != nil {
					t.Fatal(err)
				}
				sc["kube.c1.request"] = tc.option
				d.Spec.Template.Spec.Containers[0].Image = "web:v2"
				err = cl.Client.Update(ctx, d)
			})
			if !errors.Is(err, kubesim.ErrUnavailable) {
				t.Errorf("update: %v, want ErrUnavailable", err)
			}
			d := &appsv1.Deployment{}
			_ = cl.Stored(web, d)
			if got := d.Spec.Template.Spec.Containers[0].Image == "web:v2"; got != tc.applied || (len(writes) == 1) != tc.applied {
				t.Errorf("applied %v with %d writes reported, want applied %v", got, len(writes), tc.applied)
			}
		})
	}
	// a read cannot lose a response: its fault choice has one option
	var got error
	run(t, script{"kube.c1.request": 1}, time.Second, func(ctx context.Context, cl *kubesim.Cluster) {
		got = cl.Client.Get(ctx, web, &appsv1.Deployment{})
	})
	if !errors.Is(got, kubesim.ErrUnavailable) {
		t.Errorf("get: %v, want ErrUnavailable", got)
	}
}

// OnWrite names each write's verb and writer.
func TestOnWrite(t *testing.T) {
	var writes []kubesim.Write
	run(t, script{}, time.Second, func(ctx context.Context, cl *kubesim.Cluster) {
		cl.OnWrite = func(_ context.Context, w kubesim.Write) { writes = append(writes, w) }
		if _, err := setImage(ctx, cl.Client, "web:v2"); err != nil {
			t.Fatal(err)
		}
		other := kubesim.NewDeployment(client.ObjectKey{Namespace: "default", Name: "api"}, "api:v1", 1)
		if err := cl.Client.Create(kubesim.AsWriter(ctx, "kubectl"), other); err != nil {
			t.Fatal(err)
		}
		if err := cl.Client.Delete(ctx, other); err != nil {
			t.Fatal(err)
		}
	})
	var got []string
	for _, w := range writes {
		got = append(got, w.Verb+":"+w.Object.GetName()+":"+w.Writer)
	}
	if strings.Join(got, " ") != "update:web: create:api:kubectl delete:api:" {
		t.Errorf("writes %v", got)
	}
}

// A patch follows the update's rules: the generation bumps with the spec,
// and a status patched through the main resource is not taken.
func TestPatch(t *testing.T) {
	var gen, observed int64
	run(t, script{}, time.Second, func(ctx context.Context, cl *kubesim.Cluster) {
		d := &appsv1.Deployment{}
		d.Namespace, d.Name = web.Namespace, web.Name
		p := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"replicas":4},"status":{"observedGeneration":7}}`))
		if err := cl.Client.Patch(ctx, d, p); err != nil {
			t.Fatal(err)
		}
		gen, observed = d.Generation, d.Status.ObservedGeneration
	})
	if gen != 2 || observed != 1 {
		t.Errorf("generation %d, observed %d after a patch: want 2 and 1", gen, observed)
	}
}

// A fleet is its members by name.
func TestFleet(t *testing.T) {
	s := qenv.New(script{})
	defer s.Close()
	f := &kubesim.Fleet{}
	for _, n := range []string{"m1", "m2"} {
		f.Members = append(f.Members, kubesim.NewCluster(s, n, nil, kubesim.NewDeployment(web, "web:v1", 1)))
	}
	if got := strings.Join(f.Names(), ","); got != "m1,m2" {
		t.Errorf("names %s", got)
	}
	if r, err := f.Reader(context.Background(), "m2"); err != nil || r == nil {
		t.Errorf("Reader(m2): %v", err)
	}
	if _, err := f.Writer(context.Background(), "m3"); err == nil {
		t.Errorf("Writer(m3) found a member")
	}
}
