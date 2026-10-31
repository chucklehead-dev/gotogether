# Adoption friction and limits

This is an agent-assisted adaptation of the published synchronous-library starter,
not an independent maintainer adoption or a production bug discovery.

## Public workflow and setup

- `gotogether init -kind library -dir /workspace/goo-read-adaptation reader`
  wrote the starter and pinned published SDK version
  `v0.0.0-20261031015649-0ff3fcc40b19`. Its dependency step initially failed
  `Go: Unknown option: get`: I mistakenly treated `/tmp/gtg-path` as a PATH
  directory. It is a shell script; `source /tmp/gtg-path` resolved this.
  This was operator setup error, not a CLI capability gap.
- Public `go get` resolution was silent for roughly two minutes, then downloaded
  Goo and automatically installed Go 1.27.3 because Goo requires Go 1.27.0.
  No local replacement or copied production source was needed.
- Generated toy store/snapshot code, contract, observations and aliasSnapshot
  mutant were removed. Model, binding and driver were authored explicitly for
  `subjectfacts.Read(io.Reader)` from allowed production contract files and
  public toolkit workflow documentation.
- Expected invalid-input errors are recorded as `obs.hasError`; the recorder's
  error argument is nil. A rejection is an observed domain outcome. Harness,
  fixture decoding, SDK and CLI errors remain failures, never passing rejections.

## Model-authoring mistakes corrected before freeze

- An initial no-measures fixture accidentally produced malformed YAML instead
  of a semantically invalid empty measures map. Corrected it to `measures: {}`
  before any faithful tests or freeze.
- Initially planned a cumulative seen-case set. Removed that unnecessary state
  before simulation: last-case state suffices for this synchronous stateless API,
  giving a complete 17-state graph rather than an irrelevant powerset.

## Missing capabilities and bounded claims

No missing toolkit capability blocked this bounded Read adaptation. The model
checks 16 exact authored inputs, not all YAML, all Definition.Validate branches,
error wording, arbitrary I/O errors, or Evaluate/Compile. Accepted observations
check selected returned fields and typed predicate values, not every byte or
possible extra field in a Definition. Safety properties describe the model's
shape; actual rejection/acceptance and returned-field obligations live in the
binding's observation checks. Helper Boolean vals are not enabled as invariants.
There is no artificial settle or liveness claim for a synchronous call.

The freeze manifest is `FREEZE.sha256`; frozen files must not change without a
recorded unfreeze. Evidence and packaging may be extended after freeze.

## Post-freeze parent-supplied source contrast

After the freeze, the parent prepared a single-line contrast disabling YAML
KnownFields checking (`KnownFields(true)` to `KnownFields(false)`) in Goo's Read
implementation, with provenance under `contrast/`. Go 1.27 rejected the parent's
first attempt to overlay the dependency directly in the module cache. That
execution is a standard Go mutation-setup limitation, not a model catch, target
bug, or toolkit defect. The parent is preparing a supported temporary dependency
copy and alternate modfile; the frozen project module and contract remain intact.

The parent then ran the supported contrast successfully using:

```sh
. /tmp/gtg-path
GOWORK=off GOTOGETHER_KEEP=/tmp/goo-read-parent-contrast \
  go test -modfile=contrast/contrast.mod -overlay contrast/overlay.json \
  -run '^TestConforms$' -count=1 -v ./...
```

Measured result: Go test exit 1, conformance FAIL with `mismatch after read`.
At the third record, case 2 (`unknown_top`), the actual modified Read API returned
accepted=true, hasError=false and nilDefinition=false; the frozen contract requires
rejection. The other two safety properties were correctly reported as not checked
after conformance failed. This is an observation mismatch, not a tool failure.
The parent-run log and native steps are preserved as
`evidence/parent-contrast-test.txt` and `evidence/parent-contrast-steps.jsonl`.

The contrast alternate modfile temporarily redirects Goo to an exact production
package copy outside the module cache; the overlay changes only KnownFields.
That replacement is contrast-only and is not in the frozen baseline go.mod.
`contrast/PROVENANCE.json` records source commit and original/contrast hashes.
It is a parent-planted source contrast supplied after freeze, not a newly found
production bug or independently authored contrast. The log was inspected; the
same contrast command was not redundantly rerun by the adapting agent.

## Repository packaging

The parent copied the frozen contract byte-for-byte into
`internal/adoptiontest/goo-read`. Original transient logs and absolute-path
manifests referenced above are historical evidence, not packaged inputs.
`reproduce.sh` creates fresh portable evidence in a disposable module copy and
checks both semantic verdicts again after hiding every original project path.
The packaged baseline retains exact published dependencies and no replacements;
only the temporary contrast modfile selects a production source copy.
