# Changelog

Every published module (the core, qhegel, qweave, qanalysis, qgosim) is
versioned together; the examples are not published. A change to the
core's exported API updates `api/core.txt` and adds a line here.

## Unreleased

The first release. What a user meets, by module:

- **The core** (`github.com/chucklehead-dev/gotogether`). The model drives
  Go (`connect`); trace validation (`qobs`, `qtrace`, `otelio`, `convert`,
  `binding`, `validate`, `input`); simulated environments with fault
  injection (`qenv`, `qtest.System`); metastability (`qload`, `qmeta`,
  `qrole`); coverage, mutants and sweeps (`qcover`, `qmut`, `qsweep`); the
  `gotogether` CLI and the agent skills. Its dependencies: OpenTelemetry
  and yaml.v3.
- **qhegel**: Hegel drives and the model judges and shrinks.
- **qweave**: weaving into a `go -overlay` (`//quint:action`, qenv's
  sync, coverage, mutants).
- **qanalysis**: go/analysis passes (`quintvet`, a golangci-lint plugin)
  and the static action map.
- **qgosim**: qenv and whole programs in gosim, on the fork
  `github.com/chucklehead-dev/gosim`.

Before this, the project was quint-connect-go
(github.com/casselc/quint-connect-go); docs/HISTORY.md has the path here.
