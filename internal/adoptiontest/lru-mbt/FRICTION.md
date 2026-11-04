# Installed-module rehearsal: friction and limits

## Public setup

A scratch consumer pinned published SDK commit c7406027317a and
`github.com/hashicorp/golang-lru/v2 v2.0.7`, with no replace entries or Go
workspace. It compiled the public StateDriver surface and installed all 60
embedded skill Markdown files through a registered Go tool. The registered tool
survived tidy, and readonly tests and a subsequent tidy-diff passed.

An initial raw `go run .../cmd/gotogether` after library-only tidy lacked the
CLI-only checksums. The already documented `go get -tool .../cmd/gotogether`
then tidy path resolved this. It is the existing Go tool-registration pitfall,
not a new runtime-adapter defect. The packaged consumer includes the tool entry.

`connect.Test` takes one exact named run. An initial assumption that its name
was a regex generated no traces; five explicit calls now use the model's five
forced run names. The SDK's exact-match behavior was correct and actionable.
The unmodified faithful baseline then passed 32 native model-driven traces and
all five forced traces in 5.37 seconds on this workspace; this is a wall-time
observation, not a CI CPU regression budget.

## Configuration lesson incorporated in the SDK

The parent rehearsal identified malformed numeric environment values being
silently replaced by defaults. The packaged consumer is now pinned to SDK
commit 5fa07d7d103a, which returns a configuration error before invoking Quint.
A fresh external public-API probe of `(connect.Config{}).FromEnv()` with
`QUINT_MAX_STEPS=twenty-four` produced:

```text
connect: QUINT_MAX_STEPS="twenty-four": want a nonnegative integer (0 uses the default)
```

The cause and next action are explicit: provide a numeric bound such as `24`,
or `0` to select the documented default. This is setup evidence from the
published SDK, not a model counterexample or a defect in the cache. Explicit
nonzero Config fields retain their documented precedence over environment
fallbacks. The parent supplied the framework correction; no compatibility
parser or new runtime adapter was needed by this consumer.

## Observations and boundaries

The independent model is frozen separately. Driver snapshots use Keys and Peek
rather than Get, so observing state does not intentionally promote entries.
Returned values, recency order, item count and per-operation callback counts are
actual API observations. Model-only capacity is not mirrored by an observer
variable and presented as if it were an implementation observation.

Positive capacities 1–3 are the supported contract. Constructor nonpositive
sizes are rejected; an exploratory direct call showed Resize(0) empties and
disables retention, but that sparsely documented boundary was excluded before
modeling. No negative-size policy is inferred. Callback multiplicity is checked
without assuming callback order. No concurrency, complete graph, performance
claim or production defect is established.

The parent planted the source contrast after the faithful baseline and freeze
passed. A disposable dependency copy and alternate modfile omit only Get's hit
promotion, leaving the baseline, module-cache source and upstream checkout
unchanged. The Peek-based observer remains unchanged. Replaying the same native
trace yields a typed state mismatch at Get step 3 on oldest-to-newest Keys order;
compile, tool, trace-read and comparison-type errors cannot satisfy the control.
