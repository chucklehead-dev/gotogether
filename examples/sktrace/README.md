# examples/sktrace: SimKube traces as workloads

A library, not an app: it reads [SimKube](https://github.com/acrlabs/simkube)
traces (msgpack, trace format v3, with its own decoder): what happened to
a real cluster's objects over time. It turns them into what a
simulation needs: the objects applied and deleted as timed writes against
a fake API server, the rates of events as a load shape (`qload.Piecewise`),
and pod lifetimes as quantiles (`sktrace.Quantiles`). A trace has no
actor and no causal order, so it is a workload to drive a run, never a
run to check against a model.

Used by [fleetrollout](../fleetrollout) (a production workload replayed
beside the controller, pods timed by the trace, and kwok Stages written
from it) and [retrystorm](../retrystorm) (a measured load shape for the
CTMC and the load simulation). `go test ./...` here runs its decoder's
and linter's tests against the traces in `testdata/`.
