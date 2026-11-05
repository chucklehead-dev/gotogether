# Go toolchain policy and admission

The live shared minimum is **Go 1.26.10**. This is the maintained patch of the
runtime family required by gosim's current hooks, not a promise to support older
customer toolchains. All live module/workspace directives, scaffolds, test-module
emitters, the consumable CI job and image default agree. Preflight checks this
alignment. Frozen adoption inputs and historical measurements keep their original
versions so their evidence remains reproducible.

The official `https://go.dev/dl/?mode=json` feed checked on 2026-11-05 lists
**Go 1.27.3** as current stable and **Go 1.26.10** as the maintained prior-family
release. Both official Linux/amd64 archives were checked against the feed's size
and SHA256 before local admission tests. Do not describe the installed 1.26.1
baseline as the latest release.

## Admission evidence

| Toolchain and scope | Result |
|---|---|
| 1.27.3: memo/cache, subprocess cancellation, recording ownership | passed |
| 1.27.3: native qenv runtime contracts, invariant checks on | passed |
| 1.27.3: real gosim broadcast deterministic replay | refused by the runtime-hook guard |
| 1.26.10: same real gosim deterministic replay | passed (83.236s cold translation/build) |
| 1.26.10 and 1.27.3: memo cancellation race/shuffle, 20 runs | passed |
| 1.26.10: native core, both scaffold generators, woven fixtures, analyzer acceptance | passed |
| 1.26.10: workspace module tidy-diff (40 modules), root vet and preflight | passed |

The separate `current Go core` CI job keeps checking ordinary core contracts on
1.27.3 with `GOTOOLCHAIN=local`, so that admission cannot silently switch compilers.
Local admission runs also pin their toolchain. Frozen Go1.27 adoption modules keep
the standard Go automatic selection they already require; their inputs are not
rewritten to the shared live floor. This is
bounded host-core admission, not a claim that every integration supports 1.27.
The usual matrix uses the shared 1.26.10 floor and exercises the integrations.
Preflight formats visible Go source files without descending into hidden generated
trees such as gosim's .gosim output; running native admission first must not make
that generated code a repository formatting failure.

The actual gosim refusal was:

```text
gosim: this gosim translates go1.26's standard library, not go1.27.3's: build it with go1.26 (GOTOOLCHAIN=go1.26.1), or use a gosim release for go1.27.3
```

The patch named by that upstream error is historical; use the tested maintained
1.26.10 patch. The latest gosim fork revision checked (`9ee1adcb9f4e`, alongside the pinned
`f3a3ad889291`) still declares Go1.26 support, so changing its dependency pin alone does not port the hooks. Raising
core's minimum to 1.27 would also break the current-core replacement in the gosim
example, even if qgosim's own directive stayed at 1.26. Keep the guard intact.

## Current APIs and the next major port

Use the baseline's current standard APIs where they improve the implementation:
typed `errors.AsType`, iterators from `maps`/`slices`, `WaitGroup.Go`, `OnceFunc`,
`T.Context`, `B.Loop`, and `testing/synctest`. These are already used in the
repository. The memo tests now use real contexts and `synctest.Wait` to establish
durable blocking; they need neither a custom context wrapper nor a real timeout.
Disabling waiter cancellation or canceled-owner retry makes those tests fail.
Do not wrap external process or network waits in a synctest bubble and assume
they become durably blocked.

The dependency audit found x/tools 0.52.0, x/mod 0.42.0, x/sync 0.24.0 and
staticcheck 0.8.2 current. x/sys 0.49.0 (currently 0.48.0) and Orchestrion 1.15.0 (currently 1.13.1) are
separate admission candidates, not evidence that a Go1.27 runtime-hook port already exists.

A major floor increase requires a real gosim hook/standard-library port and
replay evidence first. Then check native qenv behavior, recording ownership,
subprocess lifecycle, woven coverage/mutation fixtures, analyzer fixes and the
real Orchestrion tier. Do not remove an integration check, fake GOVERSION or add
alternate source implementations merely to make a newer version pass.
