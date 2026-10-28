package grist

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
)

// An Identity is where a node runs, read once at start (IdentityFromEnv)
// and named in its recorder's id: <app>/<node>/<incarnation>. Every field
// is optional. A process id names no node: it is 1 in every container,
// so two pods, or a restarted container and its earlier self, recorded
// under one id, and their merged step files numbered one recorder twice
// (qtrace.Reconstruct's "two different records", and no verdict).
type Identity struct {
	// Node is the node's stable name: GRIST_NODE, else POD_NAME (the
	// downward API's metadata.name), else the host name.
	Node string
	// Incarnation is a random nonce per start: a restarted container keeps
	// its pod name, and its steps must not be numbered as its earlier
	// self's.
	Incarnation string
	// Namespace is POD_NAMESPACE.
	Namespace string
	// Host is NODE_NAME (the Kubernetes node, spec.nodeName: a failure
	// domain).
	Host string
	// Zone is GRIST_ZONE (the downward API cannot read a node's labels: a
	// manifest passes it, or it is "").
	Zone string
}

// IdentityFromEnv is the process's Identity: its node from GRIST_NODE,
// POD_NAME or the host name ("node" when none is known), a fresh
// Incarnation, and the other fields from their variables.
func IdentityFromEnv() Identity {
	id := Identity{Node: firstEnv("GRIST_NODE", "POD_NAME"), Namespace: os.Getenv("POD_NAMESPACE"),
		Host: os.Getenv("NODE_NAME"), Zone: os.Getenv("GRIST_ZONE"), Incarnation: newIncarnation()}
	if id.Node == "" {
		id.Node, _ = os.Hostname()
	}
	if id.Node == "" {
		id.Node = "node"
	}
	return id
}

// WithIdentity makes id Main's node identity in place of the
// environment's (tests, and orchestrators other than Kubernetes); its
// empty fields are IdentityFromEnv's.
func WithIdentity(id Identity) ServeOption {
	return ServeOption{apply: func(c *serveConfig) { c.identity = &id }}
}

// resolve is id with its empty fields from env.
func (id Identity) resolve(env Identity) Identity {
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&id.Node, env.Node}, {&id.Incarnation, env.Incarnation}, {&id.Namespace, env.Namespace},
		{&id.Host, env.Host}, {&id.Zone, env.Zone},
	} {
		if *f.dst == "" {
			*f.dst = f.src
		}
	}
	return id
}

// recorderID is the id of app's node's recorder: <app>/<node>/<incarnation>.
func (id Identity) recorderID(app string) string {
	return app + "/" + id.Node + "/" + id.Incarnation
}

// String is the identity as Main's startup line prints it.
func (id Identity) String() string {
	parts := []string{"node " + id.Node, "incarnation " + id.Incarnation}
	for _, f := range [][2]string{{"namespace", id.Namespace}, {"host", id.Host}, {"zone", id.Zone}} {
		if f[1] != "" {
			parts = append(parts, f[0]+" "+f[1])
		}
	}
	return strings.Join(parts, ", ")
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// newIncarnation is 12 random hex digits.
func newIncarnation() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// onOrchestrator reports whether the process runs under Kubernetes
// (KUBERNETES_SERVICE_HOST, set in every pod): the health port and the
// drain's delay are on by default there.
func onOrchestrator() bool { return os.Getenv("KUBERNETES_SERVICE_HOST") != "" }
