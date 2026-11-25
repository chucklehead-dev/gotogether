- `gotogether doctor` lists the module's bindings (YAML files with `spec:`
  and `module:`, outside testdata and nested modules) by their compiled
  models: `none`, with the `gotogether compile -binding B` that writes one;
  `current`; or `stale`, a skew with the fix. Without quint it compares the
  binding's, the sources' and the evaluation module's hashes, as
  `validate -compiled` and qtest do; `gotogether compile -check` still
  compares a compile now by meaning (ROADMAP Q0).
- 22 examples' bindings keep their compiled models, and 125 of their tests
  are on `qtest.RequireModel`: CI runs them with quint off PATH
  (`ci/compiled-examples.sh`), and `ci/needs-quint.txt` says what still
  needs quint and why.
