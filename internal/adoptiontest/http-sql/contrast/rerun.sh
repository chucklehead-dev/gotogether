#!/usr/bin/env bash
set -eu
export ONBOARDING_ACTIONS="$(cat "$1")"
export ONBOARDING_STEPS="$2"
GOWORK=off GOFLAGS="-mod=mod -overlay=$ONBOARDING_OVERLAY" go test -count=1 -run '^TestShrinkRecord$' .
