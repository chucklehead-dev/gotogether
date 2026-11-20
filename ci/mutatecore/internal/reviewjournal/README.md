This private prerelease prototype validates the **structure** of the existing
mutatecore journal version 1. It does not change the journal producer, CLI,
workflow, count limits, policy ledger or `reviewpolicy.Admission.Check`.

`Validate` requires an externally expected execution configuration and complete
suite inventories. It derives the exact union and owner-first target order from
those inventories, then checks the journal's start, suites, successful baselines,
admitted union, resolved source attempts and terminal counts. A source can be
killed only at the first failed attempt; a survivor needs every linked suite to
pass. Outcome completion order is unrestricted because workers run concurrently.
Empty suite inventories are allowed; an empty campaign union is rejected.

All JSON fields use exact casing, duplicate keys are rejected recursively, and
required integers and Booleans must be explicit and non-null (including zero
and false). Per-suite empty inventories may be null, as the current producer
emits them that way. Required identities/targets/attempts are checked against
the independently supplied universe. Records need complete newline framing;
blank, oversized, torn, unknown, aborted or trailing records fail closed. A
result's private validation bit prevents its zero value from claiming completion.
Returned outcomes are copies. Backward wall-clock adjustments are allowed;
timestamps are syntactically valid and nonzero, not a proof of causality.

A structurally complete campaign whose survivor count exceeds its count limit
is a successful structural validation with `CountGatePassed` false. This does
not authorize any survivor, replace the existing count gate, or feed the policy
package's completeness Boolean. Diagnostic max_survivors=-1 is recognized from
the current producer and is explicitly not an identity-policy admission.

Current format gaps remain shipping blockers: journals contain no closed source
context, binary/toolchain hashes, or independent suite-discovery manifest.
Copying the expected manifest from the same journal checks internal consistency
only. A valid complete record cannot prove its own fsync, subsequent Close, or
successful enclosing process exit; the current producer writes complete before
its deferred Close. A reader cannot recover those missing provenance/durability
facts. Also, the producer's `failed` attempt includes resource-limit failures,
so a killing suite is not necessarily a behavioral assertion witness. Collector,
producer receipts and policy integration must settle these boundaries before
any admission migration. There is no adapter that sets policy completeness here.

Synthetic tests cover complete red evidence and adverse schema, phase,
identity, inventory, attempt and terminal cases. An optional read-only retained
campaign consistency check runs with `GOTOGETHER_REVIEW_JOURNAL_FIXTURE=path`;
its manifest is deliberately reconstructed from that same log and is never
presented as independently trusted admission evidence.
