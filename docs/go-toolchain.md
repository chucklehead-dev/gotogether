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


## Current library patches (2026-11-06)

The authoritative audit still selects Go 1.27.3, x/tools 0.52.0, x/mod 0.42.0,
x/sync 0.24.0, x/sys 0.49.0, Orchestrion 1.15.0 and staticcheck 0.8.2.
Controller-runtime 0.25.2 is current. Kubernetes staged dependencies advance
from 0.37.0 to 0.37.2 together across kubesim and its fleetrollout/crossdb
consumers. Their upstream SDK diffs change dependency manifests rather than
adding API fields. Admission exercises real client semantics, faithful runs and
existing defect witnesses; native execution does not establish kind/kwok server
behavior. All 40 live workspace modules remain tidy; frozen histories retain
their original graphs.

The current Quint pair is CLI **0.33.1** and Rust evaluator **0.7.1**. The CLI
patch fixes double-recording dead-end TypeScript runs: when all requested
samples are retained (`--n-traces == --max-samples`, as connect requests),
duplicate long traces could displace short sampled traces. A bounded terminal
fixture retains both initially terminal and longer samples, without requiring
legitimate repeated samples to be unique. The Rust IPC parser also accepts
valid variable names containing `constructor`; actual generation, decoding and
state comparison check that path. Both controls fail with the previous pair.
The upstream evaluator also documents a GNU libc floor of 2.31 rather than
2.39, relevant to the Bookworm image.

The evaluator has no version command. Its cache is validated against committed
archive and executable SHA256 pins, plus native startup, before reuse. All four
Linux/Darwin asset digests were checked against official GitHub release metadata;
binary digests were derived from those archives. Native execution admission is
Linux/amd64, not execution proof for the other three targets. Cache publication
is staged and atomic, preserving an existing executable on failure. npm CLI
installation precedes that transaction and is not itself rolled back. The image
copies the checksum file beside the installer so build-time startup checks apply.

**Hegel 0.9.30 is not admitted yet.** It advances embedded libhegel from 0.48 to
0.50 and permits shared membership in concurrency rule groups. Full qhegel
stateful/shrinking/reproduction tests (210.466s), native race controls and the
new group feature pass. However, the first full internal/hegelprops run failed
TestEqualKeysEqualVerdicts (338.931s overall) without a useful failure reason.
Isolated old/new comparisons and a diagnostic full rerun pass; that does not
explain or clear the original failure. No semantic counterexample or TooSlow
cause was captured. Hegel's Test can discard native run-error details by marking
the test failed without printing them; its io.Writer option is still private.
Keep the sole admitted 0.9.27 pin while investigating failure observability and
reproduction, rather than adding version fallbacks or suppressing health checks.

The complete connect suite with the new controls uses 17.9 descendant CPU
seconds in the local cputime harness (9.330s wall); its previously unlisted
package now has an 18-second baseline, retaining the existing 2x regression
factor. The first two executable hello tutorial transcripts still replay with
the new pair; dated transcript/compiler versions remain provenance.
