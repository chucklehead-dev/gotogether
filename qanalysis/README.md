# qanalysis

gotogether's checks as `golang.org/x/tools/go/analysis` passes, and
the static action map. Its own module: the core depends only on OTel,
yaml.v3 and Hegel.

| Analyzer | Reports | Same as |
|---|---|---|
| `quintsim` | in files importing qenv, and a grist application's non-test files (importing grist; each fix grist's: an event's `Meta.Time`, a command's arguments, a processor): `go` statements, the wall clock and math/rand's functions, called or as values (`now := time.Now`; by type: dot imports too), steps recorded with `context.Background()`; with a context in scope, fixes: `go f()` to `qenv.Go`, `time.Now()`/`time.Sleep` to qenv's | `gotogether lint -sim` |
| `quintweave` | unmarked `go` statements, sync method values, embedded sync types (for packages woven by qenv/weave); fixes: `//qenv:go` above a function literal's `go`, a method value called from a function literal | `gotogether lint -weave` |
| `quintctx` | `context.Background()` or `TODO()` in a function with a `context.Context` parameter or a test's `t` (fix: pass it, or `t.Context()`); not in closures, not a nil parameter's default; `//quintctx:ok` exempts a line | |
| `quintraw` | a grist effect (the function given to `grist.Effect`, its closures, a package function named there) calling `net/http`'s client, a gRPC `ClientConn` or `database/sql` directly, past the declared calls grist records (fix: `grist.NewCall`, `Call.Do`); `//grist:raw <reason>` on the line or the one above exempts it | |
| `quintaction` | `//quint:action` without `action:`; expressions using locals (only parameters, the receiver and named results are in scope on return); with `-binding`: actions not in the model, fields the binding uses but the annotation does not record, literal `qobs.Record` actions not in the model; fixes: a local every return returns in one place named as that result, a misspelled parameter spelled, a misspelled action (directive or `Record`) named as the model's nearest, a missing field recorded from the parameter or named result of its name | `gotogether lint -binding -src` |

The first two wrap package `qlint`, so the CLI and the analyzers find the
same things.

```sh
go install github.com/chucklehead-dev/gotogether/qanalysis/cmd/quintvet@latest
quintvet ./...                                           # all five
quintvet -fix ./...                                      # and apply their fixes
quintvet -quintweave=false -quintaction.binding app.binding.yaml ./...
go vet -vettool=$(which quintvet) -quintweave=false ./... # per package, with go vet
```

**golangci-lint**: a module plugin named `gotogether` (package `golangci`;
settings `analyzers`, `binding`, `quint`). See the package doc for
`.custom-gcl.yml` and `.golangci.yml`.

**gopls** loads only the analyzers compiled into it: use the binary, go vet
or golangci-lint (which editors run).

## Static action map (`actionmap`, `cmd/actionmap`)

For each model action, the code statically reachable from where its steps
are recorded (a `//quint:action` function, or one calling `qobs.Record`
with a constant action, closures included), through a VTA call graph that
stops at other packages' functions. With qcover's per-step coverage
(`-coverage summary.json`) it reports per action:

- **reachable, never run**: a path no run took;
- **reachable, run only under other actions**: shared code, or a path of
  this action the runs never took;
- **run by its steps, not reachable from a site**: an attribution gap,
  such as code in the site's callers (the loop that leads to the step) or
  setup that ran before a task's first step.

```sh
GOTOGETHER_KEEP=/tmp/k go run -C qweave ./cmd/qweave -C .. -cover \
  -coverpkg github.com/chucklehead-dev/gotogether/examples/checkout \
  go test -run '^TestCodeCoverage$' ./examples/checkout/
cd qanalysis && go run ./cmd/actionmap -C .. -coverage /tmp/k/checkout-coverage.json ./examples/checkout/...
```

Tests: `go test ./...` (analysistest fixtures in `testdata/src`, with
stubs of qenv, qobs, grist and gRPC; the binding test and the checkout comparison need
quint, and the latter skips under `-short`).
