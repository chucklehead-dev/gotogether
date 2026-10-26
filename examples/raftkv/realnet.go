package raftkv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"time"

	"go.etcd.io/raft/v3"
)

// The real tier: the node's code as a process of its own (cmd/raftkv-node)
// over HTTP on TCP, its log a BoltWAL. Outside a simulation qenv's seams
// fall through to the real thing (qenv.Go a goroutine, Sleep and WaitFor
// real time), so start, loop, send and the handlers run unchanged, the
// incarnation's lock (inc.mu) keeping them apart.

// RealPeerTimeout is a raft batch's call in the real tier: a real
// network's round trip and an fsync on the other side, which the
// simulation's RPCTimeout (20ms) would cut short under load.
const RealPeerTimeout = time.Second

// RealNode is one node of the real tier.
type RealNode struct {
	*Node
}

// RealConfig is a node of the real tier: which of Nodes it is, its log,
// how it reaches its peers, its code's version and its mutant.
type RealConfig struct {
	Index    int
	WAL      WAL
	Peers    PeerNet
	Version  string // "v2" for the upgraded code; anything else is v1
	Mutation Mutation
}

// NewRealNode is node cfg.Index of a cluster of Nodes, recording the
// cluster's history (boot, apply, campaign, lead, stepDown) into the
// recorder in Run's context. The others are names only: it reaches them
// through cfg.Peers.
func NewRealNode(cfg RealConfig) *RealNode {
	d := &Deployment{cfg: Config{Mutation: cfg.Mutation, Record: Cluster}}
	d.cfg.defaults()
	for i, name := range Nodes {
		d.Nodes = append(d.Nodes, &Node{d: d, Index: i, Name: name})
	}
	n := d.Nodes[cfg.Index]
	n.WAL, n.Peers, n.version = cfg.WAL, cfg.Peers, cfg.Version
	return &RealNode{n}
}

// Run boots the node from its log and runs it: its senders as goroutines,
// its raft loop in the caller's. It does not return; a node stops by
// being killed, as the simulation's crash does.
func (n *RealNode) Run(ctx context.Context) { n.start(ctx) }

// Status is a node's raft status, as /status serves it.
type Status struct {
	Node    string `json:"node"`
	Up      bool   `json:"up"` // booted: its log read
	Applied int    `json:"applied"`
	Term    int    `json:"term"`
	State   string `json:"state"`
	Leader  string `json:"leader"` // "" if unknown
	Code    int    `json:"code"`   // the code's version
	Log     int    `json:"log"`    // the log format the cluster writes
}

// State is what a node's state machine holds, as /state serves it: the
// probe's observe steps (model/raftkv.qnt's observe).
type State struct {
	Node    string         `json:"node"`
	Applied int            `json:"applied"`
	KV      map[string]int `json:"kv"`
}

// Status is the node's status now.
func (n *RealNode) Status() Status {
	st := Status{Node: n.Name}
	inc := n.inc.Load()
	if inc == nil {
		return st
	}
	inc.mu.Lock()
	defer inc.mu.Unlock()
	bs := inc.rn.BasicStatus()
	st.Up, st.Applied, st.Term, st.State = true, int(inc.applied), int(bs.GetTerm()), inc.state.String()
	if inc.lead != raft.None {
		st.Leader = Nodes[inc.lead-1]
	}
	st.Code, st.Log = inc.version, inc.sm.Version
	return st
}

// State is the node's state machine now; ok is false while it boots.
func (n *RealNode) State() (State, bool) {
	inc := n.inc.Load()
	if inc == nil {
		return State{}, false
	}
	inc.mu.Lock()
	defer inc.mu.Unlock()
	return State{Node: n.Name, Applied: int(inc.applied), KV: maps.Clone(inc.sm.KV)}, true
}

// Handler serves the node: its peers' raft batches (POST /raft), its
// clients' requests (POST /kv), and for the harness /status and /state.
// A request to a node still booting is refused (503), as the simulated
// one is (errDown).
func (n *RealNode) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /raft", func(w http.ResponseWriter, r *http.Request) {
		var env Envelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := n.receive(r.Context(), env); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /kv", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, _ := n.serve(r.Context(), req) // serve's errors are in the response
		writeJSON(w, resp)
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, n.Status()) })
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		st, ok := n.State()
		if !ok {
			http.Error(w, errDown.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, st)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) // a client gone is its own failure
}

// HTTPNet is both networks over HTTP: a node's to its peers (PeerNet)
// and a client's to the nodes (KVNet). Addrs are the nodes' host:port by
// index ("" for the node itself).
type HTTPNet struct {
	Addrs  []string
	Client *http.Client // http.DefaultClient if nil
}

func (h HTTPNet) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return http.DefaultClient
}

// post sends v to path on node i and decodes the answer into out (nil:
// none expected), within timeout.
func (h HTTPNet) post(ctx context.Context, i int, path string, v, out any, timeout time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+h.Addrs[i]+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("raftkv: %s%s: %s: %s", h.Addrs[i], path, resp.Status, bytes.TrimSpace(msg))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body) // so the connection is reused
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Send is a raft batch to peer to; a failure (refused, timed out, the
// peer booting) loses it, and the node reports the peer unreachable, as
// a failed simulated call does.
func (h HTTPNet) Send(ctx context.Context, to int, env Envelope) error {
	return h.post(ctx, to, "/raft", env, nil, RealPeerTimeout)
}

// Call is a client's request to node, answered within timeout.
func (h HTTPNet) Call(ctx context.Context, node int, req Request, timeout time.Duration) (Response, error) {
	var resp Response
	err := h.post(ctx, node, "/kv", req, &resp, timeout)
	return resp, err
}

// get reads path of node i into out.
func (h HTTPNet) get(ctx context.Context, i int, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, RealPeerTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+h.Addrs[i]+path, nil)
	if err != nil {
		return err
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("raftkv: %s%s: %s", h.Addrs[i], path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Status is node i's /status.
func (h HTTPNet) Status(ctx context.Context, i int) (Status, error) {
	var st Status
	return st, h.get(ctx, i, "/status", &st)
}

// State is node i's /state.
func (h HTTPNet) State(ctx context.Context, i int) (State, error) {
	var st State
	return st, h.get(ctx, i, "/state", &st)
}

var (
	_ PeerNet = HTTPNet{}
	_ KVNet   = HTTPNet{}
)
