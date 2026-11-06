# Go toolchain policy and admission

The shared minimum is **Go 1.27.3**. All live module/workspace directives,
scaffolds, test-module emitters, the consumable CI job and image default agree.
Preflight checks this alignment. Frozen adoption inputs and historical
measurements retain their original versions so their evidence remains reproducible.
This unreleased project supports one current runtime family; a port advances that
shared floor rather than adding parallel implementations for older toolchains.

The official `https://go.dev/dl/?mode=json` feed checked on 2026-11-05 lists
Go 1.27.3 as current stable. Its Linux/amd64 archive was checked against the
feed's size and SHA256 before local admission. Admission commands set
`GOTOOLCHAIN=local` and select the tested compiler explicitly, so automatic
selection cannot disguise the runtime actually exercised.

## Admission evidence

Both qgosim and the gosim example pin the published fork port
`v0.0.0-20261105184451-66f95e073358` (commit
`66f95e073358e556fd4f17e2953d30f2b963475f`). Admission selects the downloaded
module without a local replacement, and runs qgosim's automatic weaving before
translating its consumers.

Go 1.27.3 native admission covers memo/cache, subprocess cancellation, recording
ownership, native qenv contracts, real woven build/run fixtures, analyzer
acceptance, qhegel state-machine checks and both scaffold generators. Root vet,
preflight and all 40 workspace module tidy-diffs pass. All native short-suite
packages pass after provisioning toxiproxy for qnemesis. The full suite exposed a
step-log JSON parity mismatch for invalid UTF-8: the fast encoder now emits the
replacement rune as current encoding/json does, and all 20,000 generated parity
cases pass.

Published-fork acceptance exercises broadcast faithful runs, rejection controls
and deterministic replay, checkout faithful runs across all isolation levels and
both mutant suites, and automatically woven edgepublish faithful, invariant and
mutant checks. Native and simulated consumer vet covers both examples and
qgosim. These are semantic integration checks, not merely successful translation.

JSON discovery also exercises the current standard jsontext SkipValue path on
Go 1.27.3 without experiments. A translated public validate.IsManifest consumer
reads reordered/nested JSON and huge numbers in the simulated filesystem,
retains missing-file error identity and replays an emitted step exactly. The
existing deterministic broadcast replay also passes on the modified core and
the published fork. Normal encoding/json already brings jsontext/v2 into this
toolchain's graph; no fork-hook or dependency change is required.

The former bounded `current Go core` admission job is superseded by the ordinary
CI matrix running the shared 1.27.3 floor. Integration jobs remain in that matrix.
Preflight formats visible Go source without descending into hidden generated
trees such as gosim's `.gosim` output.

Before this port, the fork deliberately refused Go 1.27's standard library. A
module directive or dependency pin alone could not admit that compiler: the fork
needed matching runtime layouts, timer behavior, hashing and translated standard
library hooks. Keep that guard strict for every later runtime family.

## Current APIs and the next major port

Use current standard APIs where they improve implementation: typed
`errors.AsType`, iterators from `maps`/`slices`, `WaitGroup.Go`, `OnceFunc`,
`T.Context`, `B.Loop`, and `testing/synctest`. These are already used in the
repository. Memo tests use real contexts and `synctest.Wait` to establish durable
blocking. Do not wrap external process or network waits in a synctest bubble and
assume they become durably blocked.

The 2026-11-05 official-module audit identifies x/tools 0.52.0, x/mod 0.42.0,
x/sync 0.24.0 and staticcheck 0.8.2 as current. The subsequent bounded library
admission advances every live x/sys requirement to 0.49.0 and the external
Orchestrion tier to 1.15.0. Orchestrion and its tracing dependencies remain in
the edgepublish module; the core does not acquire them. Frozen rehearsal inputs
retain their captured dependency graphs.

x/sys admission checks atomic no-replace publication under the Linux race
checker, source snapshots, comparison export/replay and bundle publication.
Darwin and Windows atomic-publication test binaries compile; these checks do
not establish runtime behavior on those platforms. Go 1.27's ordinary os.Rename
still permits replacement, so exclusive publication uses the platform primitives.
Orchestrion 1.15.0 includes current compiler/link flag support. Admission runs
native and genuinely woven edgepublish tests plus qweave's independent fixture,
coverage and mutant suite. The existing sync fixture also exercises
sync.WaitGroup.Go through both weaving engines. This verifies the existing
modern API hook without adding another runtime facade.

A major floor increase requires a real gosim hook/standard-library port and
replay evidence first. Then check native qenv behavior, recording ownership,
subprocess lifecycle, both scaffold generators, woven coverage/mutation fixtures,
analyzer fixes and the real Orchestrion tier. Check all workspace module tidiness,
vet and preflight before publication. Do not remove integration checks, fake
GOVERSION, or add alternate source implementations merely to pass a new version.
