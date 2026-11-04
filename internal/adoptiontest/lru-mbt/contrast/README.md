# Parent-planted Get-promotion contrast

Prepare a fresh faithful run, then execute this contrast against its retained
native trace:

```sh
run_dir=$(mktemp -d)
LRU_RUN_DIR="$run_dir" internal/adoptiontest/lru-mbt/reproduce.sh
internal/adoptiontest/lru-mbt/contrast/reproduce.sh "$run_dir"
```

The model and observing driver were frozen before this code was added. The
probe first replays the captured Get-promotion trace against the faithful
published dependency. It then copies the whole dependency module, removes
exactly the Get hit's recency-promotion line, and uses an alternate modfile
only for the contrast. Add and the Peek-based observer remain unchanged.

The contrast must produce a typed `connect.StepError` of kind `state`, with
no comparison/type error, at Get step 3, and concrete recency-key differences.
A compiler, trace-read, driver or tool failure cannot pass the control. The
expectation test succeeds only when Replay rejects this planted change; its
JSON artifact records `state_mismatch`, not a passing cache judgment.

Source hashes, the removed line, captured-trace hash, native trace and typed
failure data remain in the run directory. The baseline module, frozen files and
affected module-cache source file are checked unchanged; no upstream checkout
is edited. This is an oracle
control, not a discovered upstream defect or an exhaustive verification claim.
