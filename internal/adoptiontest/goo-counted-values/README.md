# Exact large-count arithmetic rehearsal

An agent-assisted, bounded adaptation of gotogether to the real public
`exploration.Source.FetchCountedValues` executor in Goo revision
`0659a4b20fb7fe83bbfd32a194ceb9a7d0c339a9`. An HTTP transport double supplies
source responses; the actual executor supplies every observation. The driver
contains no sum or expected-state classifier. The independent model was authored
from the documented contract without reading the executor or source contrast.

Existing Goo reference tests already cover ordinary counted-values behavior.
This trial adds the numeric boundary they do not reach with 140 generated facts:

| Total | Group counts | Mathematical classification |
|---|---|---|
| 0 | empty | no_data |
| 9007199254740993 | 9007199254740993 | observed |
| 18446744073709551615 | 18446744073709551615 | observed |
| 0 | 18446744073709551615, 1 | changed_between_reads |

Individual counts fit uint64; the last sum is 2^64. The model uses unbounded
integer addition and compares the public state with that arithmetic result.
The model checks returned count multiplicities without assuming group order.
The representation gate pins this fixed fixture/executor's observed list order;
it does not establish arbitrary output-order preservation.
This is a targeted regression and evidence-usability trial, not a production bug
finding, exhaustive API proof or independent-maintainer adoption.

## Reproduce

Go with automatic Go 1.27 selection, Python 3 and Quint 0.33.0 are required.
Build the current checkout's CLI, then run:

```sh
mkdir -p "$PWD/bin"
go build -o "$PWD/bin/gotogether" ./cmd/gotogether
PATH="$PWD/bin:$PATH" internal/adoptiontest/goo-counted-values/reproduce.sh
```

The script explicitly uses `GOTOGETHER_EVAL=quint` and
`QUINT_BACKEND=typescript`. Both the Go evaluator and Quint's Rust backend have
signed-int64 limits for this model; their refusal is a tool error and cannot
count as detecting the source contrast. The manifest records the TypeScript
backend; reproduction also selects the Quint evaluation path explicitly.

`FREEZE.sha256` retains the model, binding, observing driver, cases and exact
published dependency pins. The freeze followed a successful native JSON → Quint
→ ITF → bundle/replay → native ITF reading gate. Commit `b67fe73a` records the
freeze before creation of the source contrast. A later comment-only observer
documentation update refreshed its checksum; Go scanner tokens were verified
identical to that original freeze. Model, binding and observing logic stayed
unchanged. Never revise the frozen model to
make a contrast fail or a faithful execution pass.

The script works on disposable copies and leaves fresh results in `logs/run-*`.
It checks exact native `json.Number` scalars and JSON-text integer collections,
raw public decimal strings, and native ITF `big.Int` values and argument picks.
`cmd/inspect-itf` rejects a one-unit rounding negative control. Collections here
use the documented JSON-text transport and typed `toQuint "List[int]"` binding
helper; this is not a test of arbitrary nested native-JSON integer decoding.

Only after the exact-value replay gate passes, `make_contrast.py` copies the
pinned library outside the module cache and replaces its `big.Int` group sum
with wrapping uint64 addition. Original and changed source hashes are recorded
in `PROVENANCE.json`. Neither the Goo checkout nor its module cache is edited.
The frozen model rejects the real executor's erroneous `no_data` at sequence 4.
Missing observations instead produce exit 2. Native bundle export and relocated
replay retain pass/fail after every original project path disappears.

The native report identifies the offending captured action and observations.
`gotogether nav -run <steps> -record '<RecordRef JSON>' -json` inspects that exact
record without invoking model navigation. TypeScript supplies no failing
post-state ITF here: the checker reports that absence instead of inventing state.
Counterfactual Go-evaluator navigation of these large integers is outside scope.

SQL correctness, real ClickHouse cardinalities, group identity, null/empty value
distinctions, query/version echo, clocks, bases, UI ownership, concurrency and
performance are outside this model. Their presence in the full response does not
mean this projection checked them. See [FRICTION.md](FRICTION.md) for the lessons.
