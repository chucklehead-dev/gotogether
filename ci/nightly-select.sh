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
#   ci/nightly-select.sh bindings   the bindings on stdin (one per line) that
#                                   BINDINGS names (each term a substring of
#                                   a path); every one when BINDINGS is empty
# The filters are comma- or space-separated lists. (Not GROUPS: bash
# keeps that name for the user's group ids and ignores an assignment.)
set -euo pipefail
# terms are split on white space, never expanded as globs
set -f
cd "$(dirname "$0")/.."

# every job but select itself, in the workflow's order
ids=(grist-full fresh-seeds core-properties volume core-mutants fuzz qeval-differential verify verify-alone-plan verify-alone evaluator-shadow webjobs-live raftkv-real benchmarks)
# a job selected takes the jobs it needs with it
implied() {
  case "$1" in
  verify-alone) echo verify-alone-plan ;;
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

# ci/core-mutants.txt's rows: a package and its max
mutant_rows() { grep -v '^[[:space:]]*\(#\|$\)' ci/core-mutants.txt; }

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
      if ! printf '%s\n' "${ids[@]}" | grep -qxF -- "$a"; then
        echo "jobs filter: $a is not a nightly job (one of: ${ids[*]})" >&2
        exit 1
      fi
    done
    for a in "${asked[@]}"; do
      dep=$(implied "$a")
      [ -n "$dep" ] && asked+=("$dep")
    done
    for id in "${ids[@]}"; do
      printf '%s\n' "${asked[@]}" | grep -qxF -- "$id" && selected+=("$id")
    done
  fi
  in_selected() { printf '%s\n' "${selected[@]}" | grep -qxF -- "$1"; }

  mapfile -t pkgs < <(terms "${MUTANTS:-}")
  if [ "${#pkgs[@]}" -gt 0 ] && ! in_selected core-mutants; then
    echo "mutants filter given, but core-mutants is not among the jobs selected" >&2
    exit 1
  fi
  for p in "${pkgs[@]}"; do
    if ! mutant_rows | awk '{ print $1 }' | grep -qxF -- "$p"; then
      echo "mutants filter: $p has no core-mutants row (ci/core-mutants.txt: $(mutant_rows | awk '{ print $1 }' | xargs))" >&2
      exit 1
    fi
  done
  rows="" sep=""
  while read -r pkg max; do
    if [ "${#pkgs[@]}" -gt 0 ] && ! printf '%s\n' "${pkgs[@]}" | grep -qxF -- "$pkg"; then
      continue
    fi
    rows+="$sep{\"pkg\":\"$pkg\",\"max\":$max}"
    sep=","
  done < <(mutant_rows)

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
bindings)
  filter_bindings
  ;;
*)
  echo "usage: ci/nightly-select.sh ids|plan|bindings" >&2
  exit 2
  ;;
esac
