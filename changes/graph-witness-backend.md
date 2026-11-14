- `simulate -graph`'s witness replay (and `qtest.Refutes`, which uses it)
  runs `quint test` on `QUINT_BACKEND`'s backend, as sampling does: it ran
  on quint's default, fetching the Rust evaluator for a run asked to use
  TypeScript (issue #220).
