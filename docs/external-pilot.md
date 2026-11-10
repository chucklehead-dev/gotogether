# External pilot: one recorded operation

Use one existing Go service and one real maintainer to test the path from a
recorded operation to a useful, reproducible finding. Run this alongside the
remaining nightly recovery work. The pilot establishes evidence for its chosen
contract and capture; it does not establish production readiness for Grist or
completion of the framework's verification campaigns.

## Agree the contract and inputs

Record the target repository/revision, operation, intended allowed behavior and
observations needed to judge it. Start with an ordered finite history. Choose a
small capture that can be shared unchanged, and identify missing observations
before interpreting a verdict. A model should express the intended contract,
not be weakened until an observed implementation passes.

Pin a merged toolkit revision whose applicable CI and selected workflow pass.
The service that imports gotogether must use Go 1.27.3 or newer, even if it
only records steps in production. The toolkit also requires Go 1.27.3; managed
Quint work requires stable Quint 0.33.1 or newer. The incident walkthrough uses jq. The [toolchain policy](go-toolchain.md) owns these
requirements. Keep the target-service revision separate from the toolkit pin.

The maintainer should use published instructions for the initial attempt.
Record any assistance or undocumented repair rather than silently doing it for
them. An existing controlled example is preparation, not independent adoption.

## Exercise the workflow

1. Record actual service operations using the existing recorder or supported
   input conversion, and author or adapt a binding for the chosen contract.
   [Production validation](tutorials/production-at-volume.md) introduces the
   validation and compiled-model path.
2. Validate a faithful capture and a deliberate semantic violation. Require the
   violation to identify the intended captured record and check, not an earlier
   setup or unrelated model failure. Label planted violations as controls.
3. Inspect native JSON and navigation output. Keep pass, failure, error and
   no-verdict distinct: exit 1 alone is insufficient to identify a model defect.
   Use actor, recorder and sequence for captured-record identity; source paths,
   display text and cursor positions serve different purposes.
4. Export the native evidence bundle, relocate it, and ask a second person to
   replay and explain the result using the supplied instructions. Refuse changed
   or missing dependencies. The
   [jobqueue incident walkthrough](../examples/jobqueue/INCIDENT.md) exercises
   faithful, planted-incident, repaired, relocation and integrity controls.
5. If compiled validation is part of the selected workflow, verify its supported
   model and explicit no-verdict behavior separately. The current jobqueue
   bundle walkthrough requires Go and Quint after relocation; it does not
   demonstrate offline bundle parity.

## Decide from the results

A useful first result is a maintainer who can produce the capture and binding,
understand the structured verdict, and hand reproducible evidence to someone
else without editing toolkit source. Keep the original reports, model/binding,
revision/tool pins and bundle, plus a short account of setup time, confusing
terms, assistance, unexpected outcomes and the recipient's explanation.

Wrong verdicts, hidden observation gaps, lost evidence integrity or undocumented
manual repairs block that workflow's handoff until corrected. Inventory's native
proof runtime and unrelated simulator survivor counts remain separate J7 gates;
they do not qualify or invalidate this finite-history pilot by themselves.
Reorder the next usability work from actual maintainer friction rather than
counting another agent rehearsal as an external trial.
