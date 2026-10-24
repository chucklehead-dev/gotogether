# Static analysis (qanalysis, the binding lint)

Gotchas for agents (and humans) before changing qanalysis or the binding lint; AGENTS.md has the general ones.

- quintaction's scope check uses `LookupParent(name, body.Lbrace)`:
  go/types declares a function body's top-level locals in the same scope
  as its parameters, so a lookup without the position finds locals.
  Its analysistest fixtures are GOPATH-style with stub qenv and qobs
  packages (`qanalysis/testdata/src/github.com/...`): keep the stubs to
  what the fixtures use. An expectation on a `//quint:action` line goes
  in a block comment before the directive.
- actionmap stops at functions outside the loaded packages: a library
  that calls back (qenv's commit hooks) is one call-graph node for every
  closure handed to it.
- `gotogether lint -binding -src` and quintaction (`-binding`) check literal
  actions passed to `qobs.Record` and `Recorder.Record`, in files that
  import qobs. An action through a constant or variable is not seen, and
  a call on another imported package's `Record` (qtest's) is skipped by
  its name; the analyzer confirms each call by type, the CLI cannot.
