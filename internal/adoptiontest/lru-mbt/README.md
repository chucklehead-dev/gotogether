# Installed-module LRU model-based testing rehearsal

An agent-assisted consumer of the published gotogether `connect` SDK and
HashiCorp's published `simplelru` package. The faithful baseline has no Go
workspace or checkout replacement. This is a model-driven implementation trial,
not a recorded-history validation adapter.

The independent model was authored separately from the observing driver using
an agreed sequential LRU contract. The author did not read the implementation,
its native tests, this driver, or a source contrast. The driver applies model
picks to the actual API and compares actual observations after every action.
It contains no expected membership, recency, capacity or eviction classifier.

## Scope

Three string keys, three integer values, initial capacity two and resize targets
one through three. Actions are Add, Get, Peek, Remove and Resize. Public Keys
supplies oldest-to-newest order; Len supplies size independently; nonmutating
Peek calls supply snapshot values. Observations also retain method return
values and each operation's actual callback multiset, including duplicate
multiplicity, without requiring callback delivery order. Internal model capacity
is omitted from the StateDriver's observable projection.

Five forced model runs distinguish Get promotion from Peek nonpromotion, resize
eviction, replacement/removal and misses. Another 32 traces of at most 24 steps
use native Quint model-based generation with seed 17. A fresh driver and real
cache are initialized for every trace. Native ITF generation and comparison are
existing SDK behavior; no new adapter or output format is introduced.

The pinned upstream tests already cover ordinary LRU operations and recency.
This exercise adds an independent stateful model and external installed-module
workflow; it is not evidence that the library was previously untested,
independent-maintainer adoption, exhaustive verification or a production bug.
Concurrency, nonpositive capacities, Purge, callback ordering and other cache
variants remain outside this contract.

## Reproduce

Go 1.26.1 or newer, Quint 0.33.0 with its native Rust evaluator, Python 3 and
sha256sum are required. Run the frozen baseline from any directory:

```sh
internal/adoptiontest/lru-mbt/reproduce.sh
```

The script checks the freeze, copies the consumer to a fresh temporary directory,
checks module tidiness and the absence of replacements, installs the CLI's
embedded skills through the registered Go tool, then runs real model-based tests
and vet. `LRU_RUN_DIR=/path/to/fresh-directory` selects retained artifacts. The
consumer, installed guidance, native ITF traces and test output remain there.
Re-running tests invokes the library again; inspecting an ITF trace alone does
not re-execute it.

The baseline is frozen for reproducible evidence, not as a compatibility promise
for older SDKs. After the baseline, the [parent-planted contrast](contrast/README.md)
replays its captured Get trace against an isolated dependency copy. Omitting
Get promotion produces a typed state mismatch at step 3: model order `[b, a]`
versus observed `[a, b]`. The unchanged baseline still passes. No upstream
library defect is claimed; the genuine SDK issue found during setup was numeric
configuration silently falling back to defaults, now fixed separately.

Inside the consumer the ordinary public commands are:

```sh
go test -count=1 -v .
go tool gotogether skills install -dir ./guidance
```

The Go tool is registered in go.mod, so tidy preserves the CLI's dependencies.
See [FRICTION.md](FRICTION.md) for setup decisions and [model/README.md](model/README.md)
for the independent model contract. Dependency identity is in PROVENANCE.json.
The freeze must be established after faithful native execution succeeds and
before a parent-planted source contrast is prepared. Model, driver and baseline
module files must remain unchanged during that contrast.
