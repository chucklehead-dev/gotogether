package model

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

type nativeBackendKey struct{}
type nativeBackendIdentity struct{ java, jar string }
type nativeBackendObserver func(context.Context, *os.Process, string, nativeBackendIdentity) func() bool
type nativeBackendFactory func(context.Context) (*nativeBackendRequest, error)

// Reservation keeps the endpoint unavailable until the owned Quint process has
// started. It is transport plumbing, never part of the mathematical proof key.
// Actual backend ownership still has to be observed before proof publication.
type nativeBackendRequest struct {
	endpoint   string
	expected   nativeBackendIdentity
	listener   net.Listener
	observer   nativeBackendObserver
	owned      bool
	stopCancel func() bool
}

func newOwnedNativeBackend(ctx context.Context) (*nativeBackendRequest, error) {
	java, err := exec.LookPath("java")
	if err != nil {
		return nil, err
	}
	java, err = filepath.EvalSymlinks(java)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := os.Getenv("QUINT_HOME")
	if root == "" {
		root = filepath.Join(home, ".quint")
	}
	jar, err := filepath.EvalSymlinks(filepath.Join(root, "apalache-dist-0.62.4", "apalache", "lib", "apalache.jar"))
	if err != nil {
		return nil, err
	}
	return reserveNativeBackend(ctx, nativeBackendIdentity{java: java, jar: jar}, observeNativeBackend)
}

func reserveNativeBackend(ctx context.Context, expected nativeBackendIdentity, observer nativeBackendObserver) (*nativeBackendRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	r := &nativeBackendRequest{endpoint: listener.Addr().String(), expected: expected, listener: listener, observer: observer}
	r.stopCancel = context.AfterFunc(ctx, func() { _ = listener.Close() })
	return r, nil
}
func (r *nativeBackendRequest) close() {
	if r != nil && r.listener != nil {
		if r.stopCancel != nil {
			r.stopCancel()
		}
		_ = r.listener.Close()
	}
}
func (r *nativeBackendRequest) start(ctx context.Context, leader *os.Process) func() {
	if r.observer == nil {
		return func() {}
	}
	stop := r.observer(ctx, leader, r.endpoint, r.expected)
	return sync.OnceFunc(func() { r.owned = stop() })
}
