// Package ordersync is a service built on gotogether from outside its
// repository: orders placed over gRPC are published to a log, and a
// syncer reads them back. In production the log is Kafka (Kafka); in a
// simulation it is qenv/simlog, and the gRPC server simgrpc's (Run).
package ordersync

import (
	"context"
	"errors"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/simgrpc"
	"github.com/chucklehead-dev/gotogether/qenv/simlog"
	"github.com/chucklehead-dev/gotogether/qenv/simlog/kafkalog"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Orders is how many orders a run's client places (the model's ORDERS).
const Orders = 3

// Service places orders: each is a step, then a record on the log.
type Service struct{ Log simlog.Writer }

// Place records the order and publishes it, answering once the log has
// it.
func (s *Service) Place(ctx context.Context, in *wrapperspb.Int64Value) (*wrapperspb.Int64Value, error) {
	qobs.Record(ctx, "place", nil, "id", int(in.Value))
	done := make(chan error, 1)
	s.Log.Produce(ctx, strconv.FormatInt(in.Value, 10), []byte(strconv.FormatInt(in.Value, 10)), func(err error) { done <- err })
	s.Log.Flush(ctx)
	if err := <-done; err != nil {
		return nil, err
	}
	return in, nil
}

const placeMethod = "/ordersync.Orders/Place"

// desc is the service as protoc-gen-go-grpc would generate it.
var desc = grpc.ServiceDesc{
	ServiceName: "ordersync.Orders",
	HandlerType: (*interface{})(nil),
	Methods: []grpc.MethodDesc{{MethodName: "Place", Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		in := new(wrapperspb.Int64Value)
		if err := dec(in); err != nil {
			return nil, err
		}
		return srv.(*Service).Place(ctx, in)
	}}},
}

// Sync reads the log until ctx ends, recording each order synced and
// committing after it: at least once.
func Sync(ctx context.Context, r simlog.Reader) {
	for ctx.Err() == nil {
		recs, err := r.Poll(ctx)
		if err != nil {
			continue
		}
		for _, rec := range recs {
			id, _ := strconv.Atoi(string(rec.Value))
			qobs.Record(ctx, "sync", nil, "id", id)
			_ = r.Commit(ctx, rec.Partition, rec.Offset+1)
		}
	}
}

// Run deploys the service, its log and syncer, and a client placing
// Orders orders, on c.
func Run(c qenv.Chooser) error {
	s := qenv.New(c)
	defer s.Close()
	log := simlog.New(s, "orders", 2)
	srv := simgrpc.NewServer(s, "orders", nil)
	srv.RegisterService(&desc, &Service{Log: log.Producer(true)})
	s.Spawn("syncer", func(ctx context.Context) {
		qenv.Daemon(ctx)
		Sync(ctx, log.Member(ctx, "syncer"))
	})
	var err error
	s.Spawn("client", func(ctx context.Context) {
		conn := srv.Dial()
		for id := int64(1); id <= Orders && err == nil; id++ {
			err = place(ctx, conn, id)
		}
	})
	if rerr := s.Run(time.Minute); rerr != nil {
		return rerr
	}
	return err
}

// place calls Place until it is answered: a lost response looks like a
// lost request, so the order may be placed twice.
func place(ctx context.Context, conn grpc.ClientConnInterface, id int64) error {
	for range 10 {
		if err := conn.Invoke(ctx, placeMethod, wrapperspb.Int64(id), new(wrapperspb.Int64Value)); err == nil {
			return nil
		}
	}
	return errors.New("ordersync: no answer after 10 attempts")
}

// Kafka is the service's log on a Kafka cluster: kafkalog's writer and
// reader are simlog's seams, so Service and Sync run on it unchanged.
func Kafka(brokers []string, topic string) (simlog.Writer, simlog.Reader, error) {
	w, err := kafkalog.NewWriter(brokers, topic, true)
	if err != nil {
		return nil, nil, err
	}
	r, err := kafkalog.NewReader(brokers, topic, "ordersync")
	if err != nil {
		w.Close()
		return nil, nil, err
	}
	return w, r, nil
}
