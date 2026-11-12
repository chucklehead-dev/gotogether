#!/usr/bin/env bash
# What a nightly run takes (.github/workflows/nightly.yml's select job):
# every job and every row on a schedule or an unfiltered dispatch; a
# dispatch's filters only remove jobs and rows, never change a row's
# limits. A filter naming nothing fails, so a typo cannot run nothing and
# show green. internal/ciconfig runs this script against the workflow.
#   ci/nightly-select.sh ids        the nightly's job ids, one per line
#   ci/nightly-select.sh plan       jobs= and mutants= lines for $GITHUB_OUTPUT,
#                                   from EVENT, JOBS, MUTANTS, BINDINGS and
#                                   ALONE_GROUPS (verify-alone's groups, which
#                                   ci/verify-roster.sh alone narrows)
#   ci/nightly-select.sh gates      "package max shards" for each selected row
#                                   split into shards (core-mutants-gate's
#                                   aggregates), from EVENT and MUTANTS
#   ci/nightly-select.sh bindings   the bindings on stdin (one per line) that
#                                   BINDINGS names (each term a substring of
#                                   a path); every one when BINDINGS is empty
# The filters are comma- or space-separated lists. (Not GROUPS: bash
# keeps that name for the user's group ids and ignores an assignment.)
set -euo pipefail
# Membership tests read all of their input (grep >/dev/null, never grep -q):
# under pipefail, grep -q's early exit can kill the writer with SIGPIPE and
# fail the pipeline though the line matched, which dropped a job or refused
# a valid filter now and then.
# terms are split on white space, never expanded as globs
set -f
cd "$(dirname "$0")/.."

# every job but select itself, in the workflow's order
ids=(grist-full fresh-seeds core-properties volume core-mutants core-mutants-gate fuzz qeval-differential verify verify-alone-plan verify-alone evaluator-shadow webjobs-live raftkv-real benchmarks)
# a job selected takes the jobs it needs with it
implied() {
  case "$1" in
  verify-alone) echo verify-alone-plan ;;
  # a split package's verdict is its gate's, which needs its shards
  core-mutants) echo core-mutants-gate ;;
  core-mutants-gate) echo core-mutants ;;
  esac
}

# a filter's terms, one per line: commas and white space separate them
terms() {
  local t
  for t in ${1//,/ }; do printf '%s\n' "$t"; done
}

# a JSON list of strings (job ids and package names need no escaping)
json_list() {
  local sep="" out="["
  for x in "$@"; do out+="$sep\"$x\""; sep=","; done
  printf '%s]' "$out"
}

# ci/core-mutants.txt's rows, as "package max shards": a package, its max
# and, given as shards=N (N >= 2), the runners its campaign is split over
# (1 when not given). A malformed row fails rather than run something else.
mutant_rows() {
  local pkg max opt extra n
  while read -r pkg max opt extra; do
    if [ -n "$extra" ] || ! [[ "$max" =~ ^[0-9]+$ ]] || { [ -n "$opt" ] && ! [[ "$opt" =~ ^shards=([2-9]|[1-9][0-9]+)$ ]]; }; then
      echo "ci/core-mutants.txt: \"$pkg $max $opt $extra\" is not a package, its max and an optional shards=N (N >= 2)" >&2
      return 1
    fi
    n=1
    [ -z "$opt" ] || n=${opt#shards=}
    echo "$pkg $max $n"
  done < <(grep -v '^[[:space:]]*\(#\|$\)' ci/core-mutants.txt)
}

# the rows a run takes: every one unless MUTANTS names some (each must be a row)
selected_rows() {
  local all pkgs p
  all=$(mutant_rows) || exit 1
  mapfile -t pkgs < <(terms "${MUTANTS:-}")
  for p in "${pkgs[@]}"; do
    if ! awk '{ print $1 }' <<<"$all" | grep -xF -- "$p" >/dev/null; then
      echo "mutants filter: $p has no core-mutants row (ci/core-mutants.txt: $(awk '{ print $1 }' <<<"$all" | xargs))" >&2
      exit 1
    fi
  done
  while read -r pkg max shards; do
    if [ "${#pkgs[@]}" -gt 0 ] && ! printf '%s\n' "${pkgs[@]}" | grep -xF -- "$pkg" >/dev/null; then
      continue
    fi
    echo "$pkg $max $shards"
  done <<<"$all"
}

filter_bindings() {
  local want
  mapfile -t want < <(terms "${BINDINGS:-}")
  if [ "${#want[@]}" -eq 0 ]; then
    cat
    return
  fi
  local all kept=() t b hit
  mapfile -t all
  for t in "${want[@]}"; do
    hit=0
    for b in "${all[@]}"; do
      [[ "$b" == *"$t"* ]] && hit=1
    done
    if [ "$hit" -eq 0 ]; then
      echo "bindings filter: $t names no binding of verify's roster (a term is a substring of a path, e.g. examples/hello/; verify-alone's bindings are chosen by group, with groups)" >&2
      exit 1
    fi
  done
  for b in "${all[@]}"; do
    for t in "${want[@]}"; do
      if [[ "$b" == *"$t"* ]]; then
        kept+=("$b")
        break
      fi
    done
  done
  printf '%s\n' "${kept[@]}"
}

case "${1:-}" in
ids)
  printf '%s\n' "${ids[@]}"
  ;;
plan)
  if [ "${EVENT:-}" != workflow_dispatch ]; then
    # a scheduled run has no inputs: it takes everything
    JOBS="" MUTANTS="" BINDINGS="" ALONE_GROUPS=""
  fi
  mapfile -t asked < <(terms "${JOBS:-}")
  selected=()
  if [ "${#asked[@]}" -eq 0 ]; then
    selected=("${ids[@]}")
  else
    for a in "${asked[@]}"; do
      if ! printf '%s\n' "${ids[@]}" | grep -xF -- "$a" >/dev/null; then
        echo "jobs filter: $a is not a nightly job (one of: ${ids[*]})" >&2
        exit 1
      fi
    done
    for a in "${asked[@]}"; do
      dep=$(implied "$a")
      [ -n "$dep" ] && asked+=("$dep")
    done
    for id in "${ids[@]}"; do
      printf '%s\n' "${asked[@]}" | grep -xF -- "$id" >/dev/null && selected+=("$id")
    done
  fi
  in_selected() { printf '%s\n' "${selected[@]}" | grep -xF -- "$1" >/dev/null; }

  mapfile -t pkgs < <(terms "${MUTANTS:-}")
  if [ "${#pkgs[@]}" -gt 0 ] && ! in_selected core-mutants; then
    echo "mutants filter given, but core-mutants is not among the jobs selected" >&2
    exit 1
  fi
  chosen=$(selected_rows) || exit 1
  # a row split into shards is a matrix entry per shard, each with the
  # row's max (applied only by core-mutants-gate, to the shards' sum) and
  # an artifact name the gate downloads by
  rows="" sep=""
  while read -r pkg max shards; do
    [ -n "$pkg" ] || continue
    slug=${pkg//\//-}
    for ((i = 0; i < shards; i++)); do
      rows+="$sep{\"pkg\":\"$pkg\",\"max\":$max,\"shard\":$i,\"shards\":$shards,\"artifact\":\"mutations-$slug-shard-$i-of-$shards\"}"
      sep=","
    done
  done <<<"$chosen"

  if [ -n "$(terms "${BINDINGS:-}")" ]; then
    if ! in_selected verify; then
      echo "bindings filter given, but verify is not among the jobs selected" >&2
      exit 1
    fi
    # fail here, in seconds, not after verify's setup; verify's roster is
    # the shared one (verify-alone's groups have their own filter)
    ci/verify-roster.sh shared | filter_bindings >/dev/null
  fi
  if [ -n "$(terms "${ALONE_GROUPS:-}")" ]; then
    if ! in_selected verify-alone; then
      echo "groups filter given, but verify-alone is not among the jobs selected" >&2
      exit 1
    fi
    ALONE_GROUPS="$ALONE_GROUPS" ci/verify-roster.sh alone >/dev/null
  fi

  echo "jobs=$(json_list "${selected[@]}")"
  echo "mutants={\"include\":[$rows]}"
  ;;
gates)
  if [ "${EVENT:-}" != workflow_dispatch ]; then
    MUTANTS=""
  fi
  chosen=$(selected_rows) || exit 1
  while read -r pkg max shards; do
    [ -n "$pkg" ] && [ "$shards" -gt 1 ] && echo "$pkg $max $shards"
  done <<<"$chosen"
  true
  ;;
bindings)
  filter_bindings
  ;;
*)
  echo "usage: ci/nightly-select.sh ids|plan|gates|bindings" >&2
  exit 2
  ;;
esac
