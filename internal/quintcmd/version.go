package quintcmd

import (
	"context"
	"strings"

	"golang.org/x/mod/semver"
)

// MinimumVersion is the oldest stable CLI whose generated traces are admitted.
// This version floor does not verify the Rust evaluator's executable bytes.
const MinimumVersion = "0.33.1"

// PinnedVersion is the CLI that CI tests and ci/install-quint.sh installs with
// its paired Rust evaluator: install hints name it, and the stock-CLI identity
// memo (and validate/model's proof reuse) is reviewed for its files only.
const PinnedVersion = "0.35.0"

// Supported admits complete stable semantic versions at or above the floor.
// Prereleases require separate admission; valid build metadata is permitted.
func Supported(version string) bool {
	v := strings.TrimSpace(version)
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return semver.IsValid(v) && semver.Prerelease(v) == "" && strings.TrimSuffix(v, semver.Build(v)) == semver.Canonical(v) && semver.Compare(v, "v"+MinimumVersion) >= 0
}

// Version inspects a CLI without requiring it to be supported. The probe uses
// the same cancellation and process admission as other managed commands; its
// timeout (ProbeTimeoutEnv) starts once it holds a slot, so a wait behind
// running proofs is no timeout. A missing quint is NotFound's, one that did
// not answer a *ProbeTimeoutError: callers tell absence from a slow machine.
func Version(ctx context.Context, binary, dir string) (string, error) {
	c := Command(ctx, binary, dir, "--version")
	release, err := acquire(c.ctx)
	if err != nil {
		return "", err
	}
	defer release()
	out, err := c.probe()
	return strings.TrimSpace(string(out)), err
}

// Require admits the selected runtime before callers create or remove evidence
// files. Every workload command checks again after its final configuration.
func Require(ctx context.Context, binary, dir string) error {
	return Command(ctx, binary, dir).checkVersion()
}
