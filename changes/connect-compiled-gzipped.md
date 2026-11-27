- A spec's compiled model for connect is kept gzipped by convention:
  `connect.CompiledFor` (and so `gotogether compile -spec S [-main M]`,
  `GOTOGETHER_COMPILED` and `connect.RequireTraces`) names
  `<stem>.mbt.json.gz` or `<stem>.<main>.mbt.json.gz` (`connect.CompiledExt`),
  no longer `.mbt.json`. The format is still `gotogether.mbt/1`:
  `MBTModel.Write` gzips a path ending in `.gz` with a header naming no
  file, time or OS, so a compile twice writes the same bytes; `LoadMBT`
  reads the plain and the gzipped form alike, and refuses a broken
  container with the fix. A plain artifact an earlier gotogether kept
  beside the spec is not found; `GOTOGETHER_COMPILED=1`, a run without
  quint, `RequireTraces`' skip and `gotogether doctor` name it, with the
  compile (DESIGN §113). connect's bank model went from 105,630 bytes to
  4,998.
- Where quint is installed and `GOTOGETHER_COMPILED` is unset, connect
  still runs quint whatever the artifact beside the spec is, and now logs,
  once a process, that a stale (or plain) one is passed over (`passing over
  the spec's compiled model`), with the compile that refreshes it: a run
  without quint fails on it.
- `gotogether doctor` lists connect's compiled models by spec and main
  module: each spec a connect test of the module names (a literal
  `Config{Spec: ..., Main: ...}`) as `none` with the `gotogether compile
  -spec` that writes one, `current` or `stale` (a skew), and every
  `.mbt.json.gz` found that no test names as an `orphan` (its spec gone),
  `misplaced` (where connect does not look) or `plain` (an earlier
  gotogether's `.mbt.json`), each with the fix. `-compiled` takes connect's
  artifacts too.
- `gotogether compile -h` says what `-spec` writes (`gotogether.mbt/1`,
  `<stem>[.<main>].mbt.json.gz`), and the mbt skill, README and
  docs/agents/commands.md name `connect.RequireTraces(t, cfg)` as a connect
  test's guard: a check for quint skips where the compiled model would run.
