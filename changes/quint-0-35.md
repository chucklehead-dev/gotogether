- CI, the Action's `quint-version` default, the image and
  `ci/install-quint.sh` move to Quint 0.35.0 with its Rust evaluator
  v0.8.0 (checksums in `ci/quint-evaluator.sha256`); the installer admits
  only that pair. Stable Quint 0.33.1 or later is still admitted at run
  time; install hints (admission errors, `gotogether doctor`) name 0.35.0.
- `gotogether simulate`'s possibles read quint 0.34's `--witnesses`
  counts, now printed as `P was observed in n trace(s)` under
  `Observations:`; 0.33's `was witnessed in` is still read. With 0.35 the
  predicates' check failed with "quint's output names no count of
  witness".
- `invariant` and `observation`, quint 0.34's definition keywords, stay
  usable as parameter names (quint's grammar takes them as names), so
  `quintnames.Keywords` does not rename them.
- The examples' compiled models and connect's are compiled again with
  quint 0.35.0 (its compile output gains an `invariants` field; nothing
  else changes in meaning).
- grist's generator renames a command's or event's field named
  `invariant` or `observation` (`invariant_`), as it does `from` and `as`:
  its `QuintTaken` holds every token of quint's lexer.
