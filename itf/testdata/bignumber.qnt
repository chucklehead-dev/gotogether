// Integers of 16 characters and more, which quint 0.33's Rust backend
// writes into ITF as bignumber.js objects, in every place a value goes,
// beside records of the very fields s, e and c (bignumber_test.go; the
// traces beside it are quint's: bignumber.rust.itf.json and
// bignumber.typescript.itf.json, `quint run --mbt --max-steps 1
// --max-samples 1 --backend <b> --out-itf bignumber.<b>.itf.json`).
module bignumber {
  type Digits = { s: int, e: int, c: List[int] }
  type Wrapped = Big(int) | Shape(Digits) | Neither
  var ints: List[int]
  var shape: Digits
  var both: (int, Digits)
  var byInt: int -> Set[Digits]
  var wrapped: List[Wrapped]

  pure val P53 = 9007199254740992
  pure val MAX = 9223372036854775807
  action init = all {
    ints' = [0, 999999999999999, 1000000000000000, -99999999999999, -100000000000000, -999999999999999,
             P53 - 1, P53, P53 + 1, MAX, -MAX - 1, 4611686018427387904, 120000000000000000,
             10000000000000000 + 1, 1000000000000000000],
    shape' = { s: 1, e: 15, c: [10] },
    both' = (-MAX - 1, { s: -1, e: 18, c: [92233, 72036854775808] }),
    byInt' = Map(P53 + 1 -> Set({ s: 1, e: 15, c: [90, 7199254740993] })),
    wrapped' = [Big(MAX), Shape({ s: 1, e: 18, c: [92233, 72036854775807] }), Neither],
  }
  action step = {
    nondet n = Set(-MAX - 1).oneOf()
    nondet d = Set({ s: 1, e: 15, c: [10] }).oneOf()
    all { ints' = ints.append(n), shape' = d, both' = both, byInt' = byInt, wrapped' = wrapped }
  }
}
