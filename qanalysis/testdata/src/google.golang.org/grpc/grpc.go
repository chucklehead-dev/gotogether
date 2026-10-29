// Package grpc is a stub of gRPC for quintraw's fixtures.
package grpc

import "context"

// A ClientConn is a connection.
type ClientConn struct{}

// Invoke calls a method.
func (c *ClientConn) Invoke(ctx context.Context, method string, args, reply any) error { return nil }

// NewClient dials.
func NewClient(target string) (*ClientConn, error) { return &ClientConn{}, nil }
