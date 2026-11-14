package simvalkey

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/simvalkey/internal/engine"
	"github.com/valkey-io/valkey-go"
	"github.com/valkey-io/valkey-go/mock"
)

// Client is a valkey-go client at the simulation seam. Separate commands,
// including DoMulti commands, interleave; each Lua body executes atomically.
// Pub/sub, client caching, streaming and transactions are refused explicitly.
type Client struct {
	// Timeout is the virtual response timeout; zero waits until a response,
	// cancellation or connection failure. Use WithTimeout for a context deadline.
	Timeout         time.Duration
	server          *Server
	owner           *Client
	builder         *mock.Client
	closed          bool
	incarnation     int
	lastWrite       uint64
	issued, serving uint64
}

var _ valkey.Client = (*Client)(nil)

func newClient(s *Server) *Client { return &Client{server: s, builder: mock.NewClient(nil)} }

// B returns valkey-go's ordinary standalone command builder.
func (c *Client) B() valkey.Builder { return c.builder.B() }

// Close rejects subsequent calls and wakes pending calls with ErrClosing.
func (c *Client) Close()         { c.closed = true }
func (c *Client) isClosed() bool { return c.closed || c.owner != nil && c.owner.isClosed() }

// Mode reports the modeled single-primary deployment.
func (c *Client) Mode() valkey.ClientMode { return valkey.ClientModeStandalone }

// Nodes returns the modeled server as one node.
func (c *Client) Nodes() map[string]valkey.Client { return map[string]valkey.Client{c.server.name: c} }

func fail(err error) valkey.ValkeyResult      { return valkey.NewErrorResult(err) }
func uncertain(err error) valkey.ValkeyResult { return fail(errors.Join(ErrUnknownOutcome, err)) }
func message(v any) valkey.ValkeyMessage {
	switch x := v.(type) {
	case nil:
		return mock.ValkeyNil()
	case string:
		return mock.ValkeyBlobString(x)
	case int64:
		return mock.ValkeyInt64(x)
	case []any:
		out := make([]valkey.ValkeyMessage, len(x))
		for i, a := range x {
			out[i] = message(a)
		}
		return mock.ValkeyArray(out...)
	default:
		panic(fmt.Sprintf("simvalkey: unsupported response %T", v))
	}
}

// Do sends one command. Outages before dispatch are distinct from cancellation
// or timeout after dispatch, whose error also matches ErrUnknownOutcome.
func (c *Client) Do(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
	return c.batch(ctx, []valkey.Completed{cmd})[0]
}

func (c *Client) batch(ctx context.Context, cmds []valkey.Completed) []valkey.ValkeyResult {
	if len(cmds) == 0 {
		return nil
	}
	failure := func(err error, unknown bool) []valkey.ValkeyResult {
		out := make([]valkey.ValkeyResult, len(cmds))
		for i := range out {
			if unknown {
				out[i] = uncertain(err)
			} else {
				out[i] = fail(err)
			}
		}
		return out
	}
	if err := contextError(ctx); err != nil {
		return failure(err, false)
	}
	if c.isClosed() {
		return failure(valkey.ErrClosing, false)
	}
	s := c.server
	if qenv.From(ctx) != s.sim {
		panic("simvalkey: commands must run in the server's simulation")
	}
	if dl, ok := ctx.Deadline(); ok {
		if owner, ok := ctx.Value(timeoutKey{}).(*timeoutContext); !ok || !owner.deadline.Equal(dl) {
			panic("simvalkey: use WithTimeout for simulation deadlines, not context.WithTimeout")
		}
	}
	if c.Timeout < 0 {
		panic("simvalkey: negative client timeout")
	}
	commands := make([][]string, len(cmds))
	for i, cmd := range cmds {
		commands[i] = slices.Clone(cmd.Commands())
	}
	qenv.Yield(ctx)
	if err := contextError(ctx); err != nil {
		return failure(err, false)
	}
	if c.isClosed() {
		return failure(valkey.ErrClosing, false)
	}
	if s.Proc.Down() {
		return failure(ErrUnavailable, false)
	}
	if s.sim.FaultOf(s.failover) == "lose" {
		s.Failover()
		return failure(ErrUnavailable, false)
	}
	requestFault := s.sim.FaultOf(s.request)
	if requestFault == "outage" {
		return failure(ErrUnavailable, false)
	}
	if c.incarnation != s.Proc.Incarnation {
		c.incarnation = s.Proc.Incarnation
		c.lastWrite = 0
		c.issued = 0
		c.serving = 0
	}
	inc := c.incarnation
	caller := qenv.ProcessOf(ctx)
	callerInc := 0
	if caller != nil {
		callerInc = caller.Incarnation
	}
	callerGone := func() bool { return caller != nil && (caller.Down() || caller.Incarnation != callerInc) }
	type response struct {
		done, abandoned bool
		results         []valkey.ValkeyResult
	}
	r := &response{results: make([]valkey.ValkeyResult, len(commands))}
	defer func() { r.abandoned = true }()
	abandoned := func() bool { return r.abandoned || contextError(ctx) != nil || c.isClosed() || callerGone() }
	if requestFault != "hang" && !s.sim.Net().Partitioned(caller, s.Proc) {
		sequence := c.issued
		c.issued++
		s.Proc.Spawn("command", func(serverCtx context.Context) {
			qenv.Wait(serverCtx, func() bool { return sequence == c.serving })
			for i, args := range commands {
				var value any
				var err error
				if len(args) == 0 {
					err = engine.Error("ERR empty command")
				} else if strings.EqualFold(args[0], "WAIT") {
					value, err = c.wait(serverCtx, args, abandoned)
					if abandoned() {
						c.serving++
						return
					}
				} else {
					before := s.db.Writes
					value, err = s.db.Exec(s.sim.Now(), args)
					if s.db.Writes != before {
						s.revision++
						s.dataRevision = s.revision
						c.lastWrite = s.revision
					}
					s.check()
					s.advanceReplica()
				}
				if u, ok := errors.AsType[*engine.Unsupported](err); ok {
					panic(u)
				}
				if e, ok := errors.AsType[engine.Error](err); ok {
					r.results[i] = valkey.NewResult(mock.ValkeyError(string(e)), nil)
				} else if err != nil {
					r.results[i] = fail(err)
				} else {
					r.results[i] = valkey.NewResult(message(value), nil)
				}
				// Pipeline commands preserve connection order but other clients may run
				// between them. A Lua command's entire body has already completed.
				qenv.Yield(serverCtx)
			}
			c.serving++
			if s.sim.FaultOf(s.response) == "lost" || s.sim.Net().Partitioned(caller, s.Proc) {
				return
			}
			r.done = true
		})
	}
	ready := func() bool {
		return r.done || contextError(ctx) != nil || c.isClosed() || s.Proc.Down() || s.Proc.Incarnation != inc
	}
	if c.Timeout > 0 {
		qenv.WaitFor(ctx, ready, c.Timeout)
	} else {
		qenv.Wait(ctx, ready)
	}
	if r.done {
		return r.results
	}
	if err := contextError(ctx); err != nil {
		return failure(err, true)
	}
	if c.isClosed() {
		return failure(valkey.ErrClosing, true)
	}
	if s.Proc.Down() || s.Proc.Incarnation != inc {
		return failure(ErrUnavailable, true)
	}
	return failure(qenv.ErrTimeout, true)
}

func (c *Client) wait(ctx context.Context, args []string, abandoned func() bool) (any, error) {
	if len(args) != 3 {
		return nil, engine.Error("ERR wrong number of arguments for WAIT")
	}
	n, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != args[1] {
		return nil, engine.Error("ERR value is not an integer or out of range")
	}
	ms, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil || ms < 0 || strconv.FormatInt(ms, 10) != args[2] || ms > int64((time.Duration(1<<63-1))/time.Millisecond) {
		return nil, engine.Error("ERR timeout is not a nonnegative duration")
	}
	s := c.server
	target := c.lastWrite
	s.advanceReplica()
	ready := func() bool { return abandoned() || n <= 1 && s.replicaRevision >= target }
	if n == 0 {
		if s.replicaRevision >= target {
			return int64(1), nil
		}
		return int64(0), nil
	}
	if ms == 0 {
		qenv.Wait(ctx, ready)
	} else {
		qenv.WaitFor(ctx, ready, time.Duration(ms)*time.Millisecond)
	}
	if !abandoned() && s.replicaRevision >= target {
		return int64(1), nil
	}
	return int64(0), nil
}

// DoMulti preserves command order with a scheduling boundary between commands;
// it is a pipeline, not a transaction or an atomic script.
func (c *Client) DoMulti(ctx context.Context, cmds ...valkey.Completed) []valkey.ValkeyResult {
	return c.batch(ctx, cmds)
}

// Dedicated supplies an independent logical connection for the callback.
func (c *Client) Dedicated(fn func(valkey.DedicatedClient) error) error {
	if c.isClosed() {
		return valkey.ErrClosing
	}
	d, release := c.Dedicate()
	defer release()
	return fn(d)
}

// Dedicate supplies one logical connection until its release is called.
func (c *Client) Dedicate() (valkey.DedicatedClient, func()) {
	d := &dedicated{newClient(c.server)}
	d.owner = c
	d.Timeout = c.Timeout
	return d, d.Close
}

type dedicated struct{ *Client }

func unsupported(feature string) { panic("simvalkey: unsupported: " + feature) }

// Receive refuses pub/sub in this profile.
func (c *Client) Receive(context.Context, valkey.Completed, func(valkey.PubSubMessage)) error {
	unsupported("pub/sub")
	return nil
}

// DoCache refuses client-side caching instead of silently strengthening reads.
func (c *Client) DoCache(context.Context, valkey.Cacheable, time.Duration) valkey.ValkeyResult {
	unsupported("client-side caching")
	return valkey.ValkeyResult{}
}

// DoMultiCache refuses client-side caching.
func (c *Client) DoMultiCache(context.Context, ...valkey.CacheableTTL) []valkey.ValkeyResult {
	unsupported("client-side caching")
	return nil
}

// DoStream refuses streaming replies.
func (c *Client) DoStream(context.Context, valkey.Completed) valkey.ValkeyResultStream {
	unsupported("streaming replies")
	return valkey.ValkeyResultStream{}
}

// DoMultiStream refuses streaming replies.
func (c *Client) DoMultiStream(context.Context, ...valkey.Completed) valkey.MultiValkeyResultStream {
	unsupported("streaming replies")
	return valkey.MultiValkeyResultStream{}
}
func (d *dedicated) SetPubSubHooks(valkey.PubSubHooks) <-chan error {
	unsupported("pub/sub hooks")
	return nil
}
func (d *dedicated) SetOnInvalidations(func([]valkey.ValkeyMessage)) <-chan error {
	unsupported("client-side invalidations")
	return nil
}
