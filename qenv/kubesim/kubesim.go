// Package kubesim simulates Kubernetes clusters in a qenv simulation, for
// controllers written against controller-runtime's client. Each cluster is
// a controller-runtime fake client behind interceptors (the seam where
// Kubernetes' contract is enforced and its faults are injected), plus a
// Deployment controller that moves Deployments' status along.
//
// The contract, as the real API server and controller manager give it:
//   - a spec change bumps metadata.generation, and a write through the
//     main resource leaves the status as it was; the status catches up
//     later (a Deployment's status.observedGeneration lags, by a schedule
//     choice, "sched.kube.lag");
//   - every write bumps resourceVersion, and an update with a stale one
//     fails with a conflict (apierrors.IsConflict);
//   - a request can fail, or a write be applied with its response lost,
//     and the caller cannot tell them apart: the fault kind
//     "kube.<cluster>.request" ("unavailable", "lostResponse"; a read only
//     "unavailable"), "kube" covering every cluster's.
//
// A write is reported where it takes effect (Cluster.OnWrite), before its
// response, which may be lost: the place to record it as a model step,
// whether or not the writer hears of it. Writes are the rollout's unless
// their context says another writer made them (AsWriter): an autoscaler,
// a person with kubectl, a replayed production workload.
//
// What is not simulated: watches with their faults (a Watch reaches the
// fake client as it is), admission, finalizers' and owners' garbage
// collection, pods. Deployments roll out one replica at a time, one
// Deployment at a time per cluster.
//
// kubesim is a module of its own (it needs client-go and
// controller-runtime, which the core does not): go get
// github.com/chucklehead-dev/gotogether/qenv/kubesim.
package kubesim

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// ErrUnavailable is a request that failed in the network or the API
// server; for a write, it may or may not have been applied.
var ErrUnavailable = errors.New("kubesim: the API server did not answer")

// Cluster is a simulated cluster: its API server and its Deployment
// controller.
type Cluster struct {
	Name string
	// Client is what controllers use: every request a scheduling point and
	// a fault choice.
	Client client.Client
	// PodTimes, if set, are the options of the time a replaced pod takes
	// to become ready ("sched.kube.pod"; the first, nominal, the shortest):
	// a production trace's, say (sktrace.Quantiles of its Lifetimes,
	// scaled). Unset: 100 to 300 ms.
	PodTimes []time.Duration
	// OnWrite, if set, is called in the writer's task for every write the
	// API server applies through Client, as it lands: before its response,
	// which may then be lost.
	OnWrite func(ctx context.Context, w Write)

	raw     client.WithWatch
	scheme  *runtime.Scheme
	sim     *qenv.Sim
	request qenv.FaultKind
}

// Write is a write the API server applied.
type Write struct {
	// Verb is "create", "update", "patch" or "delete".
	Verb string
	// Subresource is the subresource written ("status"), or "" for the
	// object itself.
	Subresource string
	// Object is the object as written: its new resourceVersion and
	// generation (for a delete, as the caller passed it).
	Object client.Object
	// Writer is the context's writer (AsWriter), "" for the default one.
	Writer string
}

// NewCluster creates a cluster holding objs, with scheme's types (nil:
// client-go's), and starts its Deployment controller as a process that
// never crashes. The status of Deployments, and of every type in objs, is
// a subresource, as on a real API server.
func NewCluster(s *qenv.Sim, name string, scheme *runtime.Scheme, objs ...client.Object) *Cluster {
	if scheme == nil {
		scheme = clientgoscheme.Scheme
	}
	status := append([]client.Object{&appsv1.Deployment{}}, objs...)
	for _, o := range objs {
		if o.GetGeneration() == 0 {
			o.SetGeneration(1)
		}
	}
	raw := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(status...).WithObjects(objs...).Build()
	c := &Cluster{Name: name, raw: raw, scheme: scheme, sim: s,
		request: qenv.DeclareFaults("kube."+name+".request", "unavailable", "lostResponse")}
	read := c.request.Only("unavailable")
	c.Client = interceptor.NewClient(raw, interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			qenv.Yield(ctx)
			if s.FaultOf(read) != "" {
				return ErrUnavailable
			}
			return cl.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			qenv.Yield(ctx)
			if s.FaultOf(read) != "" {
				return ErrUnavailable
			}
			return cl.List(ctx, list, opts...)
		},
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			return c.write(ctx, Write{Verb: "create", Object: obj}, func() error {
				if obj.GetGeneration() == 0 {
					obj.SetGeneration(1)
				}
				return cl.Create(ctx, obj, opts...)
			})
		},
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			return c.write(ctx, Write{Verb: "update", Object: obj}, func() error { return c.update(ctx, obj, opts...) })
		},
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
			return c.write(ctx, Write{Verb: "patch", Object: obj}, func() error { return c.patch(ctx, obj, p, opts...) })
		},
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			return c.write(ctx, Write{Verb: "delete", Object: obj}, func() error { return cl.Delete(ctx, obj, opts...) })
		},
		SubResourceUpdate: func(ctx context.Context, cl client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			return c.write(ctx, Write{Verb: "update", Subresource: sub, Object: obj}, func() error {
				return cl.SubResource(sub).Update(ctx, obj, opts...)
			})
		},
		SubResourcePatch: func(ctx context.Context, cl client.Client, sub string, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
			return c.write(ctx, Write{Verb: "patch", Subresource: sub, Object: obj}, func() error {
				return cl.SubResource(sub).Patch(ctx, obj, p, opts...)
			})
		},
	})
	s.Process(name+".kcm", false, c.deploymentController)
	return c
}

// write makes a write's fault choice and applies it: refused, applied, or
// applied with its response lost.
func (c *Cluster) write(ctx context.Context, w Write, apply func() error) error {
	qenv.Yield(ctx)
	f := c.sim.FaultOf(c.request)
	if f == "unavailable" {
		return ErrUnavailable
	}
	if err := apply(); err != nil {
		return err
	}
	if c.OnWrite != nil {
		w.Writer, _ = ctx.Value(writerKey{}).(string)
		c.OnWrite(ctx, w)
	}
	if f == "lostResponse" {
		return ErrUnavailable // applied; the response is lost
	}
	return nil
}

// update applies an update as the API server does: a stale
// resourceVersion conflicts, the generation bumps when the spec changes,
// and the status is not touched.
func (c *Cluster) update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	cur, err := c.current(ctx, obj)
	if err != nil {
		return err
	}
	if obj.GetResourceVersion() != cur.GetResourceVersion() {
		return apierrors.NewConflict(c.resource(obj), obj.GetName(),
			fmt.Errorf("resourceVersion %q, current %q", obj.GetResourceVersion(), cur.GetResourceVersion()))
	}
	settle(obj, cur)
	return c.raw.Update(ctx, obj, opts...)
}

// patch applies a patch, then the generation and status rules as update
// does: when they change the patched object, it is written again (its
// resourceVersion moves twice, which only its order means anything of).
func (c *Cluster) patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
	cur, err := c.current(ctx, obj)
	if err != nil {
		return err
	}
	if err := c.raw.Patch(ctx, obj, p, opts...); err != nil {
		return err
	}
	before := obj.DeepCopyObject().(client.Object)
	settle(obj, cur)
	if equality.Semantic.DeepEqual(before, obj) {
		return nil
	}
	return c.raw.Update(ctx, obj)
}

// current is obj as stored.
func (c *Cluster) current(ctx context.Context, obj client.Object) (client.Object, error) {
	cur := obj.DeepCopyObject().(client.Object)
	if err := c.raw.Get(ctx, client.ObjectKeyFromObject(obj), cur); err != nil {
		return nil, err
	}
	return cur, nil
}

// settle gives obj, written over cur through the main resource, the
// generation and status the API server would: cur's generation, one more
// if the spec changed, and cur's status.
func settle(obj, cur client.Object) {
	gen := cur.GetGeneration()
	if !equality.Semantic.DeepEqual(part(obj, "Spec"), part(cur, "Spec")) {
		gen++
	}
	obj.SetGeneration(gen)
	if u, ok := obj.(*unstructured.Unstructured); ok {
		if st, ok := cur.(*unstructured.Unstructured).Object["status"]; ok {
			u.Object["status"] = runtime.DeepCopyJSONValue(st)
		} else {
			delete(u.Object, "status")
		}
		return
	}
	if f := reflect.ValueOf(obj).Elem().FieldByName("Status"); f.IsValid() && f.CanSet() {
		f.Set(reflect.ValueOf(part(cur, "Status")))
	}
}

// part is a typed object's field (Spec, Status) or an unstructured one's
// (spec, status); nil if it has none.
func part(obj client.Object, field string) any {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		return u.Object[strings.ToLower(field)]
	}
	if f := reflect.ValueOf(obj).Elem().FieldByName(field); f.IsValid() {
		return f.Interface()
	}
	return nil
}

func (c *Cluster) resource(obj client.Object) schema.GroupResource {
	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return schema.GroupResource{Resource: "unknown"}
	}
	return schema.GroupResource{Group: gvk.Group, Resource: strings.ToLower(gvk.Kind) + "s"}
}

type writerKey struct{}

// AsWriter marks ctx's writes as another writer's (an autoscaler, a person
// with kubectl): Write.Writer names it.
func AsWriter(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, writerKey{}, name)
}

// Stored reads key's object as stored, outside the simulation's faults
// and scheduling: what a test or a final audit sees.
func (c *Cluster) Stored(key client.ObjectKey, obj client.Object) error {
	return c.raw.Get(context.Background(), key, obj)
}

// NewDeployment is a Deployment of replicas pods of image, rolled out at
// generation 1: its pods labelled app=<name>, its status up to date.
func NewDeployment(key client.ObjectKey, image string, replicas int32) *appsv1.Deployment {
	labels := map[string]string{"app": key.Name}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name, Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: key.Name, Image: image}}},
			},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: replicas, UpdatedReplicas: replicas,
			AvailableReplicas: replicas, ReadyReplicas: replicas},
	}
}

// deploymentController moves each Deployment's status toward its spec, in
// name order: it observes a new generation after a lag, then replaces the
// pods one at a time (one unavailable during the rollout).
func (c *Cluster) deploymentController(ctx context.Context) {
	qenv.Daemon(ctx) // kube-controller-manager's loop runs forever
	for {
		qenv.Sleep(ctx, 100*time.Millisecond)
		var ds appsv1.DeploymentList
		if err := c.raw.List(ctx, &ds); err != nil {
			continue
		}
		slices.SortFunc(ds.Items, func(a, b appsv1.Deployment) int {
			return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
		})
		for i := range ds.Items {
			if d := &ds.Items[i]; d.Status.ObservedGeneration < d.Generation {
				c.rollOut(ctx, client.ObjectKeyFromObject(d))
			}
		}
	}
}

// rollOut takes key's Deployment through its new generation.
func (c *Cluster) rollOut(ctx context.Context, key client.ObjectKey) {
	d := &appsv1.Deployment{}
	if err := c.raw.Get(ctx, key, d); err != nil {
		return
	}
	// status lag: the controller manager is busy, or its watch is behind
	qenv.Sleep(ctx, time.Duration(c.sim.Choose("sched.kube.lag", 6))*100*time.Millisecond)
	gen := d.Generation
	replicas := int32(1)
	if d.Spec.Replicas != nil {
		replicas = *d.Spec.Replicas
	}
	for updated := int32(0); updated <= replicas; updated++ {
		if err := c.raw.Get(ctx, key, d); err != nil || d.Generation != gen {
			return // a newer spec: the next pass starts again
		}
		d.Status.ObservedGeneration = gen
		d.Status.Replicas = replicas
		d.Status.UpdatedReplicas = updated
		d.Status.AvailableReplicas = replicas
		if updated < replicas {
			d.Status.AvailableReplicas = replicas - 1
		}
		d.Status.ReadyReplicas = d.Status.AvailableReplicas
		if err := c.raw.Status().Update(ctx, d); err != nil {
			return
		}
		if len(c.PodTimes) > 0 {
			qenv.Sleep(ctx, c.PodTimes[c.sim.Choose("sched.kube.pod", len(c.PodTimes))])
		} else {
			qenv.Sleep(ctx, time.Duration(1+c.sim.Choose("sched.kube.pod", 3))*100*time.Millisecond)
		}
	}
}

// Fleet is a set of simulated clusters, as a multicluster controller sees
// them (multicluster-runtime's clusters by name).
type Fleet struct{ Members []*Cluster }

// Names are the members' names, in order.
func (f *Fleet) Names() []string {
	var out []string
	for _, c := range f.Members {
		out = append(out, c.Name)
	}
	return out
}

// Cluster is the member named name.
func (f *Fleet) Cluster(name string) (*Cluster, error) {
	for _, c := range f.Members {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("kubesim: no cluster %q", name)
}

// Reader is the member's client, for reads.
func (f *Fleet) Reader(_ context.Context, name string) (client.Reader, error) {
	c, err := f.Cluster(name)
	if err != nil {
		return nil, err
	}
	return c.Client, nil
}

// Writer is the member's client, for writes.
func (f *Fleet) Writer(_ context.Context, name string) (client.Writer, error) {
	c, err := f.Cluster(name)
	if err != nil {
		return nil, err
	}
	return c.Client, nil
}
