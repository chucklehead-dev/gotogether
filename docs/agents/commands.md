# Commands by module

The commands of each module of its own, and the environment variables
the toolkit reads, for agents (and humans) working on gotogether.
AGENTS.md has the general requirements.

- edgepublish (its own module):
  ```sh
  cd examples/edgepublish
  go test ./...
  go run github.com/DataDog/orchestrion go test ./mbt/ ./scenarios/   # woven
  ```
- gosim (qgosim and examples/gosim, their own modules): `cd examples/gosim
  && GOWORK=off go test -p 1 -short ./...`, as CI runs it (the first run
  translates the standard library, about a minute). In the workspace
  (GOWORK unset) gosim's translation cannot find the module's packages
  and every test fails with "package translated/... is not in std". Check the simulated side with
  `go vet -tags sim`.
- qanalysis (its own module): `cd qanalysis && go test ./...` (the checkout
  comparison weaves and explores checkout: skipped with -short, needs quint).
- grpcledger and streams (their own modules): `cd examples/<name> && go test ./...`.
  grpcledger's generated protobuf code is checked in; regenerate with buf
  (no protoc: see its README).
- fleetrollout (its own module): `cd examples/fleetrollout && go test ./...`;
  `./kwok-demo.sh` needs kwokctl and kwok on PATH (`go install
  sigs.k8s.io/kwok/cmd/{kwokctl,kwok}@v0.7.0`), no Docker (binary runtime).
- crossdb (its own module): `cd examples/crossdb && go test ./...`; `kind/demo.sh`
  needs Docker, kind, kubectl and helm (`kind/setup.sh` installs Crossplane;
  `CA_BUNDLE=` for a TLS-intercepting proxy). CRD and deepcopy are generated:
  `go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0 object crd paths=./apis/... output:crd:dir=./package/crds`.
- Backend: `QUINT_BACKEND=rust|typescript`, for connect, validate and
  qtest alike (unset: rust for a single validation, typescript for a
  batch chunk of several traces, which it evaluates in one process where
  rust starts one evaluator per test; quint's own default for connect's
  `quint run`).
  `GOTOGETHER_EVAL=quint` checks a linearization's candidates with quint
  alone (by default the Go evaluator judges them, internal/qeval over the
  binding's evaluation module, compiled once into
  $TMPDIR/gotogether-eval/, and quint confirms the search's conclusion).
  `GOTOGETHER_KEEP=/dir` keeps generated modules and traces from qtest and
  qhegel. `GOTOGETHER_RUN='TestX#2:seed=n'` (or `:schedule=...`,
  `:corpus=<file>`, `:choices=<file>` for a steered run) runs one run of one exploration alone, the test's
  explorations numbered in order; each failing run's report prints it.
  `GOTOGETHER_EVIDENCE=dir` writes each exploration's evidence
  (`gotogether.evidence/2`, with its role: faithful, mutant, replay) as
  dir/<package>/<test>/x<N>.json (the package as its directory in its
  module, `_` the root), cleared per test, a run alone a replay; a System's `Mutant` marks a
  config's mutation so the suite's gaps leave it out.
  `GOTOGETHER_CORPUS=dir`
  replays and saves explorations' corpus entries under dir/<test>;
  `GOTOGETHER_HEGEL_SEED=n|random` overrides qhegel searches' seeds
  (both nightly). `GOTOGETHER_MUTANT=<id>` turns one woven mutant on for
  the whole process (`=list` prints them; ci/mutate-core.sh).
  `GOTOGETHER_ALL_FAULTS=1` turns every fault family on in
  every simulation (clock faults, stalls, a partition nemesis between
  every two processes): a suite written before a family meets it. A
  failure's reproduce line sets it, and its corpus entries go to an
  `all-faults` subdirectory (their choices replay only so). Exhaust
  under it searches every family's faults: bound its Faults. `GOTOGETHER_VERBOSE=1` shows
  Hegel's output for expected failures (FindViolation and Shrink capture
  os.Stdout while Hegel runs, since Hegel writes nowhere else).
