#!/usr/bin/env bash
# Fetches quint's repository at QUINT_VERSION's tag: Q3's corpus (examples/,
# quint/testFixture, quint/test's inline programs, quint/integration-tests),
# read by internal/qoracle. Fetched, never vendored (docs/q2-front-end.md,
# decision 3): the tag's commit is checked against ci/quint-src.commit, and
# each file the corpus reads against its sha256 in the manifest
# (internal/qoracle/testdata/corpus.json).
#
#   QUINT_VERSION=0.35.0 ci/fetch-quint-src.sh DIR
#
# DIR must not exist. QUINT_SRC_CLONE=path checks a local clone instead of
# fetching (development): its HEAD must be the pinned commit and its tree
# clean, and DIR becomes a symbolic link to it. Prints QUINT_SRC=DIR.
set -euo pipefail

: "${QUINT_VERSION:?}"
dir=${1:?usage: QUINT_VERSION=x.y.z ci/fetch-quint-src.sh DIR}
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
pins="$script_dir/quint-src.commit"
version=${QUINT_VERSION#v}
expected="$(awk -v v="$version" '$1 == v {print $2}' "$pins")"
if ! [[ "$expected" =~ ^[0-9a-f]{40}$ ]]; then
  echo "fetch-quint-src: no commit pinned for quint $version in $pins: add the tag's commit (git ls-remote https://github.com/informalsystems/quint 'refs/tags/v$version^{}')" >&2
  exit 1
fi
if [ -e "$dir" ]; then
  echo "fetch-quint-src: $dir exists; give a new directory" >&2
  exit 1
fi

if [ -n "${QUINT_SRC_CLONE:-}" ]; then
  actual="$(git -C "$QUINT_SRC_CLONE" rev-parse HEAD)"
  if [ -n "$(git -C "$QUINT_SRC_CLONE" status --porcelain)" ]; then
    echo "fetch-quint-src: $QUINT_SRC_CLONE has local changes; the corpus is the tag's files" >&2
    exit 1
  fi
  src="$(cd "$QUINT_SRC_CLONE" && pwd)"
  ln -s "$src" "$dir"
else
  # only the corpus's directories, at the tag
  git -c advice.detachedHead=false clone --quiet --depth 1 --filter=blob:none --sparse --branch "v$version" \
    https://github.com/informalsystems/quint "$dir"
  git -C "$dir" sparse-checkout set examples quint/testFixture quint/test quint/integration-tests
  actual="$(git -C "$dir" rev-parse HEAD)"
fi
if [ "$actual" != "$expected" ]; then
  echo "fetch-quint-src: v$version is $actual, pinned $expected: the tag moved, or the pin is wrong" >&2
  exit 1
fi
echo "QUINT_SRC=$(cd "$dir" && pwd)"
