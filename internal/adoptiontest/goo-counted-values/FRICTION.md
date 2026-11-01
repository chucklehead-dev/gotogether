# Observed setup and evidence lessons

The first generated replay used JSON-text collection fields as plain template
values. They correctly rendered as strings, and Quint rejected `str` versus
`List[int]`. Existing typed `toQuint "List[int]"` conversions solved this without
changing the model or adding a runtime adapter.

The Go evaluator's signed-int64 boundary was known. The first backend check also
demonstrated Quint 0.33.0 Rust's `QNT600` refusal of the large model literal.
Explicit Quint/TypeScript evaluation passed all five model tests, including a
multiset check. A passing test is not enough to establish serialization, so the
gate additionally read actual native JSON, passing ITF and bundled replay with
the published SDK's arbitrary-precision ITF reader. No float64 conversion or
int64 destination was used. A one-unit rounding negative control fails.

Group output order is not promised by the contract. The pre-freeze review
replaced ordered list comparison with multiset equality preserving duplicate
counts. The observing driver still retains original raw public count strings.

TypeScript reports the planted source mismatch but supplies no failing
post-state trace. The native failure and exact captured-record JSON remain
usable; the acceptance checker explicitly reports `modelPostStateAvailable:
false`. It does not manufacture a model state or claim counterfactual navigation.

No new core adapter or decoder defect was demonstrated. Keep the backend and
capture limits explicit rather than adding compatibility paths or extending
numeric representations speculatively. The next capability should follow an
actual adoption blocker or independent-maintainer use, not another adjacent
arithmetic fixture.


The disposable dependency copy initially lived under ignored logs. Repository
prose checks still traverse those directories, so foreign documentation entered
this repository's checks. The runner now keeps the complete dependency copy in
an external temporary directory, normalizes only its inherited read-only
directory permissions, retains source-hash provenance with the evidence, and
cleans that temporary copy on exit. Frozen files and the module cache remain
unchanged.


The model compares group-count multisets. The frozen representation gate compares
this fixed executor's lists positionally; its claim is exact value preservation
for these observations, not general output-order invariance. A later observer
doc-comment update refreshed only its checksum: executable Go tokens match the
original pre-contrast freeze exactly, and the model/binding remained unchanged.
