#!/usr/bin/env bash
# Renders the tapes' GIFs and screenshots into media/ (the README's), and
# the limiter's in a light theme too. Needs vhs, ttyd, ffmpeg and quint on
# PATH (the module's README says how to install them); gotogether-nav is
# built from this checkout first.
set -euo pipefail
cd "$(dirname "$0")"
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
go build -o "$bin/gotogether-nav" ../cmd/gotogether-nav
export PATH="$bin:$PATH"
if [ "$(id -u)" = 0 ]; then export VHS_NO_SANDBOX=true; fi # the browser's sandbox refuses root
mkdir -p media
for tape in ${TAPES:-*.tape}; do
  echo "rendering $tape"
  vhs "$tape"
done
# the limiter in a light theme: its screenshot alone
sed -e 's/Catppuccin Mocha/Catppuccin Latte/' -e 's|^Output media/limiter.gif|Output "'"$bin"'/light.gif"|' \
  -e 's|^Screenshot media/limiter.png|Screenshot media/limiter-light.png|' limiter.tape > "$bin/light.tape"
vhs "$bin/light.tape"
ls -l media
