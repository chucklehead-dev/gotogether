# Versions and snapshots: an event that changes, a release that rolls, a read model that is long

From the grist skill (its SKILL.md says when to read this). The user
guide has the whole of it, with the worked case (examples/library's
book-added, version 1 to 2) step by step: read
[its §7, Versions](https://github.com/chucklehead-dev/gotogether/blob/main/docs/grist-guide.md#7-versions-changing-an-event)
and [§8, Snapshots](https://github.com/chucklehead-dev/gotogether/blob/main/docs/grist-guide.md#8-snapshots)
before changing an event or a fold. In this repository: docs/grist-guide.md.
What an agent must not get wrong:

## An event whose shape changes

- Never change a stored version's struct in place: `<app> model`
  refuses it against grist.lock. Keep the old struct under its own name,
  fields and tags exactly as stored (`BookAddedV1`), and register the
  new one with `grist.Upcast(1, func(v BookAddedV1) BookAdded {...})`.
  One upcaster per old version, each to the next, never edited again.
- An upcaster is pure and total, keeps every tag (key and value: a tag
  that must change is a new event type) and carries every field unless
  it says `.Drops("field")`. `gristtest.Upcasts(t, App)` checks it.
- A field added, nothing else changed, needs no new version: grist.lock
  takes it.
- Before the change, keep a log of the deployed version in testdata
  (`gristtest.Capture`, `gristtest.WriteLog`; library's `TestRecordLog`),
  never recorded over, and judge it by today's model on a tier
  (`gristtest.ReadLog`, `TierConfig.Log`, `Tier.Check`, then
  `gristtest.Snapshots`).
- Regenerate and fix every fold and rule the warnings name in
  `model/<app>_rules.qnt`: the model knows only the newest version.

## A new version in a rolling update: readers first

- Release N+1 registers the upcaster and holds writes back with
  `grist.WritesVersion(1, downcaster)`; release N+2 drops it once no node
  of release N runs. Decide only what the written version can say.
- `<app> catalog -diff deployed.json` (exit 0 `safe`, 3 `readers first`,
  4 `breaking`) in CI against the deployed release's catalog.
- `gristtest.Upgrade(t, binding, release1.App, App, cfg)` with a rolling
  `gristsim.Cluster`, claimed `gristtest.ReadersFirst()` at the clients;
  the deployed release kept as a package of its own (counters' `release1`).

## Retained histories

For kept committed-batch fixtures, use
`gristtest.StoredLogs(t, App, gristtest.StoredLogsConfig{Files: files, Binding: commits})`.
It restores each file fresh and checks event versions/tags, recorded observations,
current fold/snapshot round trips, and committed-prefix conformance. It never
starts processors or effects and does not synthesize settle. Inspect its typed
stage statuses/native model document: missing rows or no verdict are not passes.
The files contain no historical snapshots or original step-row timestamps;
completion/within bindings are refused. This does not prove unchanged fold
business meaning or historical capture completeness. Use the real tier when
the test intentionally needs to execute pending work after restoration.

## Snapshots and the memo

- On by default past `grist.DefaultSnapshotEvery` events per read:
  snapshots in the store, and each node's memo of states in memory (a
  read at the head then reads one event). `grist.New(name,
  grist.Snapshots(n))` changes the cadence (0: both off),
  `grist.Memo(entries)` the memo. Keyed by the read model's fingerprint
  (shapes, upcasters, what the fold makes of generated events): bump the
  read model's version when a fold's meaning changes in a way generated
  events may not show.
- Each is checked against its last folded event: a rewound log
  (`ErrRewound`) invalidates both.
- The state must survive JSON, or the read model is never snapshotted.
- `gristtest.Snapshots(t, App, st)`; `gristsim.Config{SnapshotEvery: 2}`
  with `store.<app>.snapshot` faults, claimed
  `qtest.Reaches("store.<app>.snapshot").Faulted()`.
- A deployed node audits one read in `grist.DefaultSnapshotAudit`
  (`grist.AuditSnapshots` to change it); alert on
  `grist_snapshot_audit_mismatches_total` and
  `grist_snapshot_put_failures_total`; `grist_fold_events_total` is what
  reads cost.

## Results

| Report says | Means | Next |
|---|---|---|
| `event X version N changed shape since grist.lock was written` | the struct of a stored version changed | keep it as `XVN`, register version N+1 with an upcaster; if never deployed, delete its lock entry |
| `the upcaster from version N ignores F` | an old field reaches no later version | copy it, or `.Drops("F")` on purpose |
| `an upcaster keeps every tag` | the upcast changed a tag | keep the tag; a new tag is a new event type |
| `X is version N, and this app reads X up to version M` | a newer node's event | finish the rollout; the newer release writes M with `WritesVersion` until no older node runs |
| `... which cannot hold it: written as version N it reads back as ...` | a decision only the newest version can record, while writing an older | decide only what version N says until the release that writes the newest |
| `catalog -diff`: `readers first` | the release writes what deployed nodes refuse | `WritesVersion(deployed newest, ...)` in this release, dropped in the next |
| `had N tries refused by a node of the older release` (ReadersFirst) | a rolling update's older node met an event it cannot read | as above |
| `read model R folds otherwise than when grist.lock was written` | its fingerprint changed | expected after a fold change; bump its version if its meaning changed |
| `its stored snapshot plus the events after it gives ...` | a snapshot of another fold, or taken at the wrong position | a fold changed without a new fingerprint: bump the version |
