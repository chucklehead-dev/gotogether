# Go toolchain policy and admission

This local adoption branch prepares a shared minimum of **Go 1.27.3**. All live module/workspace directives,
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

The gosim fork port is validated locally through a copied consumer tree with a
local replacement. Publishing the fork is blocked by GitHub write access (403).
The checked-in gosim dependency pins deliberately remain unchanged: this branch
is preparation, not a publishable adoption PR. After the fork is published, pin
its real revision in qgosim and examples/gosim, rerun acceptance without local
replacements, and publish the complete adoption together. Completed native
checks on Go 1.27.3 include memo/cache, subprocess cancellation, recording
ownership, real woven build/run fixtures, analyzer acceptance and qhegel native
state-machine checks. Both scaffold generators, root vet, preflight and all 40
workspace module tidy-diffs passed. Local fork broadcast replay passed with 20
choices and 44 identical steps; full broadcast faithful and rejection checks also
passed. Local checkout faithful runs across all isolation levels and both mutant
suites passed, as did woven edgepublish faithful, invariant and mutant checks.
Native and simulated consumer vet passed. The full native short suite exposed a
step-log JSON parity mismatch for invalid UTF-8: the fast encoder now emits the
replacement rune as current encoding/json does, and all 20,000 generated parity
cases pass. All native short-suite packages passed after that correction and
provisioning toxiproxy for qnemesis. Acceptance against a published pin must pass
before publishing.

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

The dependency audit found x/tools 0.52.0, x/mod 0.42.0, x/sync 0.24.0 and
staticcheck 0.8.2 current. x/sys 0.49.0 (currently 0.48.0) and Orchestrion 1.15.0
(currently 1.13.1) remain separate admission candidates.

A major floor increase requires a real gosim hook/standard-library port and
replay evidence first. Then check native qenv behavior, recording ownership,
subprocess lifecycle, both scaffold generators, woven coverage/mutation fixtures,
analyzer fixes and the real Orchestrion tier. Check all workspace module tidiness,
vet and preflight before publication. Do not remove integration checks, fake
GOVERSION, or add alternate source implementations merely to pass a new version.
