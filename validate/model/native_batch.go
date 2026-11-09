package model

import (
	"context"
	"fmt"
	"strings"
)

// proveNativeHolds shares only the bounded proof of a conjunction. Membership is
// confined to one prepared model invocation and its already capped depth. Other
// semantic classes never join the group. A counterexample names no individual
// refutation: every original member must then take the scalar verification path.
func proveNativeHolds(ctx context.Context, quint, dir, file string, invariants []string, holds map[string]bool, steps int) (map[string]bool, error) {
	if steps <= 0 {
		return nil, nil
	}
	var members []string
	seen := map[string]bool{}
	for _, name := range invariants {
		if holds[name] && !seen[name] {
			members = append(members, name)
			seen[name] = true
		}
	}
	if len(members) < 2 {
		return nil, nil
	}
	done := progressOf(ctx).phase("invariant batch " + strings.Join(members, ", "))
	result, err := verifyNativeGroup(ctx, quint, dir, file, members, steps)
	done(phaseStatus(ctx, err))
	if err != nil {
		return nil, fmt.Errorf("simulate: native holds batch: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if result.violated {
		if progress := progressOf(ctx); progress != nil && progress.w != nil {
			fmt.Fprintf(progress.w, "simulate %s: invariant batch %s has a counterexample; checking all members individually\n", progress.binding, strings.Join(members, ", "))
		}
		return nil, nil
	}
	proved := make(map[string]bool, len(members))
	for _, name := range members {
		proved[name] = true
	}
	return proved, nil
}
