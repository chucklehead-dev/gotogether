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

A `complete` event means every source the journal runs (all of them, or a
shard's selection) has a durable outcome; `gate` separately reports whether the
survivor limit passed (`passed`, `failed`, or a shard's `deferred_to_aggregate`). A complete
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

The helper now supervises a separate instance of its own executable. The worker
builds qweave once, executes the unchanged suite/candidate/count policy, and
explicitly Syncs and Closes its journal before publishing its worker result.
The parent observes that worker's actual Wait status, re-executes each built
binary's inventory, checks independently discovered suite membership and exact
binary identities, and validates the current journal before publishing
`<journal>.receipt.json`. A complete campaign above its ceiling still publishes
complete evidence and exits red; the worker's successful execution is distinct
from that external count decision. A worker exit alone, a `complete` line alone,
or a result without a successful Wait never establishes sealed completion.

`<journal>.inputs.json` retains the initial content manifest even if execution
aborts. Static Go discovery covers the selected suites, the root graph qweave
actually loads, and qweave's separate tooling module/dependencies. Membership
and actual source/embed/module/workspace/tool bytes are rechecked; dirty and
ignored compiled inputs are accounted, while unrelated documentation and runtime
outputs do not enter this static boundary. The parent records exact inherited
environment as a digest, actual helper/generator/compiler/tool identities and
build argv. An owned Go shim retains generated overlay payload bytes under
`<journal>.build-inputs/` before the real Go command consumes them. Those files
and their invocation manifests are retained for the final hash checks.

These are private completion receipts, **never survivor approvals or
admission-eligible policy evidence**. Selected cgo/native sources and compiler
binaries are recorded, while transitive system headers/libraries/native helper
executables remain explicitly unaccounted. VCS build metadata is reflected in
the final binary hash but not independently collected. Arbitrary runtime file
reads, external commands/services and hostile concurrent modification are not a
closed or authenticated universe. This protection detects ordinary drift, not
adversarial TOCTOU changes. Source contexts use absolute resolved paths and are
not promised portable across checkouts.

Unsupported collection settings (for example an external Go cache program,
input-changing GOFLAGS or aspect-tool configuration) do not silently become
complete identity evidence and do not disable the existing count gate. The
helper records the exact unavailable-collection reason, runs the existing build
and count policy unchanged, and marks input rechecking unavailable. No fallback
changes CGO_ENABLED, tags, limits or test options. A collected source/binary drift,
execution failure, incomplete journal or durability error prevents sealing and
fails the command. Input/output sidecars are published without replacing prior
evidence; all remain under the existing artifact retention tree by default.

Completion publication checks cancellation again immediately before acquiring
its final receipt name, after encoding and file Sync/Close. Journal directory
creation syncs every ancestor as well as the leaf, including the parent entry
of a new default campaign directory; explicit journal paths get the same
ancestry durability checks. Failures prevent successful publication.

The hard-deadline regression observes child termination within its existing
three-second promptness allowance: reaping the timeout wrapper does not
synchronize its grandchild's SIGKILL/exit transition. The observation sends no
extra termination signal and adds no deadline grace. A live-child negative
control remains, and failures retain the PID, last proc state and wrapper
result. An earlier one-shot observation failure lacked that state, so it is
consistent with an asynchronous observation race, not a proven diagnosis of
that particular historical execution.

## Shards and their aggregate

A campaign too long for one runner is split: `ci/mutate-core.sh -shard i/n
<package>` (0 ≤ i < n) runs shard i of n. With no `-shard` (or `-shard 0/1`) the
campaign is exactly the unsharded one above: the same candidates in the same
order, its max applied, its exit status. A shard of n > 1 takes no max and is
refused one: it never applies the limit.

Every shard builds every suite, executes every inventory, requires every
unmutated baseline and admits the complete ordered inventory (sorted by source
file and file-local ordinal) before it selects anything. It then runs the
sources whose index in that inventory is ≡ i (mod n). Its journal's `admitted`
event records the full inventory with its suite targets and the selected
indices; outcomes are recorded for exactly the selection, and its `complete`
event counts the selection with gate `deferred_to_aggregate` (an unsharded
campaign's gate is `passed` or `failed`). The parent seals each shard as
before, with its receipt.

`ci/mutate-core.sh aggregate <package> <max> <n> <evidence-dir>` is the one
verdict of a split campaign. It reads every `*.receipt.json` and `*.inputs.json`
under the directory (one directory per shard artifact) and fails closed, naming
each problem, unless all of these hold:

- one sealed receipt for each shard 0..n-1: a missing or duplicate shard, or a
  shard of another split, fails;
- every journal that was started is sealed: a journal without a receipt fails,
  reported as aborted (canceled or failed, with its error), complete but never
  sealed, or incomplete;
- each receipt is in the current strict shape (exactly what the helper writes:
  no field missing, null, aliased, duplicated or unknown), of the package
  asked, not admission eligible, its worker exit 0, its execution identity
  matching its recorded inputs and inputs sidecar, and its journal the bytes it
  sealed;
- each journal revalidates against its own manifest without the deleted
  binaries: settings, suites, every baseline passed, the full inventory
  admitted (recomputed from the manifest's suite inventories), the modulo
  selection, a valid outcome (execution evidence, attempt order) for exactly
  each selected source, and a completion that reconciles;
- one shared identity across shards (below), recomputed by the aggregate from
  each receipt's evidence and equal to the one the shard recorded;
- the shards' resolved sources are disjoint, and their union is exactly the
  full inventory, by source identity (file and file-local ordinal, so repeated
  identical descriptors stay distinct) with matching descriptors. Local binary
  IDs take no part.

Only then is the package's unchanged max applied, once, to the survivors of
every shard summed. A complete red aggregate still publishes
`<evidence-dir>/<package>.aggregate.json` and exits 1; a failed verification
publishes nothing. The aggregate receipt lists the survivors, each shard's
execution identity, and the aggregate concurrency: n shards × JOBS concurrent
source mutants, on separate runners, each test process with its own memory cap
and 300-second limit. Like every receipt here it is completion evidence, never a
survivor approval or admission. There is no reader for older journals or
receipts: rerun with the current helper.

### The identity boundary

The *shared identity* is what every shard must have in common. It is computed
from a receipt's recorded evidence, with three path prefixes made relative (the
checkout root, `GOROOT`, `GOMODCACHE`) and nothing else dropped or relaxed:

- source: every collected static input file (content, size, mode, link) and the
  Go discovery membership that selected them;
- candidates: the full ordered inventory (file, ordinal, descriptor, linked
  suites by package), its count and digest;
- suites: each package, its directory, its mutant count and its built test
  binary's digest;
- generator and toolchain: this helper's executable, the qweave it built, the
  Go toolchain binaries, `go`, `sh` and `timeout`, each by content; the Go
  version; the digest of qweave's generated overlay bytes;
- configuration: the journal version, JOBS, MEM_MB, the per-test timeout, the
  discovery arguments and the recorded `go env` settings.

Each shard's *execution identity* stays its own: its `input_digest` (the exact
inherited environment and absolute paths, unchanged), runtime environment
digest, root, tool paths, journal and worker execution. It differs between
runners and is retained, per shard, in the aggregate receipt; it is not
compared. Limitations of this boundary:

- a test reading an environment variable outside `go env` may behave
  differently on different runners, and that difference is not detected;
- built binaries embed the checkout path, so shards must run from the same
  path (GitHub's runners check out at one fixed path); a different path fails
  closed as a generator or binary disagreement, not silently;
- a shard whose input collection was unavailable (an unsupported `GOFLAGS`,
  `GOCACHEPROG` or aspect tool) is sealed as before but has no shared identity,
  and fails any aggregate;
- the closure limitations above (native headers and libraries, VCS metadata,
  runtime reads, external services, hostile concurrent modification) apply to
  each shard and so to the aggregate; receipts are not authenticated, and the
  aggregate checks their consistency, not who wrote them.

In the nightly, `ci/core-mutants.txt` gives a row `shards=N`; `ci/nightly-select.sh`
expands it into a matrix entry per shard (artifact
`mutations-<package>-shard-<i>-of-<N>`), and `core-mutants-gate` (after every
shard, whatever they did) runs the aggregate for each split row the run took.
It is the split package's failing signal.
