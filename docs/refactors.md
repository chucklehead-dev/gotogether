# Structural refactors: a review of the libraries against Go 1.26

What the libraries would gain from the language's and the standard
library's recent features, and from removing what they duplicate. The bar:
power inside the libraries, plainness at their surface. Generics are
welcome where they remove casts, `any` plumbing, reflection or per-type
copies, at a user's call site or inside the library; not where a user
must spell type parameters or constraints to use an API.

Findings are ranked by value to users and maintainers within each part:

- **A**, internal: no exported API changes (`api/*.txt` unchanged);
- **B**, additive exported changes;
- **C**, breaking exported changes, for the maintainer's decision.

Each says where, the shape now, the shape proposed, what it buys, the
risk, and its state: *done* (with the commit's subject), *proposed*, or
*rejected* (looked at, not worth it).

The maintainer's decisions (2026-10-27), after two design reviews of B4
and C1: B4 yes, with the reviewers' changes (below); C1 dropped; C2 yes;
C3 left as it is. Phase 1 covered the core module,
qhegel, qweave and qanalysis; later phases extend it to the rest
(§ Phase 2).

## A. Internal

### A1. Sorted map keys: `slices.Sorted(maps.Keys(m))` (done)

Where: every package of the core; qhegel, qanalysis. Fourteen private
helpers (`sortedKeys` ×7, `sortedNames` ×3, `keysOf` ×3, `sortedSet`,
`sortedIDs`, `sortedConsts`, bombadil's `sorted`), two of them an
insertion sort written out by hand (qhegel/replay.go,
cmd/gotogether/main.go), and about forty loops inline:

```text
keys := make([]string, 0, len(m))
for k := range m {
	keys = append(keys, k)
}
sort.Strings(keys)
```

Now:

```text
keys := slices.Sorted(maps.Keys(m))
```

Buys: about 300 lines fewer, one idiom where there were four (the
determinism rule "iterate maps in sorted order" reads as one expression),
no helper per package to keep in step. Risk: `slices.Sorted` returns nil,
not an empty slice, for an empty map; every result was checked to be
ranged over, joined, compared by length or kept in an unmarshalled field
(none is JSON-encoded where `[]` and `null` differ). Determinism is
unchanged: the order is the keys' sorted order, as before.

### A2. Typed sorts: `slices.SortFunc` with `cmp.Compare` (done)

Where: qenv/sim.go (`runnable`, every scheduling step), qenv/store.go,
qenv/qsql, qenv/faultpoint.go, qtrace/reconstruct.go, qload, qnemesis,
skills. `sort.Slice(x, func(i, j int) bool { return x[i].id < x[j].id })`
becomes `slices.SortFunc(x, func(a, b *task) int { return cmp.Compare(a.id,
b.id) })`, `slices.Sort` for ordered elements, and qnemesis's two pair
sorts share one `comparePairs` built with `cmp.Or`.

Buys: no reflection (`sort.Slice` builds a reflect-based swapper on every
call, on the scheduler's hot path), comparators that name the element
type. Risk: none for ordering. `sort.Slice` and `slices.SortFunc` are
generated from one pdqsort template and make the same comparisons and
swaps, so even ties land as before (a simulation's replay depends on it).
Float comparisons (qmeta's, NaN-sensitive) and `SliceStable` sites whose
comparator is not a plain key were left alone.

### A3. itf: struct fields read once per type, sort keys rendered once (done)

Where: itf/decode.go. `structFields(t)` parsed every field's `itf` and
`json` tags for every struct value decoded or encoded; `sort.Slice` over
sets' and maps' elements called `Value.String()` (which renders the whole
value) in the comparator, so each element was rendered at every
comparison.

Now: a process-wide `sync.Map` from `reflect.Type` to its fields (as
encoding/json caches its own), and `sortByString(xs, key)`, which renders
each key once and sorts pairs; the permutation is `sort.Slice`'s on the
same keys.

Measured (a state of 64 records in a map, a 64-name set and a list;
`BenchmarkDecodeState`, `BenchmarkEncodeState`, added): decode 124 µs →
102 µs (−18%), 1636 → 1311 allocations; encode 194 µs → 150 µs (−23%),
2980 → 1698 allocations. connect's StateDriver decodes a state per step,
validate's ITF reading decodes every state. Risk: the cached slice is
shared; the decoder only reads it (pointers into it were already taken
read-only).

### A4. One cache type for the process-wide caches: `internal/memo` (done)

Where: binding (`parsed` templates, `split` templates, `Model.types`),
validate (`cached` checkers, `models`, `evaluators`, `quintVersions`).
Seven `sync.Map`s, each with its own type assertions on `Load`, three
storing an `error` or a value in one slot (`if err, bad := v.(error)`),
and four computing a missing key in every goroutine that asked for it at
once (two parallel checks of a model typechecked it twice, each a quint
process; parallel manifests ran `quint --version` once each).

Now:

```text
// package memo
type Map[K comparable, V any] struct{ /* sync.Map of entries */ }
func (m *Map[K, V]) Once(k K, f func() V) V                 // kept whatever it is
func (m *Map[K, V]) Do(k K, f func() (V, error)) (V, error) // an error is retried next call

var models memo.Map[string, *binding.Model]
return models.Do(key, func() (*binding.Model, error) { return binding.LoadModel(quint, spec, module) })
```

Buys: no assertions, single-flight computation (callers for one key wait
for the first), each cache's error policy stated by the method it calls
(`Once` for the caches that kept errors, `Do` for those that did not).
Risk: a computation that asked for its own key would deadlock; none
does (each cache's function reaches other caches only).

### A5. Small idioms, each where it removes code (done)

- `sync.OnceFunc` for the release functions built from `var once
  sync.Once; return func() { once.Do(...) }` (qobs.InstallGlobal,
  qrun.Start, hooks.Strict). qrole's takes an argument and stays.
- `errors.AsType[*T](err)` (Go 1.26) where a pointer was declared only to
  be filled by `errors.As` (connect, qhegel ×2, qtest ×2): the variable is
  scoped to its `if`. Left: `errors.As` into a field (input) or an
  interface that is not an error (qobs's `Outcomer`).
- `strings.SplitSeq`/`bytes.SplitSeq` where a split was only ranged over
  (itf's tag options, per field; qenv's goroutine dump; the CLI), and
  `strings.Cut` for the first element of a split.
- `for b.Loop()` in otelio's benchmarks (the only `b.N` loops left).

### A6. "Did you mean": three edit distances, two bounds (proposed)

Where: internal/nearest (`Name`: optimal string alignment, case-blind,
one edit under 8 bytes, two above), qenv/internal/choice (`Nearest`) and
qnemesis (`nearest`), both Levenshtein with the looser `max(2,
len(name)/4)`. internal/nearest's own comment records why the looser
bound was dropped ("p1" offered for "q2"); fault-kind suggestions still
use it. Proposed: both call `nearest.Name`. Not done because it changes
messages (a transposition counts one edit, short names get fewer
suggestions), and tests assert on them: one commit, its message changes
listed, when the maintainer agrees.

### A7. Split the god files along their seams (proposed)

qenv/sim.go (1658 lines: run queue, waits, processes and crashes,
nemeses, settle, self-check), qtest/explore.go (1577), validate/search.go
(1537). Moves only: `qenv/runq.go` (enqueue … kth), `qenv/process.go`
(Process … OnCrash), `qenv/settle.go` (Settle … SettleCore), the nemesis
scheduling beside it. Buys navigation, smaller diffs for concurrent
work. Risk: none in behaviour; conflicts with work in flight, so do it
when nobody is in those files. explore.go's verdict paths are claims'
semantics: move, never edit, there.

### A8. Three copies of a report's reason (proposed)

`reasonOf(*validate.Report)` is written three times, identically
(qshrink/samefailure.go, validate/stepmutants.go, qtest/stepmutate.go):
the verdict's reason, else "unsettled", else "fails". It belongs to
validate (B2 below); until then it can stay.

### A9. Looked at, left alone (rejected)

- `min`/`max` builtins for `math.Min`/`math.Max`: all on floats, already
  clear; a change buys nothing.
- `unique.Handle` for interned names (actions, fault kinds): the maps
  keyed by them are small, and string comparison is not in any profile.
- `weak`/`runtime.AddCleanup`: qenv/qhttp already uses them for its
  per-simulation deadlines; no finalizers remain.
- `context.AfterFunc`: no goroutine waits on `ctx.Done()` to act.
- `testing/synctest`: used where a test of the native runtime needs
  virtual time; qnemesis's `time.Sleep`s wait on real processes, which a
  bubble cannot hold.
- `t.Context()` for `context.Background()` in tests: it is cancelled
  before cleanups run, which several tests' cleanups would see; no gain
  worth auditing each.
- `log/slog`: the libraries report through errors and `testing.TB`, by
  design; nothing logs.
- `os.Root`: user paths are joined only under directories the toolkit
  creates (KEEP, evidence); nothing reads a path an untrusted input names.
- json/v2: an experiment; not adopted.

## B. Additive exported changes

### B1. A recording DB for qhistory (proposed)

examples/wallet/history.go is 110 lines that wrap `qenv.DB` and
`qenv.Txn` (Begin, Get, GetForUpdate, Put, Commit, Rollback) to record a
list-append history for `qhistory.Check`, with the subtleties a user must
know (record the index before Begin yields, a commit of unknown outcome
stays Unknown, a Rollback after Commit records nothing). Every user of
qhistory on a qenv store writes it again. Proposed, in qhistory:

```text
// Record wraps db so its transactions' reads and appends on the keys
// lists names are recorded in h (a key's value as a list of tags).
func Record(db qenv.DB, h *History, lists func(key string, val any) (list []int, ok bool)) qenv.DB
type History struct{ Txns []Txn }
```

Buys: the example's file becomes one call; the outcome rules live where
qhistory's checker reads them. Risk: qhistory then imports qenv (it
imports neither now); a subpackage `qhistory/qenvhist` keeps it light.

### B2. `(*validate.Report).Reason()` (proposed)

The reason a failing report fails, as A8's three copies compute it, for
qshrink, qtest and users' own summaries. One method, a line in
api/core.txt.

### B3. Iterator methods beside slice methods (rejected)

Checked: `Recorder.Steps`, `Counts.Steps`, `qtrace.Reconstruct`. Each copies under a lock or builds a new slice the
caller keeps; an `iter.Seq` could not hold the lock across the caller's
loop body, and the copies are small. No API gains by an iterator.

### B4. Typed store keys (done: "qenv: Table[T], an app's typed records over a Txn")

Users keep a `qenv.Store` value as `any` and assert it back at each read
(examples/wallet's `entries(v)`, grpcledger's, quorumkv's); a wrong
assertion is a zero value or a panic at run time, never a compile error.
A typed handle names the key's type once, where the key is declared.

(a) Today, and with the proposal:

```text
// today                                   // proposed
func accountKey(a string) string {         var accounts = qenv.Keys[[]Entry]("acct/")
	return "acct/" + a
}                                          es, ok := accounts.Get(tx, a)
v, ok := tx.Get(accountKey(a))             es, ok, err := accounts.GetForUpdate(tx, a)
es, _ := v.([]Entry)                       accounts.Put(tx, a, append(es, e))
tx.Put(accountKey(a), append(es, e))
```

(b) Benefit, argued: no assertions at read sites (wallet has five, the
examples about 30), a mistyped value refused at compile time, and a
prefix declared once (ScanPrefix's ranges follow from it). No speed
change: the store still holds `any`.

(c) A newcomer must understand: one type argument at the declaration
(`qenv.Keys[[]Entry]`), after which calls look like plain methods; that
`Txn.Get` still works for untyped keys. Nothing else changes.

Decided: yes, with the reviewers' changes, as built. The name is
`qenv.Table[T]`, made by `qenv.NewTable[T](prefix)` (`Keys` would read
as `Store.Keys`). It is a value over Txn: `Get`, `GetForUpdate`, `Put`,
`Delete`, `Scan` and `ScanRange` (`[]Row[T]`, as Txn's scans return
`[]KV`), `Key`, `Prefix`, `Range`; each makes the one Txn call its raw
form makes, so levels, commit faults, the history check and Txn
wrappers see the same transaction (`TestTableIsItsRawCalls`). Its
methods take the key after the prefix, and a key that starts with the
prefix is not refused: it may be a legitimate suffix (prefix "a",
account "ab"). A typed read of a value of another type panics naming
the key, the type found and T. The doc says what the proposal's
example missed: values are shared, so clip before appending
(`append(slices.Clip(es), e)`). The faults skill says when to use a
Table, raw Txn or qsql; examples/wallet's accounts and
examples/checkout's orders and stock are ported.

## C. Breaking exported changes (not implemented)

### C1. `ShrinkActions` infers its action type (dropped by the maintainer)

(a) Today, and with the proposal:

```text
// today
shrunk := qtest.ShrinkActions(t, journey, qtest.ActionShrink[Action]{
	Binding: "webjobs.binding.yaml", Rerun: client.Play, Reset: server,
})
// proposed: Rerun a parameter, A inferred from it, the struct not generic
shrunk := qtest.ShrinkActions(t, journey, client.Play, qtest.ActionShrink{
	Binding: "webjobs.binding.yaml", Reset: server,
})
```

with

```text
func ShrinkActions[A any](t testing.TB, original []A, rerun func(context.Context, []A) ([]qtrace.Step, error), s ActionShrink) *ShrunkActions[A]
```

(b) Benefit: the type argument disappears from the call (a composite
literal of a generic type must spell it; a function's argument infers
it), and a Rerun of the wrong action type fails at the call, against
`original`, with the inferred type in the message.

(c) A newcomer must understand: nothing generic at the call; the
result's type `*ShrunkActions[Action]` is named only if they store it.

Breaking: ShrinkActions has one user outside its tests (examples/webjobs)
and one line in the workflow skill's verdicts reference. Recommendation:
do it before the first tag.

### C2. qcover's Go-coverage reader out of the simulated package (done: "qcover/gocover: Go's coverage reader out of the simulated qcover")

qcover is held to the simulation's rules (woven into simulated code,
translated by gosim), yet `ReadGoCover` runs `go tool covdata` (os/exec)
and `ParseGoCover` reads its text: host-only code that gosim translates
along with the rest (found by chiasmus's Prolog reachability over the
import graph, below). Proposed: `qcover/gocover` with `Read`, `Parse`,
`Profile`, `Block`, and `Compare` taking it. Recommendation: yes, cheap
now, a rename later. Decided: yes, and done; internal/simdeps fails if a simulated
package of the core reaches os/exec again.

### C3. Options or Config: one name for one pattern (decided: left as it is)

Config structs are named both ways: `LinearizeOptions`, `ReplayOptions`,
`VaryOptions`, `SearchOptions`, `ITFOptions`, `validate.Options`, beside
`ExploreConfig`, `SweepConfig`, `ViolationConfig`, `ServerConfig`,
`ClientConfig`, `SimConfig`, `RuleConfig`, `connect.Config`; functional
options are `qtest.Option`, `qobs.Option`, `qtrace.MutationOption`.
The split is mostly consistent (functional options where a check takes a
variable list of independent switches, structs where a call takes one
shape), the names are not. Recommendation: leave it; a rename of a dozen
types buys users nothing they would notice.

## Phase 2: grist, the CLI, qanalysis, the examples

- grist (proposals only while GR3 slice 3 is under way): the same A1
  pattern in eight files (`sortedKeys` in grist.go, `sortedNames` in
  gristtest/exercised.go, six loops inline). grist's generics sit where
  they should: a user spells one type argument per declaration
  (`grist.NewEvent[Deposited](app, "deposited")`, 52 in the examples;
  `NewCall[Req, Resp]`, 16), and handlers infer theirs (`grist.On`, never
  spelled).
- The examples: the A1 pattern in ten files of ten modules (counters,
  glomers ×2, sktrace, payd, crossdb, edgepublish, fleetrollout,
  streams ×2). Not changed: each is its own module with its own suite;
  worth doing when each is next touched, since users copy their idioms.
- qanalysis (proposed): quintsim and quintweave delegate to qlint's
  syntactic checks, shared with `gotogether lint`, and so see only calls:
  `now := time.Now` or `draw := rand.IntN`, a function value called
  later, escapes them. The analyzer has types (`pass.TypesInfo.Uses` of
  the selector is the `*types.Func` whatever the import's name); a
  check of every use, not every call, would close it. A finding more,
  never one fewer: a behaviour change of the lint, to state in the
  CHANGELOG. Done ("qlint, quintsim: the wall clock and math/rand as
  function values"): qlint's rule finds values, by name in the CLI and
  by type in quintsim (`qlint.SimFileTyped`). qweave's receiver match does not unalias (`types.Unalias`)
  a receiver's type; harmless today, since its join points name sync's
  types, whose methods are declared on no alias.
- cmd/gotogether: done with the core (A1, A5). Its errors wrap with `%v`
  where `%w` would let a caller's `errors.Is` see a missing file (21
  such in the core): a behaviour change, though only additive; proposed
  for one sweep. Done ("core: errors wrap their cause with %w"): 24
  sites, found by type (an `error` argument of a `%v` verb in
  `fmt.Errorf`); three left as they are, whose cause no caller can test
  (a duration's parse error, validate's unexported errNoEnd) or may be
  nil (shrink's argv, where `%w` would print `%!w(<nil>)`).
- The examples' simulators (`cmd/*-sim`, four) each write
  `qenv.WithContext(c, qobs.WithRecorder(context.Background(), rec))` and
  the same seed loop around a FileSink; to be weighed against
  `gotogether simulate`, which may already cover them.

## How the findings were found

Plain reading, `grep` for the idioms each feature replaces, `go vet`,
and two MCP tools, assessed here for the maintainer:

- **chiasmus** (tree-sitter call graphs, Prolog/Z3). `chiasmus_map` over
  the core's 174 files (~490K tokens) gave a usable outline of exports
  with signatures, cheaper than reading. `chiasmus_graph dead-code` named
  16 functions, all false positives: functions reached only as values
  (qenv/keyed.go's `lockKey` … assigned to `keyed.Lock`), test-only
  helpers (it reads no `_test.go` files unless given them: `orderKey`,
  `linearizeProof`, `qeval.sameState`), and common names it cannot
  resolve across packages (`filter`, `settled`). `cycles` lists
  recursive functions (encode/decode), not package cycles, which Go
  forbids anyway. Most useful: `chiasmus_verify` with Prolog over facts
  generated from `go list` (`imp(pkg, import)`, tabled reachability),
  which answered "which simulated packages reach os/exec, net/http,
  yaml, validate or binding?" in one query and found qcover → os/exec
  (C2); the same program confirmed no package reaches qtest but qtest's
  users. The solver is the value; the call-graph extraction is too
  coarse for Go (no types, no method sets, no function values).
- **wavescope** (wavelet summaries). `get_important_positions` over
  qenv/ ranked test files first and mislabelled positions ("func i",
  "struct make": identifiers inside bodies); not usable for a Go
  codebase's structure. `get_complexity_heatmap` on qtest/explore.go,
  averaged over 40-line windows, did rank `runOne` (the exploration's
  most branching function) and the verdict cache first: a plausible
  reading order for review, nothing grep and file sizes did not show.
