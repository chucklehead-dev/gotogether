This is an **unshipped prerelease prototype**, with no CLI, workflow, journal
parser or approval data. Existing count gates have not been migrated. The final
migration must replace the count argument and workflow ceilings atomically with
one canonical `ci/mutation-survivors.json`; there is no legacy mode in this
policy package. Observed survivor counts do not authorize entries.

`Load` strictly decodes the single format, including duplicate-key rejection.
`Admit` validates explicit package entries, exact file/registration-ordinal/
descriptor identities, per-source digests, human rationale/evidence and the
campaign context. `Admission.Check` requires one resolved outcome for every
inventory identity and complete validated evidence. Both unknown survivors and
reviewed cases now killed make `Difference.Passed` false. Killed reviews carry
the first killing suite for explicit reconciliation, never dormant permission.
Errors and incomplete journals must remain red. A future adapter must validate
journal integrity, source resolution and successful durable writes before
claiming completeness; a Boolean here is an explicit adapter precondition,
not a journal validator. Suite-local activation IDs never enter this policy.

The context boundary is deliberately conservative: the complete closed universe
of production, test, caller, model/spec, generator/runtime, build and dependency
inputs, including file paths and additions/deletions, plus exact toolchain
identity and inventory settings. `Snapshot` hashes a supplied `fs.FS`; it is
**not a repository input collector**. Before integration, the collector contract
must be settled and tested: start from version-controlled inputs, account for
all additional build inputs, reject unaccounted untracked/ignored compilable
sources, account for external replacements/generator inputs, and exclude runtime
outputs such as campaign journals, caches and artifacts. Do not supply arbitrary
checkout contents or an author-selected dependency subset. `.git` metadata and
the canonical ledger itself are excluded. Nonregular files fail closed until
a collector can account for their targets. All toolchain/dependency binaries
outside this universe need immutable identities in the toolchain/settings input;
a version label alone is insufficient. Settings must cover the actual inventory
scope, suites, build tags, platform, flags and relevant environment.

Broad context invalidation catches changed/new callers and operators without
trying to infer a dependency boundary. It also expires reviews after comments,
unrelated declarations and test-only changes. Intentional batch refreshes should
show changed paths/settings, preserve old rationale only as review context, and
require an explicit reviewed ledger change. This prototype reports a context
mismatch; a human-friendly changed-path/descriptor diff remains integration work.
Hashes preserve provenance; they do not prove equivalence, completeness of the
collector, or unchanged external behavior. External-runtime gaps have their own
kind and evidence and are never called equivalent automatically.

Only synthetic fixture reviews exist in tests. No campaign has been run and no
current survivor has been approved. Next review should settle the collector and
journal adapter boundaries before adding any production ledger or integration.

Admission currently compares the **whole policy prefix map** with the supplied
campaign inventory map. Each requested prefix must contain source mutants; a
suite with no mutants is a separate harness concern. Reviews resolve only within
their own prefix's inventory, while duplicate sources anywhere in the union are
rejected. Real harness prefix selection and its relationship to the canonical
whole ledger must be designed alongside the collector before integration. This
prototype is not ready to ship as an admission replacement.

The implementation lives in `ci/mutatecore/internal/reviewpolicy` to keep the
unshipped tool policy out of the module's public package surface. Its focused
check is `go test -race -shuffle=on -count=2 ./ci/mutatecore/internal/reviewpolicy`
from the repository root. This is a draft review slice; collector, validated
journal adapter and atomic policy migration remain blockers to shipping it.
