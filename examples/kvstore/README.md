# examples/kvstore: an HTTP service checked against its model

A key-value service with versioned compare-and-swap:
- `kv/`: the store, the HTTP server and a client;
- `cmd/kvserver`: the service;
- `cmd/kvload`: a concurrent load generator;
- `testdata/mutants/`: the store's planted bugs, each a directory whose
  files replace `kv/`'s of the same names (a `go build` overlay): the
  store's own files carry no bug, and each mutant is a whole file to
  diff against them.

`model/kv.qnt` is the service's Quint model.

The service is instrumented the way a production service would be. It
uses no Orchestrion and no build changes:
- the store reports each operation as a model step (`qobs.Record`) from
  inside the critical section that orders it;
- the HTTP server emits one ordinary OTel span per request, with the
  attributes an operator wants anyway.

Run it, put load on it, then check what it recorded:

```sh
./run-demo.sh                               # faithful: both checks pass
MUTANT=staleCAS ./run-demo.sh               # a race: two CAS with the same expected version both win
MUTANT=revisionOnConflict ./run-demo.sh     # a failed CAS advances the revision
MUTANT=staleRead ./run-demo.sh              # reads from a lagging snapshot
MUTANT=lateRevisionOnConflict ./run-demo.sh # the revision bug after the first rotation: the last log alone shows it
MUTANT=staleSnapshot ./run-demo.sh          # a rotated log headed by an old state
```

```
$ gotogether validate -binding kv.binding.yaml -convert kv.convert.yaml spans.jsonl      (MUTANT=staleCAS)
  conformance: FAIL
    step 112 is not a transition of the model from the state reached
      quint:    cas("a", 39, "y", Ok)
      observed: cas(expected=39, key=a, value=y) (seq 112, span 85ea63b66b1bd28e)
      obs:      revision=46
```

Or by hand:

```sh
go run ./cmd/kvserver -addr 127.0.0.1:8080 -steps steps.jsonl -spans spans.jsonl &
go run ./cmd/kvload -addr 127.0.0.1:8080 -clients 8 -ops 100
kill -INT %1
gotogether validate -binding kv.binding.yaml steps.jsonl                              # the model steps
gotogether validate -binding kv.binding.yaml -convert kv.convert.yaml spans.jsonl     # the plain spans
```

The demo then runs the service again with its step log rotated
(`-rotate 120`) and validates the last file alone (below, *A rotated log,
checked alone*); the two rotation mutants show only there.

## What it shows

- **Checking a concurrent service.** The model is sequential. A
  concurrent run conforms if some serial order explains it: every CAS
  outcome, revision and read must be consistent. The recording gives that
  order:
  - steps are recorded under the store's lock;
  - for spans, the server stamps `kv.op`, the store's linearization
    index, and `kv.convert.yaml` uses it as an ordering key
    (`order: { op: attr:kv.op }`). Without it, spans of concurrent
    requests would be ordered by end time, which is not the order the
    store applied them in.
- **Errors that classify themselves.** `ErrConflict` and `ErrNotFound`
  implement `QuintOutcome()`. The binding maps `conflict` and `missing` to
  the model's `Outcome` values, so a CAS's outcome is checked as well as
  its effect.
- **Reads checked against the model.** A read is a model step that
  changes nothing. Its `expect` checks what it returned
  (`obs.found`, `obs.value`, `obs.version`) against the model's store.
- **Long runs.** A 400-step run is replayed in segments, because Quint's
  Rust evaluator limits `.then` nesting to about 50.
- **A rotated log, checked alone.** `kvserver -steps steps.jsonl -rotate
  N` starts a new step log every N operations (`steps.1.jsonl`,
  `steps.2.jsonl`, ...; `kv/rotate.go`). The recorder is the same, so the
  seqs go on across files, and a later file alone has lost its prefix:
  replayed from `init` it would be judged against an empty store, and
  validation says `error lost-prefix`. So the store heads each new file
  with its whole state, a step of the model's `restore(values, versions,
  rev)`, which assigns every state variable from its arguments. It
  records it under the lock that orders operations, right after the N-th,
  so no operation is in flight across the head. The binding names it
  (`snapshot: restore`): a file that starts with it is judged from that
  state (`info lost-prefix: judged from the state recorded by restore at
  seq 153`). The store is a map of records, which a recorded map cannot
  carry (`toMap` renders plain values), so the snapshot records two maps,
  values and versions, and `restore` zips them. A snapshot inside a
  longer trace (all the files together) is skipped: only a log's head is
  trusted as its start. So the head itself is checked only by what
  follows it: a stale one (the `staleSnapshot` mutant) shows in the next
  operation, and not at all in the files read together.
- **What the load must reach.** A load that never conflicted would
  conform and say nothing of the CAS. The test lists the service's
  blocks by outcome, each with how a load reaches it (a hand-built
  `qtest.Catalog`, as grist builds an App's), and claims the run took
  every one (`catalog.Exercised()`): one missed is named with its hint.
- **Mutants as overlays.** `TestMutants` (`qtest.Mutants{Package: "kv"}`)
  builds the tests once per directory of `testdata/mutants`, each with
  its files in place of `kv/`'s, and runs `TestMutant`, which says what
  catches the mutant it was built with. A failing run's reproduce and
  navigate lines name the mutant.

## Tests (`kvstore_test.go`)

| Test | Direction | Shows |
|---|---|---|
| `TestServiceUnderLoadConforms` | 8 concurrent clients; the handlers' steps recorded into the run (`qobshttp.Handler`, in parallel) and checked (`qtest`) | a concurrent run is a serial behaviour of the model, which took every block of the service's catalog (`catalog.Exercised()`: each CAS and delete by outcome) |
| `TestServiceSpansConform` | the same load; plain spans converted and checked | spans plus `kv.op` give the same verdict |
| `TestClientHistoryLinearizes` | 4 clients record their own operations, with invocation times (`kv.LoadRecorded`) | in recording order the faithful store is rejected; `qtest.Linearize()` finds the order the intervals allow |
| `TestRotatedLogConformsAlone` | 4 clients, the log rotated every 40 operations; the last file alone | judged from its head's snapshot it conforms; without the head, the lost prefix's error |
| `TestModelDrivesService` | model traces drive the HTTP API (`connect.Run`) | store and revision match the model after every request |
| `TestHegelDrivesService` | a Hegel state machine over HTTP; runs checked after the test | |
| `TestMutants` | each mutant of `testdata/mutants`, built with its overlay, runs `TestMutant` | each caught, as below |

| Mutant (`TestMutant`) | The bug | Caught by |
|---|---|---|
| `staleCAS` | the version checked under a read lock, written under the write lock | the converted spans of a concurrent load: a CAS the model cannot take (`Rejects("cas")`) |
| `revisionOnConflict` | a failed CAS advances the revision | a put and a stale CAS, recorded: `Mismatch("cas")`; in full mode Hegel's machine, every case checked, shrinks to the failed CAS alone |
| `staleRead` (full mode) | reads from a snapshot refreshed every fourth read, client-recorded | a read no order real time allows explains (`Mismatch("read")`) |
| `lateRevisionOnConflict` | the revision bug from the first rotation on | the last file alone shows the failed CAS (`Mismatch("cas")`), judged from its head |
| `staleSnapshot` | each head is the previous rotation's state | the last file's first operation disagrees with its head (`Mismatch` or `Rejects`) |

```sh
go test ./examples/kvstore/ -v
```
