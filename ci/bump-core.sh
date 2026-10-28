#!/usr/bin/env bash
# Moves every published module to one core commit (ROADMAP I14): each
# requires the core, and the published modules it requires (qgosim's
# qweave), at that commit, then tidies, on its own go.mod (GOWORK=off),
# as a user's go get would resolve it. internal/ciconfig's
# TestCorePinsAgree fails while their pins differ.
#
#   ci/bump-core.sh            # HEAD, which must be pushed
#   ci/bump-core.sh 3b20911    # a pushed commit
#
# The commit must be on GitHub: the go command fetches it from there
# (GOPROXY=direct skips the proxy's delay for a commit just pushed).
set -euo pipefail
cd "$(dirname "$0")/.."
commit="${1:-$(git rev-parse HEAD)}"
core=github.com/chucklehead-dev/gotogether
# the published modules, as AGENTS.md lists them (TestCorePinsAgree checks
# this list against the repository's go.mod files)
modules=(qhegel qweave qanalysis qgosim grist qnav qenv/simgrpc qenv/kubesim qenv/simlog/kafkalog qenv/simlog/kinesislog)
export GOWORK=off GOFLAGS=-mod=mod
for m in "${modules[@]}"; do
  reqs=()
  # the core and every published module it requires, at the commit
  while read -r path; do
    if [ "$path" = "$core" ] || printf '%s\n' "${modules[@]}" | sed "s|^|$core/|" | grep -qx "$path"; then
      reqs+=("$path@$commit")
    fi
  done < <(cd "$m" && go mod edit -json | sed -n 's|^.*"Path": "\('"$core"'[^"]*\)".*$|\1|p' | grep -vx "$core/$m" || true)
  [ "${#reqs[@]}" -eq 0 ] && continue # qweave requires no core
  echo "$m: ${reqs[*]}"
  (cd "$m" && go get "${reqs[@]}" && go mod tidy)
done
echo "pins:"
grep -H "$core v" "${modules[@]/%//go.mod}" || true
