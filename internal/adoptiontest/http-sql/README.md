# HTTP/SQL cold-start rehearsal

A bounded, instruction-isolated adoption experiment using an uninstrumented
net/http + database/sql service copied from payd. A fresh agent authored the
model, binding and actual HTTP/SQLite driver using public documentation and a
published toolkit version, then froze them before receiving a source contrast.
See [TRIAL.md](TRIAL.md) for scope, friction and limitations. This is an internal
rehearsal, not independent-maintainer adoption or a newly discovered bug.

## Reproduce

With Go matching go.mod, Python3 and Quint available:

```sh
mkdir -p "$PWD/bin"
go build -o "$PWD/bin/gotogether" ./cmd/gotogether
PATH="$PWD/bin:$PATH" internal/adoptiontest/http-sql/reproduce.sh
```

The packaging checker consumes current report/2; the frozen SDK pin is unchanged.
The script enforces exact exit codes and structured verdicts for faithful
completion, an unfinished prefix, missing observations, a double-debit source
contrast, replay and action shrinking. Shrinking launches fresh HTTP/SQLite runs
and reduces three actions to one while retaining the same finish mismatch.
Generated evidence stays in logs/ and is not committed.

This module deliberately stays outside the workspace and pins the original
published core: it records what the public workflow supported at trial time.
It is not a new supported example track or a production test framework.
