# Bounded instruction-isolated adoption rehearsal

Started about 2026-10-30 23:03 UTC; first useful model/runtime verdict about 23:04:40 UTC (roughly two minutes). This is an internal instruction-isolated rehearsal, not independent-maintainer evidence. No toolkit source, existing harness/model, or mutant was inspected. Read supplied service and public README/workflow/liveness/prove-it docs; used scaffold/help and exported `go doc` only.

## Ownership and scope

Independently authored `model/payments.qnt` (three phases, fixed one account/key, amount 10), `payments.binding.yaml`, and `rehearsal_test.go`. Roughly two minutes implementation, one minute checking and documenting before freeze. This is a bounded contract, not a general proof over arbitrary accounts, keys, crashes, SQL backends, or processor failures. The processor peer dedupes by the actual incoming HTTP key, an explicit environmental assumption. Model liveness assumes weakly fair begin/finish; the real finite prefix makes no liveness claim. Conflict/replay are unfair inputs. The service source is uninstrumented; observations are SQL queries and HTTP responses from the driver plus the peer's actual capture map, after controlled synchronization. Begin is recorded only after the real request has claimed its SQL key and entered the delayed processor. No successful runtime event is invented. The prefix is copied while that request is blocked, then processor release permits actual completion and replay.

`once` and `conserved` are design invariants; bindings compare their complete relevant observed state. The model uses legal transitions, not unconstrained observed mutations. Snapshot debit count comes from accounts.n_charges; captures from deduped peer capture map. The test does not independently validate every response body field or GET/read APIs.

## Reproduction

All commands run from `/workspace/onboarding-http-sql`, first `source /tmp/gtg-path`, with `GOWORK=off` for Go commands. Toolkit binary `/tmp/onboarding-tools/gotogether`, pinned core version in go.mod, no replace.

```
GOWORK=off GOFLAGS=-mod=mod go test -count=1 -v ./...
quint test model/payments.qnt
/tmp/onboarding-tools/gotogether simulate -graph -binding payments.binding.yaml
/tmp/onboarding-tools/gotogether validate -binding payments.binding.yaml -dir /tmp/onboarding-faithful -require-finish -manifest logs/faithful.manifest.json logs/faithful.jsonl
/tmp/onboarding-tools/gotogether validate -binding payments.binding.yaml -dir /tmp/onboarding-prefix logs/prefix.jsonl
/tmp/onboarding-tools/gotogether validate -binding payments.binding.yaml -dir /tmp/onboarding-prefix-finish -require-finish logs/prefix.jsonl
/tmp/onboarding-tools/gotogether validate -replay logs/faithful.manifest.json -dir /tmp/onboarding-replay
/tmp/onboarding-tools/gotogether validate -binding payments.binding.yaml -dir /tmp/onboarding-missing logs/missing-observation.jsonl
```

Original report files are in logs. Faithful: conformance, safety and eventual completion pass; graph complete (3 states, 5 transitions), acceptance scenario reached; Quint happyTest passes. Prefix: conformance/safety pass, completed uncollected/no verdict. Same prefix with explicit completion requirement: incomplete/no settle failure (not a model defect). Full trace with actual settle passes require-finish. Manifest replay reproduces PASS. Missing-observation negative control removes only finish obs.balance from the real log: binding field error, no model-defect classification. This is deliberate malformed evidence, not a code mutant.

## Friction and workarounds

- Initial go doc failed on absent sums, then remained unable to resolve package after `go mod download`. `GOWORK=off GOFLAGS=-mod=mod go doc ...` resolved it and populated sums; no toolkit version changes.
- `go doc -mod=mod` is not supported; GOFLAGS is required.
- Scaffold included completed among ordinary invariants. Moved it to eventually and set model liveness classification deliberately.
- First expect used a field inside a quoted string. Graph evaluator explicitly refused that. Public README showed bare templates; changed to `phase == {{.obs.phase}}`, which quotes the typed observation itself.
- First default CLI validate commands created transient /tmp/gotogether-validate-* paths rather than requested /tmp/onboarding-* names; passing commands removed them. Subsequent commands use explicit -dir. No toolkit repo files touched.
- No additional skills unavailable from supplied docs were silently substituted. No source mutant inspected or invented before freeze. Model checks' self-mutants and Hegel are not yet exercised; they are a scope limitation, not claimed evidence.

## Freeze

See FREEZE.sha256 for model, binding, driver and original baseline source hashes. Frozen before requesting any source contrast. Later contrast must retain model/binding unchanged; source overlay or copy will be recorded separately. Runtime evidence hashes are separate because recording reruns legitimately change timestamps and recorder IDs.

## Parent-provided contrast after freeze

At 23:06 UTC parent disclosed `/tmp/onboarding-double-debit/overlay.json`, replacing only the debit SQL with subtracting twice the amount. This is an explicitly planted source contrast, not a discovered production bug. Original service source and frozen files remain unchanged (`sha256sum -c FREEZE.sha256` passes). Source copy and overlay hash are in contrast/PROVENANCE.sha256 and contrast/charge.go.txt. Baseline logs were preserved in logs/baseline before running the overlay.

Actual fresh HTTP/SQLite contrast execution completed with status201, one capture and n_charges1, but balance80. Frozen binding rejects finish as **observed state mismatch** against model balance90. This is not an invariant failure: the checker correctly refuses to judge invariants after nonconformance. logs/contrast/report.txt and manifest.json record it; replay.txt reproduces same failing finish. Missing obs.balance remains a separate malformed-evidence/tool diagnostic and cannot count as catching this source contrast. Go execution itself passes, demonstrating that the useful contrast verdict comes from the frozen binding rather than a test setup crash.

### Workload minimization

Public `gotogether shrink -help` supports external action drivers, not bare-log minimization. Added separate shrink_driver_test.go derived from original driver without modifying the frozen original. The externally supplied action list enables charge/conflict/replay; each candidate launches a fresh actual service, SQLite database and delayed processor. An empty action list produces a genuine empty step log. Initial shrink failed because empty sequence was rejected; help/error explicitly required Reset+empty run to succeed, so the driver was corrected rather than counting that setup failure.

Public shrink reduced `[charge, conflict, replay]` to `[charge]`, 1-minimal under action deletion, using eight real reruns, with mismatch after finish retained. The single charge action still contains asynchronous start/processor delay/release/completion (two observed model steps begin/finish). This is **rerun workload minimization**, not arbitrary recorded-trace deletion, and does not claim minimal scheduling or Go instructions. Outputs, manifest and actual steps: logs/contrast/shrunk/. The separate driver skips outside external minimization mode.

`reproduce.sh` is the portable entrypoint: Go per go.mod, Python3, Quint and version-matching gotogether on PATH; no /workspace or absolute tool binary assumptions. It creates an absolute-path overlay dynamically from the portable source contrast copy. Full original logs contain absolute paths in their manifests (replay works in original location); the portable script regenerates manifests at a new checkout location.

## Portable verifier hardening

Root review correctly identified that the initial shell script treated any nonzero validation status as an expected rejection. `reproduce.sh` now requires exact process status 1 for incomplete-prefix, source mismatch and both manifest replays, and exact status 2 for missing-field evidence. JSON assertions in contrast/check_report.py also require the precise verdict: source contrast/replays must have one state/conformance failure on finish, observed balance80/captures1, model balance90, reason `mismatch after finish`. Prefix must pass conformance with eventual completion uncollected; missing-field control must be error2 specifically naming absent obs.balance. Tooling errors cannot satisfy the source contrast checks. Additional checker negative controls confirmed exit2 and unrelated failure reason are rejected.

The hardened script generates missing-observation evidence from its own baseline and checks it. Every invocation uses a new logs/reproduction/run-* directory containing reports, dynamic overlay, generated modules and fresh manifests. Historical logs/contrast manifests are original-location evidence only; portable claims use newly generated manifests and replay them in the current location. The script contains no absolute toolkit binary or original workspace path. The model, binding, original driver and original service freeze remain unchanged. No Go module/pin change.

Hardened end-to-end run passed: logs/reproduce-hardened.log, outputs logs/reproduction/run-20261030T230911-308386. Initial hardening check caught a mistaken assumption that unjudged eventuals appear in actors.invariants; public JSON instead reports receipt.checks.status=not_checked and absent.cause=uncollected, now asserted directly. A relocation smoke setup initially omitted the binding during copying (setup error, not a model catch); copying the complete artifact fixes the setup.

Complete relocated artifact `/tmp/onboarding-portable-check/reproduce.sh` also passed end-to-end when invoked from the original working directory: logs/reproduce-relocated.log. It generated and replayed fresh source and shrunk manifests under the relocated artifact's own logs/reproduction/run-20261030T230936-309310 directory, confirming no dependency on original absolute manifest paths.


## Repository packaging after the rehearsal

The parent copied this artifact to `internal/adoptiontest/http-sql` after the blind
rehearsal and contrast were complete. It is intentionally outside go.work and
uses the original published module pin, without replacements. Public docs and
tools were supplied at that pin; the agent had instruction-only access limits,
not a separate security sandbox. No existing payd harness/model/mutant was given.

Packaging runs gofmt on the active Go drivers. The exact original pre-contrast
driver bytes remain in `frozen/rehearsal_test.go.txt`; FREEZE.sha256 now points to
that archived file. Model, binding and baseline service hashes are unchanged.
The parent-supplied source contrast is explicitly planted, not a production
finding. Transient original logs/manifests are not packaged; reproduce.sh makes
fresh evidence, semantic checks and manifests at the current checkout location.
