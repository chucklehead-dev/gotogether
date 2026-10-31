# Goo strict Read adaptation rehearsal

A bounded, agent-assisted adaptation of the published library starter to
`github.com/casselc/goo/subjectfacts.Read`. The frozen driver calls the actual
pinned library and observes returned definitions and errors for 16 authored YAML
inputs. Expected input rejection is a domain observation. Setup, missing
observations and tool failures are separate from source-defect evidence.

This is instruction-isolated modeling against a real library contract. It is not
independent-maintainer adoption, general parser verification, or discovery of a
production bug. The specific parent-planted source contrast was supplied after
`FREEZE.sha256` captured the model, binding, driver, fixtures and module files.
The starter's toy snapshot model and aliasing mutant were replaced, not credited
as Goo evidence. The prior Goo lifecycle/aggregate trial is separate.

## Reproduce

Go with automatic Go 1.27 toolchain selection, Python3 and Quint are required.
Use a current CLI that emits structured graph scope; the frozen runtime SDK
remains at its original published pin. From this checkout:

```sh
mkdir -p "$PWD/bin"
go build -o "$PWD/bin/gotogether" ./cmd/gotogether
PATH="$PWD/bin:$PATH" internal/adoptiontest/goo-read/reproduce.sh
```

The reporting CLI is also available at an exact published revision:

```sh
GOBIN="$PWD/bin" GOWORK=off go install github.com/chucklehead-dev/gotogether/cmd/gotogether@7c581c4fe7ed
PATH="$PWD/bin:$PATH" internal/adoptiontest/goo-read/reproduce.sh
```

This CLI revision supplies structured report fields and native evidence bundling.
The frozen runtime SDK pin records what was available when the model and driver
were authored.

Both runtime modules have exact published pins and the baseline has no local
replacement. The script verifies the frozen hashes, works in a disposable module
copy, and leaves fresh evidence under `logs/run-*`. It checks:

- Every actual Read call conforms, and all 16 bounded cases are reachable in the
  complete 17-state/272-transition model graph.
- Disabling only `KnownFields` causes unknown-top-level-field input case 2 to be
  accepted without error. The frozen model rejects it, producing exactly a
  post-read observed-state mismatch (exit 1), not an invariant/tool failure.
- Removing an actual observation produces an evidence error (exit 2), which
  cannot count as catching the contrast.
- Faithful and contrast manifests replay after the original project path is
  renamed away, from a different working directory, using bundled evidence.

The contrast is a parent-provided injected contract violation. Go 1.27 refuses
source overlays beneath `GOMODCACHE`, so the script copies the exact production
package outside the cache and selects that copy only through an alternate
contrast modfile. The baseline module, Goo checkout, module cache, frozen model
and observing driver remain unchanged. The original/contrast source hashes and
one-line change are recorded in `contrast/PROVENANCE.json`.

The model covers two accepted definition shapes and fourteen rejection fixtures,
not arbitrary YAML or all Goo behavior. A replay judges captured history; it does
not run the library again. See [FRICTION.md](FRICTION.md) for the measured setup,
authoring and mutation-setup friction. No missing runtime adapter was established
within this bounded exercise.
