#!/usr/bin/env bash
# Installs the requested npm CLI and its admitted Rust evaluator pair. Releases
# are fetched directly, avoiding GitHub's shared unauthenticated API quota.
#
#   QUINT_VERSION=0.33.1 ci/install-quint.sh
set -euo pipefail

: "${QUINT_VERSION:?}"
npm_config_fetch_retries=2 npm_config_fetch_timeout=60000 npm install -g "@informalsystems/quint@${QUINT_VERSION}"
cli_version="$(quint --version)"
if [[ "$cli_version" != "${QUINT_VERSION#v}" ]]; then
  echo "install-quint: PATH selects CLI $cli_version, wanted $QUINT_VERSION; fix the npm binary PATH" >&2
  exit 1
fi
printf 'quint CLI %s\n' "$cli_version"

bm="$(npm root -g)/@informalsystems/quint/dist/src/rust/binaryManager.js"
ev="$(node -p 'require(process.argv[1]).QUINT_EVALUATOR_VERSION' "$bm")"
if [[ "$ev" != v0.7.1 ]]; then
  echo "install-quint: evaluator $ev is not admitted; update the paired evaluator checksums before installing it" >&2
  exit 1
fi
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) target=x86_64-unknown-linux-gnu ;;
  Linux-aarch64) target=aarch64-unknown-linux-gnu ;;
  Darwin-arm64) target=aarch64-apple-darwin ;;
  Darwin-x86_64) target=x86_64-apple-darwin ;;
  *) echo "install-quint: no evaluator asset for $(uname -sm)" >&2; exit 1 ;;
esac
asset="quint_evaluator-${target}.tar.gz"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
pins="$script_dir/quint-evaluator.sha256"

# The evaluator has no --version flag. Verify its bytes, not an executable bit
# or a directory name, against the admitted release before checking startup.
if command -v sha256sum >/dev/null 2>&1; then
  digest() { sha256sum < "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  digest() { shasum -a 256 < "$1" | awk '{print $1}'; }
else
  echo "install-quint: install sha256sum or shasum to verify evaluator integrity" >&2
  exit 1
fi
verify() {
  local file=$1 name=$2 expected actual
  expected="$(awk -v name="$name" '$2 == name {print $1}' "$pins")"
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || { echo "install-quint: missing or duplicate checksum for $name" >&2; return 1; }
  actual="$(digest "$file")" || return 1
  [[ "$actual" == "$expected" ]] || { echo "install-quint: checksum mismatch for $name" >&2; return 1; }
}

dir="${QUINT_HOME:-$HOME/.quint}/rust-evaluator-${ev}"
binary="$dir/quint_evaluator"
if [[ -f "$binary" && -x "$binary" ]] && verify "$binary" "quint_evaluator-${target}" && "$binary" --help >/dev/null; then
  printf 'quint evaluator %s: %s (verified cache)\n' "$ev" "$binary"
  exit 0
fi
if [[ -d "$binary" ]]; then
  echo "install-quint: $binary is a directory; remove the invalid cache entry" >&2
  exit 1
fi
mkdir -p "$dir"
# Staging beside the final executable makes publication a same-filesystem
# rename. Failed downloads/extraction/startup never change an existing binary.
stage="$(mktemp -d "$dir/.install.XXXXXX")"
trap 'rm -rf "$stage"' EXIT
url="https://github.com/quint-co/quint/releases/download/evaluator/${ev}/${asset}"
curl -fsSL --connect-timeout 15 --max-time 60 --retry 2 --retry-max-time 180 \
  -o "$stage/$asset" "$url"
verify "$stage/$asset" "$asset"
tar -xzf "$stage/$asset" -C "$stage" quint_evaluator
chmod +x "$stage/quint_evaluator"
verify "$stage/quint_evaluator" "quint_evaluator-${target}"
"$stage/quint_evaluator" --help >/dev/null
mv -f "$stage/quint_evaluator" "$binary"
printf 'quint evaluator %s: %s (verified release)\n' "$ev" "$binary"
