`ci/mutate-core.sh <package> [max-survivors]` runs this helper. It mutates the
requested source prefix and admits tests from every test-bearing package
under `./<package>/...`, including packages with only external tests. Each suite
has its own numbered binary and runs in its source directory. Compilation,
inventory execution, and every unmutated baseline must succeed before mutated
tests run. Baseline failures retain their diagnostics.

A process-local mutant ID is never an identity across binaries. The identity is
the source file and its ordinal in that file's generated registration order.
Every occurrence has an ordinal even when two source sites have identical
printed descriptions. Shared files must have matching descriptions and mutant
counts across binaries; inconsistent inventories abort the campaign. This needs
no change to the woven runtime or its public metadata.

The denominator is the union of source identities. A mutant survives only if
every suite linking it passes; a suite not linking it contributes no result.
Suites registering no mutants still need a passing baseline. A campaign with
no mutants at all fails. Descendant-only mutants now contribute to the total;
existing survivor thresholds remain unchanged. Tests outside the requested package tree are not admitted.

`JOBS` bounds concurrent source mutants. Each test process retains the existing
`MEM_MB` cap (3072 MiB by default), short-test mode, and 300-second limit. A source
mutant's linked suites run in sequence until one kills it. Timeouts and ordinary
test failures kill mutants; setup failures abort the campaign. Inventory and
baseline execution have the same cap and timeout but must pass.

Run the focused controls with `go test -race ./ci/mutatecore`.

Build and baseline phases name each suite on stderr. Admission reports the
source-mutant and suite totals; execution reports completed source mutants and
elapsed time every 25 completions. These progress messages are not verdicts.
