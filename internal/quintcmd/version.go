package quintcmd

import (
	"context"
	"strings"

	"golang.org/x/mod/semver"
)

// MinimumVersion is the oldest stable CLI whose generated traces are admitted.
// This version floor does not verify the Rust evaluator's executable bytes.
const MinimumVersion = "0.33.1"

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
// the same cancellation and process admission as other managed commands.
func Version(ctx context.Context, binary, dir string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	out, err := Command(ctx, binary, dir, "--version").Output()
	if ctx.Err() != nil {
		return strings.TrimSpace(string(out)), ctx.Err()
	}
	return strings.TrimSpace(string(out)), err
}

// Require admits the selected runtime before callers create or remove evidence
// files. Every workload command checks again after its final configuration.
func Require(ctx context.Context, binary, dir string) error {
	return Command(ctx, binary, dir).checkVersion()
}
