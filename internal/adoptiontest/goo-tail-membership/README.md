# Tail-membership capability rehearsal

An agent-assisted, bounded model of Goo's public process-local
`TailMembershipSnapshots` API at revision
`0659a4b20fb7fe83bbfd32a194ceb9a7d0c339a9`. It checks ownership, read-anchored
expiry, immutable observations and atomic multi-reference selection. It does not
require ClickHouse, a browser or a persistent publication store.

The model author used `docs/TAIL_SELECTION_CUSTODY.md`, without reading Goo's
implementation or existing native tests/model. A separate observing driver calls
the actual `TailTracker.Observe`, `Freeze` and `Resolve` APIs. It records actual
returned capabilities, errors, requested references and complete drafts separately
from the model trace. Random tokens and basis hashes remain opaque identities.

The fixture contains 40 authored operations and 38 modeled steps; input/draft
mutation operations are followed by observed resolves. Four histories cover:

- Delayed issuance, one second before expiry, exact expiry and expired issuance.
- Two batches with different read clocks; mixed expired/live requests in both
  orders must return no partial draft, while the newer batch remains usable.
- Changed ownership, forged or duplicate occurrences, and repeated issuance of
  the same occurrence under another token.
- Two connections with otherwise identical selector scope.

Existing Goo Hegel/native tests already check scope, expiry, duplicates and exact
payloads. This trial adds a stateful model and end-to-end gotogether evidence
workflow; it does not establish that those contracts were previously untested,
independent-maintainer adoption, exhaustive verification or a production defect.

## Representation and freeze

The independent model computes expiry as original read time plus 120 seconds.
Input record/provenance expectations come from the trusted pre-Freeze batch,
not the returned draft. Issued basis identities are observed once and must survive
later resolution unchanged. The model compares complete member occurrences,
including body, window, read clock, basis and limited/late flags.

Quint strings cannot contain embedded double quotes. Record bytes therefore use
lossless base64 in the model, while complete native JSON responses remain in the
evidence. Go tests decode and compare byte-for-byte. The evidence checker compares
decoded record values and exact requested/returned opaque references. The fixture
uses whole seconds; the driver explicitly rejects subsecond output rather than
rounding it into a passing observation. This is not arbitrary-time validation.

`FREEZE.sha256` pins the independent model, binding, driver, stimuli and exact
published dependency versions after native validation and bundled replay pass.
No checkout replacements or module-cache edits are used for the faithful run.
Any later source contrast must leave these frozen files unchanged.

Freeze commit `ec382346` precedes the parent-planted source contrast. The contrast
changes just the expiry-pruning boundary from `!now.Before(expires)` to
`now.After(expires)` in a disposable copy of the pinned dependency. The unchanged
model rejects the actual returned expired proof at sequence 10,
`case:expired-at-boundary`. Source hashes and the exact edit are retained in each
run's `reports/PROVENANCE.json`. Neither the external checkout nor the module
cache is changed.

## Reproduce

Go with automatic Go 1.27 selection, Python 3 and Quint 0.33.0 are required.
From the gotogether checkout:

```sh
mkdir -p "$PWD/bin"
go build -o "$PWD/bin/gotogether" ./cmd/gotogether
PATH="$PWD/bin:$PATH" internal/adoptiontest/goo-tail-membership/reproduce.sh
```

The script verifies the freeze, Go representation tests and five Quint model
selftests. It records fresh actual API calls, validates with Quint/Rust and
bundles the faithful run before preparing changed source. The source contrast
must exit 1 with a native observed-state mismatch. Omitting one actual members
observation must exit 2 as an evidence error. Both faithful and failing bundles
then replay from another working directory after every original project path is
removed. Fresh artifacts remain under `logs/run-*` (ignored by Git).

Replay judges the captured observations; it does not execute Goo again. The
model checks acceptance/refusal, atomic draft presence and preserved data, not
the precise library rejection-error category. Raw error strings and categories
remain available separately. No graph or exhaustive state exploration is claimed.

Capacity/eviction, concurrent calls, arbitrary selectors, payload canonicalization,
durable retention, custody, authorization and completeness remain outside scope.
