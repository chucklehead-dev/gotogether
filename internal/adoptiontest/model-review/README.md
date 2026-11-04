# A real model review: hello's completion contract

This rehearsal compares an actual historical model change, rather than a newly
planted model mutant. Commit `5ead75826986a5ad37403c094c55db3ba6eed963`
added hello's `done` action after mutation testing exposed a client that completed
only some requests. Its parent is the before instance. The four historical
binding/model files are copied byte for byte; [PROVENANCE.md](PROVENANCE.md)
records their origin and hashes.

From the repository root, with Go 1.26.1+, Quint 0.33.0 and its Rust evaluator,
Git, Python 3 and `sha256sum` on PATH:

```sh
internal/adoptiontest/model-review/reproduce.sh
```

The script builds the current checkout's CLI, then compares temporary copies of
the frozen inputs. It retains JSON, JUnit and SARIF reports and CLI build identity
in a fresh temporary directory. Set `MODEL_REVIEW_RUN_DIR` to a fresh directory
if you need a particular artifact destination. This uses the current local SDK;
it is not an installed-module baseline or independent-maintainer adoption claim.

Both original bindings use `REQUESTS = 1.to(3)`. On the explicit observation
projection `applied,count`, both graphs have eight states and both directional
inclusions hold. The before graph has 24 transitions; the after graph has 25.
The additional transition is the new `done` action, which preserves state.
Existing `replay` transitions already preserve that same completed state, so the
finite observed state-sequence languages are **equivalent**.

That result does not mean the completion contract is unchanged. `done` adds a
named action guarded by `applied == REQUESTS`; code must reach that action and
record completion. The original change also added occurrence claims to kill a
client that never finishes. State-sequence comparison does not compare action
identity or establish that an implementation reaches a named action. Retain the
completion/occurrence checks when reviewing this change.

The script checks this precise result and scope, including that there are no
claimed native witness confirmations. Equivalence here rests on the complete Go
graph/product analysis, not native exhaustive equivalence. It checks the frozen
inputs and their temporary copies again after comparison. No model, guard,
projection or historical binding is edited to obtain the result.
