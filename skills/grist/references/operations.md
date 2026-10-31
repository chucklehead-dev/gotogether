# Operating a grist node, and its store's incidents

Read this before running an app's binary in production (secrets, logs,
metrics, migrations, retention), or when a node reports a rewind
(`grist.ErrRewound`, `grist_rewound 1`, a `rewound` log line), or when
a test simulates a managed database's failover or a restore. The design
is docs/grist.md §2.11 and docs/grist-platform.md §3.4.

## The binary's commands

```sh
app serve -db-file /secrets/dsn -addr :8080 -health-addr :8081 -steps /steps/   # the DSN out of ps and the pod spec
app resume -db-file /secrets/dsn -tenant t1         # after a rewind, once an operator decided: never automatic
app steps -export -db-file /secrets/dsn -tenant t1  # every committed step as JSONL, as a relay records them
app prune -db-file /secrets/dsn -steps 720h -markers 2160h   # retention: step rows and markers past the horizon
```

- serve's stderr has JSON lifecycle lines (`starting`, `serving`,
  `draining`, `drain` per phase, `stopped`, `rewound`), each with the app,
  node and incarnation; the plain startup lines stay.
- `/metrics`: `grist_requests_total{kind,block,code}`,
  `grist_request_duration_seconds` (histogram), `go_*`,
  `grist_build_info`, `grist_rewound{tenant}`, beside the processor,
  relay and snapshot gauges.
- `sqlstore.Open` migrates under `grist_schema`'s lock: pods starting at
  once are safe; a database a newer grist migrated is refused. Schema version 2
  requires immutable stored-event IDs. Older prerelease/unversioned tables are
  refused without backfilling; initialize a fresh schema deliberately.
- A marker pruned is a key whose retry runs again: give `-markers` a
  horizon far past any client's retries. `steps -export` before pruning
  where a whole history is wanted.
- One tenant's appends serialize on its head row: hundreds a second on
  PostgreSQL (docs/grist.md §2.11 has the numbers); a busier one is two
  tenants.

## A rewind

A failover that lost its asynchronously replicated tail, or a restore,
makes a tenant's log go backwards under a running node. The node records
it (`gristRewound`, a model step), stops the tenant's processors (no
effect runs again for a trigger it already ran for) and keeps serving
commands and queries. Decide what the lost commits did (the clients'
receipts, the dependencies' records, `steps -export` and another node's
steps file), then `app resume -tenant T`: the processors go on from their
checkpoints, at-least-once effects replay under their keys (a Keyed
dependency answers from its record), at-most-once ones run again.

Effect keys include the immutable `Meta.ID` of the trigger, not a node's rewind
counter: `<app>/<processor>/<tenant>/<position>@<event-id>`. A surviving and a
fresh node use the same key. Restored retained events keep their IDs, while
replacement commits get fresh IDs even with identical fields. Native effect and
call records expose `triggerId`; `CallInfo.TriggerID` also crosses transports.
Missing/malformed IDs stop processing before callbacks or checkpoint changes.
These IDs do not detect an unobserved restore or reconcile lost external effects;
resume still requires the operator's decision.

An observed rewind permanently retires that node's in-flight processor work,
including if resume occurs before its callback returns. A started callback may
finish and record its actual result under the original trigger identity and
causal clock. Old work cannot retry or submit checkpoints or domain outcomes;
no failed/unknown event is fabricated for retirement. Check the dependency
records and native telemetry before resuming. This is an admission fence, not
revocation of an already-issued append/call or protection against an unobserved
restore. Do not automatically replay or redrive a late completion.

## Simulating it

```go
cfg.Store = &gristsim.Managed{
	Failover: &qenv.Failover{LoseTail: 2, ReadOnly: time.Second}, // store.<app>.failover at commits the run chooses
	Restore:  true,                                               // nemesis.restore.<app>, from a backup every RestoreEvery
	Resume:   5 * time.Second,                                    // the operator resumes 5 s after a rewind
}
cfg.Store = &gristsim.Managed{LoseAfter: 4, Lose: 1, Resume: 5 * time.Second} // the 4th commit lost, at a point the test chooses
```

| A run shows | Means |
|---|---|
| `gristRewound`, then `gristResumed`, conforming | the incident, and the operator's resume, as the model admits them |
| `Mismatch("read")` at the clients only | a commit acknowledged and lost before any node read it: a receipt with no commit |
| the first step after a lost commit rejected (`ledgerPost`, `bankPay`) | a lost commit whose consequence (an effect) happened |
| an app's val violated at the settle (`postedApplied`) | a dependency holds what the log lost |
| `Rejects("<p>")` or `Rejects("<call>")` while rewound | a processor ran after a rewind unasked: what `Managed.FailOpen` (the ablation) shows |


## Declared-call telemetry

For a real tier that already exports OTel, route the context's recorder to
`qobs.NewOTelSink`, flush the provider explicitly and decode the exported
OTLP/JSON with `otelio.ReadSteps`. The existing store-side bindings judge
these native step events, including invocation pairing, scalar fields and
order coordinates. Ordinary HTTP span names/statuses do not supply them.
Retain both services' committed records and caller records for
`gristtest.Linked` / `gristtest.ProtocolSteps`; a timeout is unknown, while
an exported invocation with no completion is pending. Never infer settle
or completeness from span end or exporter flush. See examples/payouts'
`SPANS=1 ./run-demo.sh` and `GOTOGETHER_SPANS_OUT` for the real HTTP,
lost-reply, interrupted-recorder and generated-mutant acceptance path.

## Retaining committed history

`gristtest.Capture` submits its private event IDs with each batch, so a captured
log restores the exact recorded identities through `Batch.RetainedIDs`.
Ordinary commands/processors leave that field nil and stores mint new IDs.
Explicit retained IDs must be canonical, distinct, and absent from the target
store; a collision is a typed permanent `store.IdentityExists` with no partial
commit. Raw fixture batches without IDs materialize new identities and do not
prove the original ones. Capture errors, overlapping appends, unknown outcomes
and snapshots sampled during an append cannot be written or restored as a
complete capture. Collect a fresh known-complete sequential history instead.
