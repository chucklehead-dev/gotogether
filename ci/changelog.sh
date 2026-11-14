#!/usr/bin/env bash
# Assembles changes/*.md (changes/README.md) under CHANGELOG.md's
# "## Unreleased" heading, newest first by the commit that added each
# (a fragment not yet committed first of all, then by name). Without
# -write it prints the bullets it would insert; with -write it inserts
# them and deletes the fragments. CHANGES_DIR and CHANGELOG override the
# paths (the test runs it on copies).
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
dir=${CHANGES_DIR:-$root/changes}
log=${CHANGELOG:-$root/CHANGELOG.md}
write=0
case "${1:-}" in
  -write) write=1 ;;
  "") ;;
  *) echo "usage: ci/changelog.sh [-write]" >&2; exit 2 ;;
esac

frags=()
while IFS= read -r f; do frags+=("$f"); done < <(
  for f in "$dir"/*.md; do
    [ -e "$f" ] || continue
    [ "$(basename "$f")" = README.md ] && continue
    t=$(git -C "$dir" log -1 --diff-filter=A --format=%ct -- "$f" 2>/dev/null || true)
    printf '%s\t%s\n' "${t:-9999999999}" "$f"
  done | sort -t$'\t' -k1,1nr -k2,2 | cut -f2)

if [ ${#frags[@]} -eq 0 ]; then
  echo "no fragments in $dir" >&2
  exit 0
fi

entries=$(for f in "${frags[@]}"; do sed -e '$a\' "$f"; done)
if [ "$write" -eq 0 ]; then
  printf '%s\n' "$entries"
  exit 0
fi

grep -q '^## Unreleased$' "$log" || { echo "$log has no '## Unreleased' heading" >&2; exit 1; }
tmp=$(mktemp)
ENTRIES="$entries" awk '
  !done && /^## Unreleased$/ { print; print ""; print ENVIRON["ENTRIES"]; done = 1; skip = 1; next }
  skip && /^$/ { skip = 0; next }
  { skip = 0; print }
' "$log" > "$tmp"
mv "$tmp" "$log"
rm -f "${frags[@]}"
echo "moved ${#frags[@]} fragments into $log" >&2
