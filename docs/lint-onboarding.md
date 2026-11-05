# Executable lint onboarding

Run `ci/lint-onboarding.sh` from a checkout with Go, Quint and Python 3 on `PATH`.
It builds the public CLI once and creates all broken inputs in a temporary
folder, removed on exit. Missing prerequisites fail explicitly.

The script records a misspelled bank action, checks its exact source location
and fix, then corrects it. It also repairs a wall-clock escape from simulation,
checks that a broken binding does not suppress the independent simulation scan,
and verifies a missing Quint executable produces operational evidence. A valid
span-conversion configuration exercises a separate input family. Invalid flag and
positional requests also demonstrate refusal before target inspection.

Use `gotogether lint -json -binding examples/bank/bank.binding.yaml -src DIR`
for an agent-facing report. The single current format is `gotogether.lint/1`.
Text and JSON render the same inspection: `checks` say what passed, failed or
was blocked; `findings` carry stable codes, severity, source locations where
available, analysis basis and suggested fixes. Missing coordinates mean the rule
has no precise location. A blocked check is not a successful check.

Exit 0 means the selected checks completed without error findings; exit 1 means
an authored problem was found; exit 2 means an input or required tool was
unavailable, or the request was invalid. `complete` distinguishes checks that
ran from checks blocked by another problem. Syntactic source inspection does not
establish Go type correctness, trace conformance or application behavior.

Put `-json` first when an agent also needs machine-readable request errors.
For example, `gotogether lint -json -sim DIR unexpected` exits 2 with one
`request.arguments` blocked operational check, an actionable finding and no
inspection evidence. Malformed flags use `request.flags`. Neither request reads
targets or invokes Quint. `-h` prints ordinary help and exits 0.

Flags retain Go's parsing rules: parsing stops at an error, the first positional
argument, or `--`. A later `-json` does not retroactively change output mode;
`-json=false` disables it, and a string flag may consume `-json` as its value.
