package dbaas

import (
	"context"
	"errors"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

type simReq struct {
	Op       string
	In       CreateInput
	ID       string
	Token    string
	Password string
	Deadline time.Time // the caller's: a request arriving after it is refused
}

type simResp struct {
	DB  Database
	Err error
}

// SimClient is the API across a simulated network: requests and responses
// can be lost or duplicated (qenv.Service), and the cloud throttles
// ("dbaas.throttle").
type SimClient struct {
	svc     *qenv.Service[simReq, simResp]
	sim     *qenv.Sim
	Timeout time.Duration
}

// ErrExpired is a request the cloud refused because it arrived after its
// caller's deadline: it was not applied. The caller has given up by then
// and never sees it.
var ErrExpired = errors.New("dbaas: request arrived after its deadline (not applied)")

// throttle is the cloud refusing a request for its rate limit.
var throttle = qenv.DeclareFaults("dbaas.throttle", "throttled")

// NewSim serves cloud in s, on the virtual clock, and returns its client.
// Each request carries its caller's deadline and the cloud refuses one
// that arrives after it (ErrExpired), as a real API bounds a request's
// age (AWS refuses a signed request five minutes old; a gRPC server drops
// a request past its propagated deadline; over HTTP the connection the
// caller closed takes the request with it). Without that, a create
// delivered late (rpc.dbaas.delay) lands after the provider has seen the
// resource deleted and found nothing, and the database leaks; expire
// false keeps such a cloud, to show it.
func NewSim(s *qenv.Sim, cloud *Cloud, expire bool) *SimClient {
	cloud.Now = s.Now
	svc := qenv.NewService(s, "dbaas", nil, func(ctx context.Context, r simReq) (simResp, error) {
		if expire && s.Now().After(r.Deadline) {
			return simResp{Err: ErrExpired}, nil
		}
		if s.FaultOf(throttle) == "throttled" {
			return simResp{Err: ErrThrottled}, nil
		}
		var out simResp
		switch r.Op {
		case "create":
			out.DB, out.Err = cloud.Create(ctx, r.In)
		case "get":
			out.DB, out.Err = cloud.Get(ctx, r.ID)
		case "find":
			out.DB, out.Err = cloud.FindByToken(ctx, r.Token)
		case "password":
			out.Err = cloud.SetPassword(ctx, r.ID, r.Password)
		case "delete":
			out.Err = cloud.Delete(ctx, r.ID)
		}
		return out, nil
	})
	return &SimClient{svc: svc, sim: s, Timeout: 2 * time.Second}
}

func (c *SimClient) call(ctx context.Context, r simReq) (Database, error) {
	r.Deadline = c.sim.Now().Add(c.Timeout)
	out, err := c.svc.Call(ctx, r, c.Timeout)
	if errors.Is(err, qenv.ErrTimeout) {
		return Database{}, errors.New("dbaas: request timed out (outcome unknown)")
	}
	if err != nil {
		return Database{}, err
	}
	return out.DB, out.Err
}

// Create implements API.
func (c *SimClient) Create(ctx context.Context, in CreateInput) (Database, error) {
	return c.call(ctx, simReq{Op: "create", In: in})
}

// Get implements API.
func (c *SimClient) Get(ctx context.Context, id string) (Database, error) {
	return c.call(ctx, simReq{Op: "get", ID: id})
}

// FindByToken implements API.
func (c *SimClient) FindByToken(ctx context.Context, token string) (Database, error) {
	return c.call(ctx, simReq{Op: "find", Token: token})
}

// SetPassword implements API.
func (c *SimClient) SetPassword(ctx context.Context, id, password string) error {
	_, err := c.call(ctx, simReq{Op: "password", ID: id, Password: password})
	return err
}

// Delete implements API.
func (c *SimClient) Delete(ctx context.Context, id string) error {
	_, err := c.call(ctx, simReq{Op: "delete", ID: id})
	return err
}
