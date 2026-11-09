package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/chucklehead-dev/gotogether/validate"
)

type nativeProofSessionKey struct{}
type nativeProofRequestKey struct{}
type nativeToolResolver func(context.Context, string) (string, bool)

// One iterator owns this store. Only exact positive group proofs enter it;
// reports and caller-owned slices are never stored or returned through it.
type nativeProofSession struct {
	identity nativeToolResolver
	backend  nativeBackendFactory
	proofs   map[string]*validate.NativeProof // exact request digest -> producing proof
}

func newNativeProofSession(identity nativeToolResolver, backends ...nativeBackendFactory) *nativeProofSession {
	backend := newOwnedNativeBackend
	if len(backends) > 0 {
		backend = backends[0]
	}
	return &nativeProofSession{identity: identity, backend: backend, proofs: map[string]*validate.NativeProof{}}
}

type nativeProofRequest struct {
	session  *nativeProofSession
	guard    *validate.SourceGuard
	original *validate.SourceGuard
	base     string
	owned    map[string]bool
	binding  string
	proof    *validate.NativeProof
}

func captureNativeProofRequest(session *nativeProofSession, original *validate.SourceGuard, baseFile, observerFile, baseSource, observerSource, canonical, binding string) (*nativeProofRequest, error) {
	if err := original.Verify(); err != nil {
		return nil, err
	}
	guard, err := validate.CaptureModelSources(baseFile)
	if err != nil {
		return nil, err
	}
	files, err := guard.Files()
	if err != nil {
		return nil, err
	}
	expected := map[string]string{baseFile: baseSource}
	if observerFile != baseFile {
		expected[observerFile] = observerSource
	}
	owned := map[string]bool{}
	for _, file := range files {
		if source, ok := expected[file.Path]; ok {
			sum := sha256.Sum256([]byte(source))
			if file.SHA256 != hex.EncodeToString(sum[:]) {
				return nil, fmt.Errorf("simulate: owned native input changed during preparation")
			}
			owned[file.Path] = true
		}
	}
	if len(owned) != len(expected) {
		return nil, fmt.Errorf("simulate: owned native input absent from captured source inventory")
	}
	if err := original.Verify(); err != nil {
		return nil, err
	}
	return &nativeProofRequest{session: session, guard: guard, original: original, base: canonical, owned: owned, binding: binding}, nil
}

func (r *nativeProofRequest) verifySources() error {
	if err := r.original.Verify(); err != nil {
		return err
	}
	return r.guard.Verify()
}

type nativeProofToken struct {
	key, tool, source string
	request           *nativeProofRequest
}

func (r *nativeProofRequest) prepare(ctx context.Context, quint string, group []string, steps int) (nativeProofToken, bool, error) {
	r.proof = nil
	if err := ctx.Err(); err != nil {
		return nativeProofToken{}, false, err
	}
	if err := r.verifySources(); err != nil {
		return nativeProofToken{}, false, err
	}
	files, err := r.guard.Files()
	if err != nil {
		return nativeProofToken{}, false, err
	}
	inventory := make([]validate.File, 0, len(files))
	for _, f := range files {
		if !r.owned[f.Path] {
			inventory = append(inventory, f)
		}
	}
	tool, eligible := r.session.identity(ctx, quint)
	if err := ctx.Err(); err != nil {
		return nativeProofToken{}, false, err
	}
	token := nativeProofToken{request: r}
	if !eligible {
		return token, false, nil
	}
	token.tool = tool
	sourceData, _ := json.Marshal(struct {
		Base  string
		Files []validate.File
	}{r.base, inventory})
	sourceHash := sha256.Sum256(sourceData)
	token.source = hex.EncodeToString(sourceHash[:])
	data, err := json.Marshal(struct {
		Source                   string
		Group                    []string
		Steps                    int
		Tool, Config, Invocation string
	}{token.source, group, steps, tool, nativeApalacheConfig, "verify:main=simulate:init=init:step=step:owned-backend-v1"})
	if err != nil {
		return token, false, err
	}
	sum := sha256.Sum256(data)
	token.key = hex.EncodeToString(sum[:])
	proof, hit := r.session.proofs[token.key]
	if hit {
		r.proof = proof.Clone()
		r.proof.Reused = true
	}
	if err := r.verifySources(); err != nil {
		return token, false, err
	}
	if err := ctx.Err(); err != nil {
		return token, false, err
	}
	return token, hit, nil
}

func (t nativeProofToken) verify(ctx context.Context, quint string) error {
	if t.request == nil {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := t.request.verifySources(); err != nil {
		return err
	}
	if t.tool != "" {
		identity, eligible := t.request.session.identity(ctx, quint)
		if err := ctx.Err(); err != nil {
			return err
		}
		if !eligible || identity != t.tool {
			return fmt.Errorf("simulate: native proof tool identity changed during verification")
		}
	}
	if err := t.request.verifySources(); err != nil {
		return err
	}
	return ctx.Err()
}

func (t nativeProofToken) publish(group []string, steps int) {
	if t.key == "" {
		return
	}
	config := sha256.Sum256([]byte(nativeApalacheConfig))
	proof := &validate.NativeProof{ProducerBinding: t.request.binding, RequestSHA256: t.key, SourceSHA256: t.source, ToolchainSHA256: t.tool, ConfigurationSHA256: hex.EncodeToString(config[:]), Invariants: append([]string(nil), group...), Steps: steps}
	t.request.session.proofs[t.key] = proof
	t.request.proof = proof.Clone()
}
