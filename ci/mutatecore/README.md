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
mutant's linked suites run in sequence until one kills it. The exact source-owning
package runs first when it has a linked test suite; other suites retain their
admitted order. A passing owner still needs every other linked suite to pass.
Nonzero test-process statuses still count as operational kills; setup statuses
125–127 abort the campaign. This count policy is unchanged and does not establish
that an assertion detected incorrect behavior. Inventory and baseline execution have the same cap and timeout but
must pass.

Run the focused controls with `go test -race ./ci/mutatecore`.

Build and baseline phases name each suite on stderr. Admission reports the
source-mutant and suite totals; execution reports completed source mutants and
elapsed time every 25 completions. These progress messages are not verdicts.

Every run prints a unique internal JSONL journal path under
`.gotogether/mutations/` before building suites. `GOTOGETHER_MUTATION_LOG` can
select an explicit path, resolved from the repository root when relative; an
existing file is refused rather than overwritten. Journals are retained when
temporary test binaries are removed. There is no resume mode.

The versioned start event records only campaign settings. Suite inventories,
baseline results and admitted source-to-binary ID mappings precede mutation
outcomes. Each source outcome records the actual suites attempted, and is synced
to disk before progress is reported. Execution errors and cancellation are
errors, never killed mutations. Owned build, discovery and test commands run in
separate process groups so cancellation also stops their ordinary descendants.

A `complete` event means every admitted source has a durable outcome;
`gate_passed` separately reports whether the survivor limit passed. A complete
campaign can therefore be red. An `aborted` event reports graceful failure, while
an abrupt kill may leave only earlier records and a partial final line. Without
`complete`, the campaign is incomplete. Unrecorded sources remain unknown; no
verdict may be inferred for them. Write or sync failures fail the command.

CI must retain `.gotogether/mutations/` with an `always()` artifact upload and
`include-hidden-files: true`, including on cancellation or failure. Use a unique
matrix artifact name (for example, the job index), and retain the default
journal location so that upload finds it. These internal records are evidence
for review, not a public receipt format or permission to resume a prior build.

Each attempted suite now retains `execution`: the owned shell/timeout process's
`disposition`, exact `exit_code` when it exited, or actual OS `signal_number` and
signal display name when signaled. `exited` includes exit 0 and ordinary nonzero
statuses; exit 1 alone is not assertion evidence. `timeout_status` (124) and
`signal_status` (129–192) describe wrapper status conventions, not proven causes:
a test can explicitly return those statuses too. Other exit codes remain raw
`exited` statuses. `setup_status` (125–127), `execution_error` and `canceled`
remain errors rather than kills. Interrupted commands carry no inferred exit or
signal result. Baseline journal records retain the same execution metadata;
outputs and error details keep their existing capture policy.

Resource/status evidence can therefore be separated from ordinary exits during
review without changing the survivor gate. Neither an operational kill nor an
ordinary nonzero exit is itself behavioral evidence. These additive fields are
internal journal data; this slice does not integrate the separate draft journal
reader or introduce a public report, compatibility reader, campaign resume,
new limit, or approval policy.

The 300-second test deadline sends SIGKILL to the owned process group so tests
that ignore SIGTERM cannot outlive the cap. No grace allowance is added. GNU
`timeout` may also kill itself in that group. Native runs have observed both
actual SIGKILL and exit 124 at the hard deadline; the journal preserves whichever
occurred. The regression checks the TERM-ignoring child is no longer running,
independently of the wrapper's status. Neither status uniquely establishes a
timeout cause or an assertion witness; these remain operational kills.
