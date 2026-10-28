# Versions and snapshots: an event that changes, a release that rolls, a read model that is long

From the grist skill (its SKILL.md says when to read this). The design
is docs/grist.md §2.1 (versioning) and §2.2 (snapshots);
examples/library's book-added is the worked case.

## An event whose shape changes

A stored event never changes: a v1 node's events stay v1 in the log.
Changing the struct in place makes every stored v1 event decode into the
new one silently (a renamed field reads as its zero value). Instead:

1. Keep the old struct under a name of its own, its fields and tags
   exactly as stored (`BookAddedV1`).
2. Change the event's struct, and declare how a v1 event becomes it:

```go
type BookAddedV1 struct {
	Book   string `json:"book" grist:"tag"`
	Copies int    `json:"copies"`
}

type BookAdded struct {
	Book  string `json:"book" grist:"tag"`
	Count int    `json:"count"` // was copies
	Shelf string `json:"shelf"` // new: "main" for every v1 event
}

var App = grist.New("library")

var Added = grist.NewEvent[BookAdded](App, "book-added",
	grist.Upcast(1, func(v BookAddedV1) BookAdded {
		return BookAdded{Book: v.Book, Count: v.Copies, Shelf: "main"}
	}))
```

   A third version adds `grist.Upcast(2, func(v BookAddedV2) BookAdded
   {...})` and renames the v2 upcaster's result to `BookAddedV2`: one
   upcaster per old version, each to the next, never edited again.
3. `go run ./cmd/<app> model`: grist.lock records both versions' shapes,
   the generated model knows only the newest (`bookAdded(book, count,
   shelf)`), and the warning names the folds and rules reading it: fix
   them in `model/<app>_rules.qnt`.
4. `gristtest.Upcasts(t, App)`, and a log of the deployed version judged
   by today's model (below).

The rules: an upcaster is pure and total (Verify runs it over generated
values at boot, and refuses one that panics or answers twice
differently); it keeps every tag, key and value (the store indexed the
event by them: a tag that must change is a new event type); it carries
every field of the old struct into the next unless it says
`.Drops("field")` (CheckUpcasters names one it ignores). Reads upcast
before any fold, handler or rule sees the event; appends write the
newest unless `WritesVersion` holds them back (below); a stored version
newer than the app's (a v2 event read by a v1 node in a rollout) is a
`*grist.NewerVersionError`, never a guess: the node's command or query
answers `ErrUnavailable` (503, retried, a newer node serves it) and its
processor waits. A field added to a version, nothing else changed,
needs no new version: grist.lock takes it (old events read it as zero,
old nodes ignore it), which is how a field rolls out. Fixing an
upcaster refolds the read models over it: the upcasters are in their
fingerprint.

## A new version in a rolling update: readers first

A release that adds a version must not write it while nodes of the
release before it run: they would refuse its events. So it ships in two
releases:

1. **Release N+1 reads version 2 and writes version 1**: the upcaster,
   plus a downcaster from the newest struct to the old one:

   ```go
   var Added = grist.NewEvent[BookAdded](App, "book-added",
   	grist.Upcast(1, func(v BookAddedV1) BookAdded { return BookAdded{Book: v.Book, Count: v.Copies, Shelf: "main"} }),
   	grist.WritesVersion(1, func(v BookAdded) BookAddedV1 { return BookAddedV1{Book: v.Book, Copies: v.Count} }))
   ```

   Blocks decide in the newest shape; the append is downcast, and fails
   (naming the fix) when version 1 cannot hold the event (a shelf other
   than "main"): decide only what version 1 can say in this release.
   Verify checks the downcaster (total, pure, tags kept), CheckUpcasters
   that it undoes the upcaster.
2. **Release N+2 drops `WritesVersion`**, once no node of release N
   runs: it writes version 2.

Check each release before it ships:
- `go run ./cmd/<app> catalog -diff deployed.json` (the deployed
  release's `<app> catalog`, kept in CI): `safe` (exit 0), `readers
  first` (exit 3: a version written at once, a new event type) or
  `breaking` (exit 4: a version or event removed, a shape changed in
  place, a tag, a block removed), each change with its fix.
- `gristtest.Upgrade(t, binding, deployed.App, App, cfg)`: a qtest.System
  of the rolling update, the deployed release kept as a package of its
  own (examples/counters' `release1`), every node's steps judged by
  today's model; with a `gristsim.Cluster` with `Rolling`, its rolling
  restart is the rollout:

```go
func TestRollingUpdate(t *testing.T) {
	up := gristtest.Upgrade(t, "shop.binding.yaml", release1.App, App, rolling)
	up.Explore(t, n).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	up.WithBinding("shop.calls.binding.yaml", qtest.Linearize()).Explore(t, n).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Completes()), gristtest.ReadersFirst())
}
```

  `gristtest.ReadersFirst()`: no call refused by an older node for an
  event it cannot read; a release writing the new version at once shows
  `qtest.Some(gristtest.WritersFirst())` (counters' `eagerV2` mutant).

## A deployed version's log, judged by today's model

Record a log with the app as deployed, before changing an event
(`gristtest.Capture` around a store, the workload's commands through
`App.Execute`, `gristtest.WriteLog`: examples/library's `TestRecordLog`),
keep it in testdata, and never record over it. Then every later version
runs on it:

```go
func TestOldLogConforms(t *testing.T) {
	log := gristtest.ReadLog(t, "testdata/logs/v1.jsonl")
	tier := gristtest.NewTier(t, App, gristtest.TierConfig{Log: &log})
	tier.Start("node")
	/* today's calls, then tier.Settle(...) */
	tier.Check("library.binding.yaml", "library.calls.binding.yaml")
	gristtest.Snapshots(t, App, tier.Store)
}
```

The node's relay records the old steps with their events re-rendered in
today's shapes, so the store's binding judges the old log and today's
steps after it by today's model (the clients' binding is not run: its
clients never saw the old log).

## Snapshots

Every read folds from the read model's latest snapshot (its state as
JSON at a position) and the events after it; one that folded
`grist.SnapshotEvery`'s events (default 1000) takes a new one. A read
model with fewer events reads as it always did, one read and no lookup.
Snapshots live in the store (`store.SnapshotStore`: memstore, simstore,
sqlstore's `grist_snapshots`), keyed by the read model's fingerprint:
its name, version, keys, the shapes of the events it folds and what its
fold makes of generated events. A fold changed in a way generated
events show gets a new fingerprint by itself; bump the version when the
fold's meaning changes in a way they may not (`<app> model` warns when
the fingerprint changed at the same version). A snapshot whose checksum
fails (torn), past the read's position or of another fingerprint is
never read. The state must survive JSON (exported fields of plain
values), or the read model is never snapshotted.

- `gristtest.Snapshots(t, App, st)`: the stored snapshots, and one at
  each position of the log, plus their tails, equal a fold from the start.
- `gristsim.Config{SnapshotEvery: 2}` explores decisions made from
  snapshots, through `store.<app>.snapshot` faults (torn, lost) and
  crashes; claim `qtest.Reaches("store.<app>.snapshot").Faulted()`.
- A deployed node audits one in 100 reads from a snapshot (refolds the
  key, compares, answers and replaces a wrong one: `grist.SnapshotAudit`
  to change the rate); alert on `grist_snapshot_audit_mismatches_total`.

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
