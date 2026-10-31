# Finite internal model choices

Ordinary trace validation can explain a recorded run through finite internal
`nondet` and `any` choices. No binding annotation or second input format is needed.
The validator retains whole paths across observed steps: a locally matching
choice may make a later call impossible. One path must explain every observation
and satisfy all applicable invariants and settled eventual checks together.
Separate paths cannot be combined into a passing report.

Initialization can also make finite choices. The validator enumerates distinct
legal initial states, then retains each root's complete property history through
the observations. A later read can select an initially unobserved leader without
exposing that model choice as a recorded runtime event. Paths that reach the
same later state cannot erase an initial invariant violation or combine
separately satisfiable properties into one passing explanation.

## Search and replay establish different facts

The Go evaluator enumerates finite successors. An exhaustive search with no
matching path establishes rejection; an exhaustive search with no path satisfying
the joint property set establishes the reported property conflict. These negative
conclusions are Go enumeration results, not independent Quint proofs of absence.

For a selected matching path, the validator specializes the original action IR
with concrete choices. It retains domains, guards and assignments, then asks Quint
to confirm the original observations and exact initial/intermediate states.
Assigning an evaluator-produced post-state directly would not establish that the
original action admits it. The selected path's property results must also agree.
A confirmed representative path does not independently prove a global negative
conclusion. All three evaluation policies (`go`, `quint`, `shadow`) use this
explicit search-and-confirmation procedure for supported internal choices.

## Bounds and current scope

`validate.Options.ChoiceLimit` and CLI `-choice-limit` bound expanded successor
states, including every distinct initial state. Zero resolves to 20,000; negative values are
invalid. Equivalent successors from one parent count once. Expansion is counted
before filtering observations. The limit is not a time or memory bound: successor
enumeration happens first and has the evaluator's separate finite-domain limits.
Exhaustion yields no verdict and cannot become a mutant kill or a reproduced
failure. The effective limit is preserved in manifests, profiles and cache keys;
manifest replay rejects an override.

This slice requires an ordinary complete trace and finite initialization.
The original initializer is pinned and confirmed by Quint, including its guards
and choice domains, rather than replaced by assignments of a computed state.
An initializer with no admitted state receives an explicit unsupported result:
there is no selected path to confirm. This slice does not add pinned-choice
support to history-order search, hidden
actions, partitioned validation, snapshots/resume, `within` checks, missing/gapped
records or assumed effects. Existing hidden/resume search remains separate.
Compiled-only validation cannot perform Quint confirmation and refuses internal
choices, while deterministic actions remain usable. Unrenderable IR, ambiguous
imported-variable identities and evaluator limits likewise produce an explicit
unsupported result. Local operator application, variant constructors/matches and
variant literal parameters/choices are among the current rendering limits.

## Evidence available to people and agents

Result/document/receipt `choices` metadata records `status` (`matched`, `rejected`,
`exhausted`, `unsupported`), reason, limit, explored count, exhaustive flag and
`confirmation` (`path_confirmed`, `not_performed`). A witness contains each
original action's pinned expression and observed recorder/sequence identity.
These identify the observed input, not evidence that runtime code made the model's
internal choice. JSON retains the structured data; text and TUI receipts show
search scope and confirmation separately from history-order search.
The separate `choices.initialization` object contains the original initializer's
action and pinned expression. It has no observed row, recorder or sequence:
it is a model explanation, not a runtime observation. JSON retains the full
expression; text and TUI receipts show a bounded preview.

Cursor model exploration is separate from the whole-run receipt. It still
requires an unambiguous initial state and may explicitly refuse a choosing
initializer even when validation found and confirmed a matching path. The
receipt remains available; the cursor never silently chooses the first root.

Keep observable outcomes as model parameters when they are part of the public
contract. Finite internal choices are useful for genuinely unobserved model
state; they need not be exposed solely to avoid random replay. Binding lint
identifies choice sites and describes bounded support without promising every
model or validation mode is supported.
