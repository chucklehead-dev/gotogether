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
	var token nativeProofToken
	if request, _ := ctx.Value(nativeProofRequestKey{}).(*nativeProofRequest); request != nil {
		var hit bool
		var err error
		token, hit, err = request.prepare(ctx, quint, members, steps)
		if err != nil {
			return nil, err
		}
		if hit {
			if err := token.verify(ctx, quint); err != nil {
				return nil, err
			}
			proved := make(map[string]bool, len(members))
			for _, name := range members {
				proved[name] = true
			}
			if p := progressOf(ctx); p != nil && p.w != nil {
				fmt.Fprintf(p.w, "simulate %s: invariant batch reuses exact positive native proof from %s\n", p.binding, request.proof.ProducerBinding)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return proved, nil
		}
	}
	proofContext := ctx
	var backend *nativeBackendRequest
	if token.key != "" {
		var err error
		backend, err = token.request.session.backend(ctx)
		if canceled := ctx.Err(); canceled != nil {
			if backend != nil {
				backend.close()
			}
			return nil, canceled
		}
		if err != nil || backend == nil {
			if backend != nil {
				backend.close()
				backend = nil
			}
			token.key = ""
		} else {
			defer backend.close()
			proofContext = context.WithValue(ctx, nativeBackendKey{}, backend)
		}
	}
	done := progressOf(ctx).phase("invariant batch " + strings.Join(members, ", "))
	result, err := verifyNativeGroup(proofContext, quint, dir, file, members, steps)
	done(phaseStatus(ctx, err))
	if err != nil {
		return nil, fmt.Errorf("simulate: native holds batch: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Backend/accounting failures disable reuse; they do not turn a coherent
	// ordinary result into an error or manufacture a toolchain provenance claim.
	if backend != nil && !backend.owned {
		token.key = ""
	}
	if err := token.verify(ctx, quint); err != nil {
		return nil, err
	}
	if result.violated {
		if progress := progressOf(ctx); progress != nil && progress.w != nil {
			fmt.Fprintf(progress.w, "simulate %s: invariant batch %s has a counterexample; checking all members individually\n", progress.binding, strings.Join(members, ", "))
		}
		return nil, nil
	}
	token.publish(members, steps)
	proved := make(map[string]bool, len(members))
	for _, name := range members {
		proved[name] = true
	}
	return proved, nil
}
