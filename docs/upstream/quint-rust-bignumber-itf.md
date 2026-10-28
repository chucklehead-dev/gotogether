<!-- Drafted 2026-10-28 (ROADMAP I11's note), not filed: the maintainer files it at github.com/informalsystems/quint. The toolkit works around it: itf.ParseWith reads such values by the model's types. -->

### Rust backend: integers of 16+ characters come out as bignumber.js records `{ s, e, c }` (console and `--out-itf`)

**quint version:** 0.33.0, `--backend rust` (the TypeScript backend is correct)

**Summary.** With the Rust backend, every integer whose decimal text is longer than 15 characters (sign included: from 10^15 up, and from -10^14 down) is shown and written as a record of fields `s`, `e` and `c` instead of an integer. On the console:

```
[State 1] { x: { s: 1, e: 15, c: [10] } }
```

and in `--out-itf` (both `quint run` and `quint test`):

```json
{"x": {"s": {"#bigint": "1"}, "e": {"#bigint": "15"}, "c": [{"#bigint": "10"}]}}
```

where ITF's encoding, and the TypeScript backend's output, is `{"x": {"#bigint": "1000000000000000"}}`. The value isn't even close to 2^53: 10^15 is affected.

**Reproduction.** `iss.qnt`:

```
module iss {
  var x: int
  action init = x' = 0
  action step = x' = x + 1000000000000000
  val small = x < 2000000000000000
}
```

```
$ quint run iss.qnt --backend rust --invariant small --max-steps 3 --max-samples 1 --seed 1
An example execution:

[State 0] { x: 0 }

[State 1] { x: { s: 1, e: 15, c: [10] } }

[State 2] { x: { s: 1, e: 15, c: [20] } }

[violation] Found an issue (26ms at 38 traces/second).

$ quint run iss.qnt --backend typescript --invariant small --max-steps 3 --max-samples 1 --seed 1
[State 1] { x: 1000000000000000 }

[State 2] { x: 2000000000000000 }
```

`quint run iss.qnt --backend rust --max-steps 2 --max-samples 1 --out-itf t.itf.json` writes the objects above into the ITF. The same happens for integers in every position (lists, tuples, map keys and values, sum payloads, `mbt::nondetPicks`) and for `quint test --out-itf`.

Some values, as the Rust backend writes them:

| integer | ITF written |
|---|---|
| 999999999999999 | `{"#bigint": "999999999999999"}` |
| 1000000000000000 (10^15) | `{"s": 1, "e": 15, "c": [10]}` |
| -99999999999999 | `{"#bigint": "-99999999999999"}` |
| -100000000000000 | `{"s": -1, "e": 14, "c": [1]}` |
| 9007199254740993 (2^53+1) | `{"s": 1, "e": 15, "c": [90, 7199254740993]}` |
| 9223372036854775807 | `{"s": 1, "e": 18, "c": [92233, 72036854775807]}` |
| 10000000000000001 | `{"s": 1, "e": 16, "c": [100, 1]}` |

(each number inside is itself a `{"#bigint": ...}`; abbreviated here).

**Cause (as far as we can tell from 0.33.0's dist).** `dist/src/rust/commandWrapper.js` parses the evaluator's output with `json-bigint`'s default `JSONbig.parse`, which turns every JSON number written in more than 15 characters into a bignumber.js `BigNumber` (fields `s`: sign, `e`: exponent, `c`: coefficient in base-1e14 limbs). The evaluator writes integers as plain JSON numbers, so `itf.js`'s `ofItfValue` gets a `BigNumber` object: it is no `#bigint`, no `typeof 'number'`, and falls through to "any other object must represent a record", so the integer becomes the record `{ s, e, c }`. Everything downstream (console output, `--out-itf`, presumably the REPL's and `quint test`'s reporting) sees the record.

**Possible fixes** (any one):
- parse with `JSONbig({ useNativeBigInt: true })` (or `storeAsString`) and accept a native `bigint` in `ofItfValue`;
- have the Rust evaluator write integers as ITF's `{"#bigint": "..."}` strings, as the ITF ADR specifies, so that no JSON number needs precision;
- at least, recognise a `BigNumber` in `ofItfValue` (`BigNumber.isBigNumber(value)`) before the record case.

**Why it matters.** A tool reading quint's ITF can't tell such an object from a model's genuine record with fields `s`, `e` and `c` (e.g. `type Digits = { s: int, e: int, c: List[int] }`, which the Rust backend writes with the same shape, `{"c": [...], "e": ..., "s": ...}`) without the variable's type, so traces with large integers (timestamps in microseconds, balances in minor units, 64-bit ids) are read wrong, or must be refused. The trace's JSON key order differs (`s, e, c` for a BigNumber, sorted for a record), but JSON key order is not semantic.

**Related, smaller.** The Rust backend refuses the literal `-9223372036854775808` (QNT600: "Integer literal 9223372036854775808 is outside i64 range"), since `-` applies to the literal 9223372036854775808. i64's minimum can only be written as `-9223372036854775807 - 1`, so a value read from a Rust trace and written back as a Quint literal (a replay of the trace) fails on Rust and passes on TypeScript.
