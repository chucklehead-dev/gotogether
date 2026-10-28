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
- webjobs (its own module): `cd examples/webjobs && go test -short ./...`
  runs the real server with a Go client and checks the recorded Bombadil
  runs (testdata/runs), faithful and per mutant overlay; `go test ./...` adds the live runs, which need
  `bombadil` (`npm i -g @antithesishq/bombadil@0.7.9`) and a Chrome on
  PATH (`CHROME=` names one; an older Chromium needs
  `BOMBADIL_ARGS="--chrome-grant-permissions local-network-access"`).
  `WEBJOBS_RECORD=1 go test -run 'TestLive$|TestMutants$' .` records 6s
  runs into testdata/runs; `WEBJOBS_TIME_LIMIT=10s` shortens a live run;
  `WEBJOBS_JOURNEYS=dir` keeps the journeys.
- grist (its own module): `cd grist && GOTOGETHER_CHECK=1
  GOTOGETHER_STRICT=1 go test ./...` (as CI; `TestNewIsGreen`, full mode,
  scaffolds a module with `grist new` and tests it). A grist app's
  generated model and bindings: `go run ./cmd/<app> model` in its
  module (`-init` writes a first model/<app>.qnt), or `GRIST_UPDATE=1 go
  test -run TestGenerated`; grist's own golden files:
  `GRIST_UPDATE=1 go test -run TestGenerateGolden .` in grist/.
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
  By default the Go evaluator (internal/qeval over the binding's
  evaluation module, compiled once into $TMPDIR/gotogether-eval/) decides
  every run it passes, with no quint process, and judges a
  linearization's candidates, quint confirming the search's conclusion;
  a run it rejects or refuses is quint's to judge and report
  (DESIGN §56). `GOTOGETHER_EVAL=quint` has quint judge every run and
  every candidate. `GOTOGETHER_EVAL=shadow` judges with both, every
  batch the evaluator judges checked by quint too, and each actor of
  every run checked (Check, CheckBatch) judged by both, its plan as the
  check plans it, and fails on a disagreement of a verdict, a passing
  result or an ITF state, naming the run (the nightly's
  `evaluator-shadow`; as slow as quint alone). `GOTOGETHER_SHADOW_LOG=file`
  appends a line per run so judged (`agree`, `DISAGREE`) or refused by
  the evaluator (`refused`, with why).
  validate keeps quint's typechecks and the evaluation modules' compiles
  across processes, by content (quint's version, the spec's and its
  imports' paths and contents, every .qnt beside them, the arguments),
  under the user's cache directory (`gotogether/quint`; DESIGN §57):
  `GOTOGETHER_CACHE=off` asks quint every time, `GOTOGETHER_CACHE=<dir>`
  keeps it there. An entry unused for a week is pruned.
  `GOTOGETHER_KEEP=/dir` keeps generated modules and traces from qtest and
  qhegel. `GOTOGETHER_RUN='TestX#2:seed=n'` (or `:schedule=...`,
  `:corpus=<file>`, `:choices=<file>` for a steered run) runs one run of one exploration alone, the test's
  explorations numbered in order; each failing run's report prints it.
  With it, `GOTOGETHER_NAV` (start, fail, `@k`, `<schedule>@k`) navigates
  that run instead (DESIGN §58): `GOTOGETHER_NAV_FOLLOW` options taken
  from the node, the node's JSON to `GOTOGETHER_NAV_OUT`,
  `GOTOGETHER_NAV_SAVE=1` its run to the corpus, `GOTOGETHER_NAV_FOLD=1`
  folds its linear stretch; `gotogether nav -run` sets them.
  qnav (its own module): `cd qnav && go test ./...` (hello's session
  skipped with -short); its tapes need vhs, ttyd and ffmpeg (qnav's
  README), `go test ./tapes/`, `tapes/render.sh` for the media.
  `GOTOGETHER_NAV=serve` is a session: `validate.RunRequest` lines from
  stdin (or the file `GOTOGETHER_NAV_IN`), `validate.RunResponse` lines
  to stdout (or `GOTOGETHER_NAV_OUT`) until the input ends; run the
  compiled test binary (`go test -c`), not `go test`, which hides a
  passing test's output.
  `GOTOGETHER_EVIDENCE=dir` writes each exploration's evidence
  (`gotogether.evidence/2`, with its role: faithful, mutant, replay) as
  dir/<package>/<test>/x<N>.json (the package as its directory in its
  module, `_` the root), cleared per test, a run alone a replay; a System's `Mutant` marks a
  config's mutation so the suite's gaps leave it out. Each Validate and
  Record run writes its manifest there too (`gotogether.run/2`,
  run<N>.json, N per test from 1, its files bundled in run<N>/: the step
  log, the binding with its spec and imports, the artifacts; `gotogether validate -replay` judges it again,
  `gotogether evidence` lists it as external).
  `GOTOGETHER_CORPUS=dir`
  replays and saves explorations' corpus entries under dir/<test>;
  `GOTOGETHER_HEGEL_SEED=n|random` overrides qhegel searches' seeds
  (both nightly). `GOTOGETHER_MUTANT=<id>` turns one woven mutant on for
  the whole process (`=list` prints them; ci/mutate-core.sh).
  `GOTOGETHER_STRICT=1` (or `true`; another value but `0` or `false`
  fails, naming it) turns every toolkit skip into a failure with the
  skip's message (qtest's RequireQuint, Mutate with no mutants woven,
  Record's and Explore's SkipIfEmpty, qhegel.Shrink's, the scaffold's
  unwritten mutant test) and `qtest.Optional(t, reason)` with them, a
  test's own gated skip; set in CI's jobs that install quint (ci.yml
  says which do not, and why). qhegel keeps its own copy of Optional, so
  it builds on the core it requires. The core's own tests skip through
  internal/skip (`skip.Quint`, `skip.Optional`), never a bare t.Skip
  for a missing tool; opt-in gates (`-short`, GOTOGETHER_VERIFY,
  RAFTKV_REAL) stay plain skips.
  `GOTOGETHER_ALL_FAULTS=1` turns every fault family on in
  every simulation (clock faults, stalls, a partition nemesis between
  every two processes): a suite written before a family meets it. A
  failure's reproduce line sets it, and its corpus entries go to an
  `all-faults` subdirectory (their choices replay only so). Exhaust
  under it searches every family's faults: bound its Faults. `GOTOGETHER_VERBOSE=1` shows
  Hegel's output for expected failures (FindViolation and Shrink capture
  os.Stdout while Hegel runs, since Hegel writes nowhere else).
- CI's time budgets (ci/test-times.txt) are CPU seconds: each test binary
  runs under internal/cputime (`go test -exec`), which appends its
  package directory, CPU seconds and wall seconds to the file
  `GOTOGETHER_CPU_TIMES` names, its children's time included (quint, node
  and its evaluator, go builds; orphans too, cputime being their
  subreaper on Linux), and `ci/time-budget.sh < file` fails a package
  costing more than twice its line. CPU time, because CI runs a module's
  packages at once on four cores: validate's wall time there was
  binding's and qtest's too, and five budgets were "fixed" in a day by
  raising them. To measure a package as CI does:
  ```sh
  go build -o /tmp/cputime ./internal/cputime
  GOTOGETHER_CPU_TIMES=/tmp/cpu.txt go test -exec /tmp/cputime -count=1 ./validate/
  ci/time-budget.sh < /tmp/cpu.txt; sort -k2,2nr /tmp/cpu.txt
  ```
  A deliberate change of a package's cost sets its line to the CPU
  seconds the job printed (each budgeted job prints its file sorted, CPU
  second column) with the reason after `#`; a laptop's numbers differ
  from CI's by its cores' speed, not by what runs beside the tests.
