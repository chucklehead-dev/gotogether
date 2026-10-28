package simgrpc_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/simgrpc"
)

// counter is a service as protoc-gen-go-grpc would generate it, by hand:
// one unary method, Add, over a well-known type (no generated code).
type counter struct {
	total int64
	// refuse, if set, is Add's error.
	refuse error
	// callers are the "caller" metadata of each call.
	callers []string
}

func (c *counter) Add(ctx context.Context, in *wrapperspb.Int64Value) (*wrapperspb.Int64Value, error) {
	if c.refuse != nil {
		return nil, c.refuse
	}
	md, _ := metadata.FromIncomingContext(ctx)
	c.callers = append(c.callers, strings.Join(md.Get("caller"), ","))
	c.total += in.Value
	in.Value = -1 // the caller's message is not this one: it was marshalled
	return wrapperspb.Int64(c.total), nil
}

const addMethod = "/test.Counter/Add"

var counterDesc = grpc.ServiceDesc{
	ServiceName: "test.Counter",
	HandlerType: (*interface{})(nil),
	Methods: []grpc.MethodDesc{{MethodName: "Add", Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := new(wrapperspb.Int64Value)
		if err := dec(in); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return srv.(*counter).Add(ctx, in)
		}
		info := &grpc.UnaryServerInfo{Server: srv, FullMethod: addMethod}
		return interceptor(ctx, in, info, func(ctx context.Context, req any) (any, error) {
			return srv.(*counter).Add(ctx, req.(*wrapperspb.Int64Value))
		})
	}}},
}

// script makes each named fault kind's choice once, as the option given,
// and every other choice nominal.
type script map[string]int

func (s script) Choose(kind string, n int) int {
	k := strings.TrimPrefix(kind, "fault.")
	if o, ok := s[k]; ok && qenv.IsFault(kind) {
		delete(s, k)
		return o
	}
	return 0
}

// run serves a counter in a simulation chosen by c and runs client
// against it, returning the counter and the server.
func run(t *testing.T, c qenv.Chooser, cnt *counter, client func(ctx context.Context, conn *simgrpc.Conn)) *simgrpc.Server {
	t.Helper()
	s := qenv.New(c)
	defer s.Close()
	srv := simgrpc.NewServer(s, "counter", nil)
	srv.RegisterService(&counterDesc, cnt)
	s.Spawn("client", func(ctx context.Context) { client(ctx, srv.Dial()) })
	if err := s.Run(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	return srv
}

func add(ctx context.Context, conn grpc.ClientConnInterface, n int64) (int64, error) {
	out := new(wrapperspb.Int64Value)
	err := conn.Invoke(ctx, addMethod, wrapperspb.Int64(n), out)
	return out.Value, err
}

// A call crosses the simulated network as on the wire: the handler gets
// a copy of the request and the outgoing metadata as incoming, and the
// interceptor wraps it.
func TestCall(t *testing.T) {
	cnt := &counter{}
	var intercepted []string
	var got []int64
	var req *wrapperspb.Int64Value
	s := qenv.New(script{})
	defer s.Close()
	srv := simgrpc.NewServer(s, "counter", nil)
	srv.Interceptor = func(ctx context.Context, r any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		intercepted = append(intercepted, info.FullMethod)
		return h(ctx, r)
	}
	srv.RegisterService(&counterDesc, cnt)
	s.Spawn("client", func(ctx context.Context) {
		conn := srv.Dial()
		ctx = metadata.AppendToOutgoingContext(ctx, "caller", "alice")
		for _, n := range []int64{2, 3} {
			req = wrapperspb.Int64(n)
			out := new(wrapperspb.Int64Value)
			if err := conn.Invoke(ctx, addMethod, req, out); err != nil {
				t.Errorf("Add(%d): %v", n, err)
			}
			got = append(got, out.Value)
		}
	})
	if err := s.Run(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 2 || got[1] != 5 {
		t.Errorf("totals %v, want [2 5]", got)
	}
	if req.Value != 3 {
		t.Errorf("the handler changed the caller's request to %d", req.Value)
	}
	if srv.Calls() != 2 || len(intercepted) != 2 || intercepted[0] != addMethod {
		t.Errorf("%d calls, intercepted %v", srv.Calls(), intercepted)
	}
	if len(cnt.callers) != 2 || cnt.callers[0] != "alice" {
		t.Errorf("the handler saw callers %q, want alice twice", cnt.callers)
	}
}

// Each network fault is the status a real client sees, and does to the
// handler what the network would.
func TestFaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fault  script
		code   codes.Code
		total  int64 // after the call
		calls  int
		answer int64 // the call's answer, when it has one
	}{
		{"refused", script{"grpc.counter.conn": 1}, codes.Unavailable, 0, 0, 0},
		{"dropped", script{"rpc.counter.request": 1}, codes.DeadlineExceeded, 0, 0, 0},
		{"duplicated", script{"rpc.counter.request": 2}, codes.OK, 8, 2, 4},
		{"response lost", script{"rpc.counter.response": 1}, codes.DeadlineExceeded, 4, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cnt := &counter{}
			var answer int64
			var err error
			srv := run(t, tc.fault, cnt, func(ctx context.Context, conn *simgrpc.Conn) {
				answer, err = add(ctx, conn, 4)
			})
			if status.Code(err) != tc.code {
				t.Errorf("status %v (%v), want %v", status.Code(err), err, tc.code)
			}
			if cnt.total != tc.total || srv.Calls() != tc.calls {
				t.Errorf("total %d after %d calls, want %d after %d", cnt.total, srv.Calls(), tc.total, tc.calls)
			}
			if err == nil && answer != tc.answer {
				t.Errorf("answer %d, want %d", answer, tc.answer)
			}
		})
	}
}

// A timeout is virtual time: a call whose response is lost fails after
// the connection's Timeout, not after a wall-clock deadline.
func TestTimeoutIsVirtual(t *testing.T) {
	var took time.Duration
	start := time.Now()
	run(t, script{"rpc.counter.response": 1}, &counter{}, func(ctx context.Context, conn *simgrpc.Conn) {
		conn.Timeout = 5 * time.Second
		t0 := qenv.Now(ctx)
		_, _ = add(ctx, conn, 1)
		took = qenv.Now(ctx).Sub(t0)
	})
	if took < 5*time.Second || took > 6*time.Second {
		t.Errorf("the lost response failed after %v of virtual time, want the 5s timeout", took)
	}
	if real := time.Since(start); real > 5*time.Second {
		t.Errorf("the run took %v of real time", real)
	}
}

// A handler's status reaches the caller as it is; a method nobody
// registered, and any stream, are Unimplemented.
func TestStatuses(t *testing.T) {
	var refused, unknown, stream error
	run(t, script{}, &counter{refuse: status.Error(codes.FailedPrecondition, "closed")}, func(ctx context.Context, conn *simgrpc.Conn) {
		_, refused = add(ctx, conn, 1)
		unknown = conn.Invoke(ctx, "/test.Counter/Sub", wrapperspb.Int64(1), new(wrapperspb.Int64Value))
		_, stream = conn.NewStream(ctx, &grpc.StreamDesc{StreamName: "Watch"}, "/test.Counter/Watch")
	})
	if s := status.Convert(refused); s.Code() != codes.FailedPrecondition || s.Message() != "closed" {
		t.Errorf("the handler's status arrived as %v", refused)
	}
	if status.Code(unknown) != codes.Unimplemented || status.Code(stream) != codes.Unimplemented {
		t.Errorf("unknown method: %v; stream: %v; want Unimplemented", unknown, stream)
	}
}

// A crash of the server's process kills the handler and loses the
// request: the caller times out, and the server, restarted, serves the
// next call.
func TestServerCrash(t *testing.T) {
	s := qenv.New(script{})
	defer s.Close()
	proc := s.Process("server", true, func(context.Context) {})
	srv := simgrpc.NewServer(s, "counter", proc)
	cnt := &counter{}
	srv.RegisterService(&counterDesc, cnt)
	var first, second error
	s.Spawn("client", func(ctx context.Context) {
		conn := srv.Dial()
		proc.Crash()
		_, first = add(ctx, conn, 1)
		proc.Restart()
		_, second = add(ctx, conn, 1)
	})
	if err := s.Run(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	if status.Code(first) == codes.OK || second != nil || cnt.total != 1 {
		t.Errorf("down: %v; restarted: %v; total %d, want a failure, then the call applied once", first, second, cnt.total)
	}
}
