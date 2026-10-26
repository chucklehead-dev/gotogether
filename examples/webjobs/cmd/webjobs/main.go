// Command webjobs serves the webjobs app. With -steps it records the
// server's effects as a step log (qobs.FileSink, one write per step), and
// on SIGINT or SIGTERM stops taking requests, waits for every job to end
// and records that it drained and settled.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/webjobs/webjobs"
	"github.com/chucklehead-dev/gotogether/qobs"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address (port 0: any; the address is printed)")
	steps := flag.String("steps", "", "write the server's steps to this file")
	seed := flag.Int64("seed", time.Now().UnixNano(), "seed of the jobs' durations")
	minWork := flag.Duration("min-work", 0, "a job's shortest run (with -max-work; default 300ms to 1.2s)")
	maxWork := flag.Duration("max-work", 0, "a job's longest run")
	flag.Parse()

	cfg := webjobs.Config{Seed: *seed, MinWork: *minWork, MaxWork: *maxWork}
	var sink *qobs.JSONLSink
	if *steps != "" {
		var err error
		if sink, err = qobs.FileSink(*steps, qobs.PerStep); err != nil {
			log.Fatal(err)
		}
		cfg.Recorder = qobs.NewRecorder(sink, qobs.WithID("server"), qobs.WithActor("webjobs"), qobs.WithProcess("webjobs"))
	}
	// before the address is printed: a harness may stop the server as
	// soon as it reads it
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	app := webjobs.New(cfg)
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("webjobs: http://%s/\n", ln.Addr()) // stdout: a harness reads the address
	srv := &http.Server{Handler: app}
	go srv.Serve(ln)

	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Print(err)
	}
	app.Drain()
	if sink != nil {
		if err := sink.Close(); err != nil {
			log.Fatal(err)
		}
	}
}
