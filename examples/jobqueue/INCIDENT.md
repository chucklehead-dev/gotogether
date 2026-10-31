# A stale acknowledgement: from alert to verified repair

This is a bounded rehearsal of the existing `ackAnyLease` implementation mutant,
using actual jobqueue service calls. It is not a production incident or an
independent-adoption claim. The model and binding stay unchanged. The queue
promises at-least-once execution; this walkthrough does not enable `exactlyOnce`.

The incident: a worker leases job 1, the queue restarts and loses the lease, the
worker performs its work, and the buggy queue accepts its stale acknowledgement.
The faithful control rejects that ack. The repaired run also obtains a fresh
lease and completes it, proving that rejecting every ack is not a repair.

## Reproduce and keep the native evidence

From this directory, with Go 1.26.1, Quint 0.33.0 and `jq` on PATH:

```sh
incident_out=$(mktemp -d)
OUT="$incident_out" ./incident.sh
incident_bundle="$incident_out/moved-capture"
incident_manifest="$incident_bundle/incident/run.json"
incident_log="$incident_bundle/incident/$(jq -r '.inputs[0].path' "$incident_manifest")"
incident_binding="$incident_bundle/incident/$(jq -r '.model.binding.path' "$incident_manifest")"
```

The script builds the producer and CLI, records independent faithful/incident/
repaired captures, validates them, exports each with `gotogether bundle`, moves
the complete bundle, replays it from an unrelated working directory, and checks error/no-verdict controls. Existing
evidence is not overwritten. Expected tamper and missing-file errors appear on
stderr; their native error reports are retained. The script restores the input.

Each of `faithful/`, `incident/` and `repaired/` contains a native `run.json`
and verified binding, model and capture files under `files/`. `reports/` keeps
the original native reports and inspection documents. The temporary source
captures are removed after export. Replay depends on the copied
model/binding/inputs, not the checkout or the former bundle location. Go and
Quint are still required tools. Export uses existing manifest data and leaves
its verdict unchanged; it does not rerun the model or sanitize the evidence.
Generated work files are not the source model.

To export an existing manifest directly, use a fresh output directory:

```sh
gotogether bundle -out handoff run.json
gotogether validate -replay handoff/run.json -json
```

`bundle -json` emits the native exported manifest, with paths relative to
`handoff/run.json`. Exit 0 means the files were verified and published together,
even when the recorded model verdict is failure or no verdict. Existing output,
stale or missing files, unresolved input formats, and unsupported model
references are errors. File imports in injected `defs` must move into the model
before validating anew.
Evidence is copied unchanged; select and sanitize logs before validation if needed.

## Route the alert using data

```sh
jq '{verdict, exit, failures: [.actors[].failures[]? |
  {check, kind, record, quint}]}' "$incident_bundle/reports/incident.json"
```

Require `verdict == "fail"`, `check == "conformance"`, `kind == "disabled"` and
the captured action `ack` with outcome `ok`. `brief`, `text`, `reason`, source
paths and navigation command strings are display context; do not parse them for
identity. This is an illegal implementation transition, not an invariant failure.

| Index or identity | This incident | Purpose |
|---|---|---|
| actor | `jobqueue` | select the checked history |
| recorder | `jobqueue-incident-incident` | identify the capture |
| recorder sequence | 4 | identify the actual acknowledgement |
| model step | 5 | locate the rejected call after the inferred restart |
| cursor prefix | 4 calls | open the model immediately before the rejection |
| source location | JSONL line 5, including its header | inspect provenance; the path can move |

The stable captured reference is actor + recorder + sequence. The model call and
failure kind add the semantic failure identity. Sequence, model step and source
line happen to differ here; substituting one for another opens the wrong state.

## Inspect the same record and model boundary

The agent-facing forms use existing native JSON:

```sh
incident_ref=$(jq -c '.actors[0].failures[0].record | {actor, recorder, seq}' \
  "$incident_bundle/reports/incident.json")
"$incident_out/bin/gotogether" nav -run "$incident_log" \
  -record "$incident_ref" -json
"$incident_out/bin/gotogether" nav -binding "$incident_binding" \
  -run "$incident_log" -at 4 -json
```

The first command returns the captured ack unchanged. The second returns a model
cursor, with no whole-history verdict. Its path includes `restart`, although the
capture has no synthetic restart record. The model requires a stale outcome;
the recorded implementation answered `ok`.

Build the interactive navigator from this checkout and open the same capture:

```sh
(cd ../../qnav && go build -o "$incident_out/bin/gotogether-nav" ./cmd/gotogether-nav)
"$incident_out/bin/gotogether-nav" -binding "$incident_binding" \
  -run "$incident_log"
```

It opens before call 5. Press `e`, select the rejected captured record, then
Enter: the inspector shows the same actor, recorder, sequence 4, `ack`, `ok` and
`queue-2` process. Backspace returns to the unchanged model cursor. Filtering
with `/ack` explains why `o == Stale` rejects the observed `Ok`.

Taking an enabled stale-ack branch explores a hypothetical model continuation.
It changes neither the capture nor its verdict. Evidence inspection continues
to show the original checked history, even after a counterfactual move. A model
branch is not a repaired execution.

## Verify portability and repair

```sh
cd "$incident_out"
"$incident_out/bin/gotogether" validate \
  -replay "$incident_manifest" -json
```

Exit 1 with the same native conformance failure is expected. The script checks
the captured identity and native manifest verdict again after relocation; the
original capture directory no longer exists.

`reports/repaired.json` must pass the unchanged model and binding. Its seven
actual records reject the original stale ack, then lease, perform and accept a
fresh ack. The producer verifies the service's final `Done` state. The duplicate
performance is permitted by this at-least-once design; no stronger delivery
guarantee is inferred.

| Native report | Verdict | Exit | Meaning |
|---|---|---:|---|
| `faithful.json` | `pass` | 0 | stale ack rejected under the same forced incident |
| `incident.json`, `replay.json` | `fail` | 1 | actual captured ack is disabled by the model |
| `repaired.json` | `pass` | 0 | stale ack rejected and a fresh lease completed |
| `empty.json` | `no verdict` | 1 | a valid capture header contains no observations |
| `tampered.json`, `missing.json` | `error` | 2 | replay input integrity failed before model judgment |

Exit 1 alone cannot identify a service incident. A pass applies to the supplied
bounded history and selected checks; it is not production completeness or a
liveness proof. The script and `TestIncidentWorkflow` enforce these distinctions
without weakening the model to make the repaired case pass.
