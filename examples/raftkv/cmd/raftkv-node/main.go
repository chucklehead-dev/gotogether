// Command raftkv-node is one node of raftkv's real tier: the node's code
// (raftkv.RealNode, the simulation's own) as a process, its raft messages
// and its clients' requests over HTTP on TCP, its write-ahead log a bbolt
// file fsynced at every commit. It records the cluster's history
// (cluster.binding.yaml: boot, apply, campaign, lead, stepDown) into a
// step log of its own; the clients record theirs (raftkv.HTTPNet,
// raftkv.Client).
//
//	raftkv-node -name n1 -incarnation 1 -listen 127.0.0.1:7001 \
//	  -peer n2=127.0.0.1:7002 -peer n3=127.0.0.1:7003 \
//	  -wal data/n1 -steps logs/n1.1.jsonl
//
// A restart is a new incarnation (-incarnation 2, a new -steps file): its
// recorder's id is "<name>.<incarnation>", each log's sequence numbers its
// own; every node's process is "cluster", the model's one instance. A kill (kill -9) is the crash the
// simulation's nemesis makes; what was committed to the log survives it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/examples/raftkv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// peers is the repeatable -peer name=host:port.
type peers map[string]string

func (p peers) String() string { return fmt.Sprint(map[string]string(p)) }

func (p peers) Set(v string) error {
	name, addr, ok := strings.Cut(v, "=")
	if !ok || name == "" || addr == "" {
		return fmt.Errorf("-peer %q: want name=host:port (e.g. n2=127.0.0.1:7002)", v)
	}
	p[name] = addr
	return nil
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ps := peers{}
	name := flag.String("name", "", "this node's name, one of "+strings.Join(raftkv.Nodes, ", "))
	incarnation := flag.Int("incarnation", 1, "this start's number: a restart is the next (its recorder is <name>.<incarnation>)")
	listen := flag.String("listen", "127.0.0.1:0", "the address to serve peers, clients and /status, /state on")
	flag.Var(ps, "peer", "a peer, name=host:port (repeat for each)")
	walDir := flag.String("wal", "", "the directory of the write-ahead log (kept across restarts)")
	steps := flag.String("steps", "", "the step log to write (a new file per incarnation; none if empty)")
	mutation := flag.String("mutation", "", "a mutant, by name: "+mutantNames())
	version := flag.String("version", "v1", "the code's version: v1, or v2 (a rolling upgrade's)")
	flag.Parse()

	index := slices.Index(raftkv.Nodes, *name)
	if index < 0 {
		return fmt.Errorf("-name %q: want one of %s", *name, strings.Join(raftkv.Nodes, ", "))
	}
	m, ok := raftkv.Mutations[*mutation]
	if !ok {
		return fmt.Errorf("-mutation %q: want one of %s", *mutation, mutantNames())
	}
	if *version != "v1" && *version != "v2" {
		return fmt.Errorf("-version %q: want v1 or v2", *version)
	}
	if *walDir == "" {
		return errors.New("-wal: a directory is needed (the log must survive a restart)")
	}
	addrs := make([]string, len(raftkv.Nodes))
	for i, n := range raftkv.Nodes {
		if i == index {
			continue
		}
		if addrs[i] = ps[n]; addrs[i] == "" {
			return fmt.Errorf("-peer %s=host:port missing: every other node needs one", n)
		}
	}
	for n := range ps {
		if !slices.Contains(raftkv.Nodes, n) || n == *name {
			return fmt.Errorf("-peer %s: not another node (want %s, without %s)", n, strings.Join(raftkv.Nodes, ", "), *name)
		}
	}

	ctx := context.Background()
	if *steps != "" {
		// a new file per incarnation: FileSink truncates, and an earlier
		// incarnation's steps are evidence
		if _, err := os.Stat(*steps); err == nil {
			return fmt.Errorf("-steps %s exists: give each incarnation a new file", *steps)
		}
		sink, err := qobs.FileSink(*steps, qobs.PerStep) // a kill -9 loses no step
		if err != nil {
			return err
		}
		// one process for every node, as a simulated run records them: the
		// cluster model's instance is the cluster, whose nodes' crashes are
		// its steps, not restarts of the instance
		id := fmt.Sprintf("%s.%d", *name, *incarnation)
		ctx = qobs.WithRecorder(ctx, qobs.NewRecorder(sink, qobs.WithID(id), qobs.WithProcess("cluster")))
	}

	wal, err := raftkv.OpenBoltWAL(*walDir)
	if err != nil {
		return err
	}
	node := raftkv.NewRealNode(raftkv.RealConfig{Index: index, WAL: wal, Peers: raftkv.HTTPNet{Addrs: addrs},
		Version: *version, Mutation: m})
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	// the harness reads the address from here when -listen's port is 0
	log.Printf("raftkv-node %s.%d listening on %s (%s, code %s)", *name, *incarnation, ln.Addr(), m, *version)
	go func() { //qenv:ok the real tier: the server is a goroutine of the process
		log.Fatal(http.Serve(ln, node.Handler()))
	}()
	node.Run(ctx) // never returns: the node stops when killed
	return nil
}

func mutantNames() string {
	var out []string
	for k := range raftkv.Mutations {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
