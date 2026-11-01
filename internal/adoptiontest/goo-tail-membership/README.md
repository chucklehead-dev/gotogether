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

Current gate: real Goo execution, native representation checks, Quint/Rust
validation and bundled replay pass. Source contrast and reproducible relocated
replay controls are the next step after this baseline freeze.

Capacity/eviction, concurrent calls, arbitrary selectors, payload canonicalization,
durable retention, custody, authorization and completeness remain outside scope.
