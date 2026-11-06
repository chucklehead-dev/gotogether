#!/usr/bin/env bash
# Linux CI bootstrap for the navigator's native tape tools. Reuse working
# system tools and the pinned VHS module; install only what is absent.
# TAPE_TOOLS_TIMEOUT_SECONDS is the total command budget (default 300, 1..600).
# Its remaining time bounds probes, downloads and builds, with 10s to kill them.
set -euo pipefail

fail() { printf 'install-tape-tools: %s\n' "$*" >&2; exit 1; }
[[ "$(uname -s)" == Linux ]] || fail 'this bootstrap requires Linux; install the tape tools with your system packages'
command -v timeout >/dev/null || fail 'GNU timeout is required to bound native tool installation'
budget=${TAPE_TOOLS_TIMEOUT_SECONDS:-300}
[[ "$budget" =~ ^[1-9][0-9]{0,2}$ ]] && ((10#$budget <= 600)) || fail 'TAPE_TOOLS_TIMEOUT_SECONDS must be an integer from 1 to 600'
deadline=$((SECONDS + 10#$budget))
vhs_version=v0.12.1

remaining() {
  left=$((deadline - SECONDS))
  if ((left <= 0)); then
    printf 'install-tape-tools: command budget exhausted before %s\n' "$phase" >&2
    exit 124
  fi
}

bounded() {
  local phase=$1 left status
  shift
  remaining
  printf 'install-tape-tools: %s (at most %ss remaining)\n' "$phase" "$left" >&2
  if timeout --kill-after=10s "${left}s" "$@"; then return 0; else status=$?; fi
  printf 'install-tape-tools: %s failed (exit %s); check tool availability or retry acquisition\n' "$phase" "$status" >&2
  return "$status"
}

apt() {
  local phase="apt-get $1" left status
  remaining
  printf 'install-tape-tools: %s (at most %ss remaining)\n' "$phase" "$left" >&2
  # The timeout must have privilege to stop apt and its children, not just sudo.
  if sudo -n timeout --kill-after=10s "${left}s" env DEBIAN_FRONTEND=noninteractive \
    apt-get -o DPkg::Lock::Timeout=30 -o Acquire::http::Timeout=30 \
    -o Acquire::https::Timeout=30 -o Acquire::Retries=2 "$@"; then return 0; else status=$?; fi
  printf 'install-tape-tools: %s failed (exit %s); check the package mirror and retry acquisition\n' "$phase" "$status" >&2
  return "$status"
}

ttyd_version() {
  local text
  text=$(bounded 'check ttyd' ttyd --version) || return
  if [[ "$text" =~ ^ttyd[[:space:]]version[[:space:]]([0-9]+)\.([0-9]+)(\.[0-9]+)?([-+][^[:space:]]+)?($|[[:space:]]) ]] &&
    ((10#${BASH_REMATCH[1]} > 1 || (10#${BASH_REMATCH[1]} == 1 && 10#${BASH_REMATCH[2]} >= 7))); then
    printf '%s\n' "$text"
  else
    fail "ttyd must be at least 1.7; install a current ttyd (found: $text)"
  fi
}

ffmpeg_version() {
  local text
  text=$(bounded 'check ffmpeg' ffmpeg -version) || return
  [[ "$text" == 'ffmpeg version '* ]] || fail 'ffmpeg did not report its version; install a working ffmpeg'
  printf '%s\n' "${text%%$'\n'*}"
}

missing=()
if command -v ttyd >/dev/null; then
  printf 'install-tape-tools: reuse ttyd at %s\n' "$(command -v ttyd)"
  ttyd_version
else missing+=(ttyd); fi
if command -v ffmpeg >/dev/null; then
  printf 'install-tape-tools: reuse ffmpeg at %s\n' "$(command -v ffmpeg)"
  ffmpeg_version
else missing+=(ffmpeg); fi
if ((${#missing[@]})); then
  command -v sudo >/dev/null || fail 'sudo is required to install missing system packages'
  apt update
  apt install -y --no-install-recommends "${missing[@]}"
fi
# Installation success is not enough: absent/broken tools must not become tape skips.
command -v ttyd >/dev/null || fail 'ttyd is still missing after package installation'
ttyd_version
command -v ffmpeg >/dev/null || fail 'ffmpeg is still missing after package installation'
ffmpeg_version

pinned_vhs() {
  local info kind module version rest
  info=$(bounded 'check VHS module identity' go version -m "$1") || return
  while read -r kind module version rest; do
    if [[ "$kind" == mod && "$module" == github.com/charmbracelet/vhs && "$version" == "$vhs_version" ]]; then return 0; fi
  done <<< "$info"
  return 1
}

if command -v vhs >/dev/null && pinned_vhs "$(command -v vhs)"; then
  vhs_path=$(command -v vhs)
else
  gopath=$(bounded 'locate Go tool directory' go env GOPATH)
  [[ -n "$gopath" && "$gopath" != *:* ]] || fail 'go env GOPATH must name one tool directory'
  tool_bin="$gopath/bin"
  mkdir -p "$tool_bin"
  bounded "install VHS $vhs_version" env GOBIN="$tool_bin" GOTOOLCHAIN=local go install "github.com/charmbracelet/vhs@$vhs_version"
  vhs_path="$tool_bin/vhs"
  [[ -x "$vhs_path" ]] || fail 'Go installation did not produce an executable vhs'
  pinned_vhs "$vhs_path" || fail "installed VHS does not identify module $vhs_version"
fi
bounded 'verify VHS' "$vhs_path" --version
printf 'install-tape-tools: ready: VHS %s at %s\n' "$vhs_version" "$vhs_path"
if [[ -n "${GITHUB_PATH:-}" ]]; then printf '%s\n' "${vhs_path%/*}" >> "$GITHUB_PATH"; fi
