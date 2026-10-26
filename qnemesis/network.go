package qnemesis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// A Network cuts, heals and degrades the links between two named
// endpoints (targets, or clients that are none), both directions at once:
// a fault names a pair, as qenv's nemeses do ("nemesis.partition.a.b").
type Network interface {
	// Cut breaks every link between a and b: their connections close and
	// new ones are refused until Heal.
	Cut(ctx context.Context, a, b string) error
	Heal(ctx context.Context, a, b string) error
	// Latency delays each response between a and b by d, give or take
	// jitter (uniformly). Latency and Loss replace each other on a link.
	Latency(ctx context.Context, a, b string, d, jitter time.Duration) error
	// Loss drops the responses of a share (toxicity, from 0 to 1) of the
	// connections between a and b, closing each after timeout.
	Loss(ctx context.Context, a, b string, toxicity float64, timeout time.Duration) error
	// Clear removes Latency and Loss between a and b.
	Clear(ctx context.Context, a, b string) error
	// Pairs are the pairs with a link, each sorted (a < b): what
	// "nemesis.isolate.<p>" cuts, and what a fault may name.
	Pairs() [][2]string
}

// Toxiproxy is a Network made of toxiproxy's proxies
// (github.com/Shopify/toxiproxy), one per directed link (Link), driven by
// its HTTP API at URL (default http://127.0.0.1:8474). A cut disables the
// link's proxies, which closes their connections and refuses new ones; a
// timeout toxic would hold them open and drop bytes from the middle of a
// stream, which a TCP peer cannot see: a framed protocol would read
// garbage after the heal. Latency is a latency toxic, loss a timeout toxic
// (both downstream: on the responses) with a toxicity, which toxiproxy
// draws per connection, not per message: over a pooled connection a loss
// fault loses every response or none. A link has latency or loss, not
// both: each replaces the other (toxiproxy deadlocks removing a latency
// toxic stacked before a timeout toxic that closed its connection).
type Toxiproxy struct {
	URL string
	// Listen is the host proxies listen on (default 127.0.0.1); each
	// gets a free port.
	Listen string
	Client *http.Client

	mu      sync.Mutex
	proxies map[[2]string][]string // a sorted pair's proxies
}

func (t *Toxiproxy) base() string {
	if t.URL == "" {
		return "http://127.0.0.1:8474"
	}
	return strings.TrimSuffix(t.URL, "/")
}

// call makes one request of toxiproxy's API; a status not in ok is an
// error carrying its body (toxiproxy says what was wrong).
func (t *Toxiproxy) call(ctx context.Context, method, path string, body any, out any, ok ...int) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.base()+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c := t.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("qnemesis: toxiproxy at %s: %w (is toxiproxy-server running? set Toxiproxy.URL to its API; a call that times out on a running server hangs in toxiproxy: "+
			"it deadlocks removing a latency toxic held up by a closed timeout toxic after it, which qnemesis never stacks: check for toxics added by other means)", t.base(), err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if !slices.Contains(ok, resp.StatusCode) {
		return fmt.Errorf("qnemesis: toxiproxy %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// proxyName names a directed link's proxy.
func proxyName(from, to string) string { return "qnemesis:" + from + "->" + to }

func sorted(a, b string) [2]string {
	if b < a {
		a, b = b, a
	}
	return [2]string{a, b}
}

// Link makes the proxy of the directed link from → to, forwarding to
// upstream (to's real address, host:port), and returns the address from
// must dial instead. A proxy of that name left by an earlier run is
// replaced. Close deletes the proxies Link made.
func (t *Toxiproxy) Link(from, to, upstream string) (addr string, err error) {
	if strings.Contains(from, ".") || strings.Contains(to, ".") {
		return "", fmt.Errorf("qnemesis: link %s -> %s: a name in a fault's kind cannot contain a dot (nemesis.partition.<a>.<b> splits on them): name the endpoint without one", from, to)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	name := proxyName(from, to)
	if err := t.call(ctx, http.MethodDelete, "/proxies/"+url.PathEscape(name), nil, nil, http.StatusNoContent, http.StatusNotFound); err != nil {
		return "", err
	}
	host := t.Listen
	if host == "" {
		host = "127.0.0.1"
	}
	var p struct {
		Listen string `json:"listen"`
	}
	in := map[string]any{"name": name, "listen": host + ":0", "upstream": upstream, "enabled": true}
	if err := t.call(ctx, http.MethodPost, "/proxies", in, &p, http.StatusCreated); err != nil {
		return "", err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.proxies == nil {
		t.proxies = map[[2]string][]string{}
	}
	k := sorted(from, to)
	if !slices.Contains(t.proxies[k], name) {
		t.proxies[k] = append(t.proxies[k], name)
	}
	return p.Listen, nil
}

// of returns the proxies between a and b, or an error naming the fix.
func (t *Toxiproxy) of(a, b string) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ps := t.proxies[sorted(a, b)]
	if len(ps) == 0 {
		return nil, fmt.Errorf("qnemesis: no link between %s and %s: make one with Toxiproxy.Link(%q, %q, upstream) and dial the address it returns", a, b, a, b)
	}
	return slices.Clone(ps), nil
}

func (t *Toxiproxy) enable(ctx context.Context, a, b string, on bool) error {
	ps, err := t.of(a, b)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if err := t.call(ctx, http.MethodPost, "/proxies/"+url.PathEscape(p), map[string]any{"enabled": on}, nil, http.StatusOK); err != nil {
			return err
		}
	}
	return nil
}

// Cut implements Network: the links' proxies disabled.
func (t *Toxiproxy) Cut(ctx context.Context, a, b string) error { return t.enable(ctx, a, b, false) }

// Heal implements Network: the links' proxies enabled, listening on
// the ports they had.
func (t *Toxiproxy) Heal(ctx context.Context, a, b string) error { return t.enable(ctx, a, b, true) }

const (
	latencyToxic = "qnemesis_latency"
	lossToxic    = "qnemesis_loss"
)

// toxic replaces the toxics on every proxy between a and b with the one
// of that name: latency and loss replace each other, never stacked.
// toxiproxy (v2.12, v2.13) deadlocks removing a latency toxic that holds
// a response while the timeout toxic after it has closed its connection
// (the latency toxic's write of the held chunk blocks forever, and the
// removal waits for it holding the proxy's toxics locked): the DELETE
// hangs until the API's own 25 s timeout answers 503, and every later
// call on the proxy hangs too. Under load, a loss's GET left a response
// in the latency sleep when Clear came.
func (t *Toxiproxy) toxic(ctx context.Context, a, b, name, typ string, toxicity float64, attrs map[string]any) error {
	ps, err := t.of(a, b)
	if err != nil {
		return err
	}
	for _, p := range ps {
		path := "/proxies/" + url.PathEscape(p) + "/toxics"
		// both: a link holds one of them at a time
		for _, old := range []string{lossToxic, latencyToxic} {
			if err := t.call(ctx, http.MethodDelete, path+"/"+old, nil, nil, http.StatusNoContent, http.StatusNotFound); err != nil {
				return err
			}
		}
		in := map[string]any{"name": name, "type": typ, "stream": "downstream", "toxicity": toxicity, "attributes": attrs}
		if err := t.call(ctx, http.MethodPost, path, in, nil, http.StatusOK); err != nil {
			return err
		}
	}
	return nil
}

// Latency implements Network: a latency toxic on the responses.
func (t *Toxiproxy) Latency(ctx context.Context, a, b string, d, jitter time.Duration) error {
	return t.toxic(ctx, a, b, latencyToxic, "latency", 1, map[string]any{"latency": d.Milliseconds(), "jitter": jitter.Milliseconds()})
}

// Loss implements Network: a timeout toxic on the responses of a share
// of the connections, which closes each after timeout (at least a
// millisecond: toxiproxy's 0 would hold it open forever, its stream cut
// in the middle).
func (t *Toxiproxy) Loss(ctx context.Context, a, b string, toxicity float64, timeout time.Duration) error {
	return t.toxic(ctx, a, b, lossToxic, "timeout", toxicity, map[string]any{"timeout": max(timeout.Milliseconds(), 1)})
}

// Clear implements Network.
func (t *Toxiproxy) Clear(ctx context.Context, a, b string) error {
	ps, err := t.of(a, b)
	if err != nil {
		return err
	}
	for _, p := range ps {
		for _, name := range []string{lossToxic, latencyToxic} {
			if err := t.call(ctx, http.MethodDelete, "/proxies/"+url.PathEscape(p)+"/toxics/"+name, nil, nil, http.StatusNoContent, http.StatusNotFound); err != nil {
				return err
			}
		}
	}
	return nil
}

// Pairs implements Network.
func (t *Toxiproxy) Pairs() [][2]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][2]string, 0, len(t.proxies))
	for k := range t.proxies {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] || out[i][0] == out[j][0] && out[i][1] < out[j][1] })
	return out
}

// Close deletes the proxies Link made: a t.Cleanup's.
func (t *Toxiproxy) Close() error {
	t.mu.Lock()
	var names []string
	for _, ps := range t.proxies {
		names = append(names, ps...)
	}
	t.proxies = nil
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var first error
	for _, n := range names {
		if err := t.call(ctx, http.MethodDelete, "/proxies/"+url.PathEscape(n), nil, nil, http.StatusNoContent, http.StatusNotFound); err != nil && first == nil {
			first = err
		}
	}
	return first
}
