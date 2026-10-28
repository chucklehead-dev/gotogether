package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/template"

	"github.com/chucklehead-dev/gotogether/binding"
)

// systemTest is a qtest.System test for the binding: a Run that deploys
// nothing yet, one commented qobs.Record per bound or modelled action with
// the fields the binding reads, a conformance test that fails until steps
// are recorded, a test of what the runs exercised, and the overlay
// mutants' harness with a claim to fill in. With http, Run deploys an
// HTTP service on qenv/qhttp: its server's process first (a client that
// starts before its server has every request refused until it listens),
// a handler where the steps are recorded, and a client whose requests
// retry a refused connection.
func systemTest(pkg, bpath string, b *binding.Binding, m *binding.Model, http bool) string {
	ctx := "ctx"
	indent := "\t\t\t"
	if http {
		ctx, indent = "r.Context()", "\t\t"
	}
	var rec strings.Builder
	for _, a := range m.ActionNames() {
		if _, bound := b.Actions[a]; a == "init" || a == "step" || a == b.Restart || m.Helpers[a] && !bound {
			continue
		}
		// the parameters' fields in the signature's order, then the rest
		// (observations) as Fields sorts them
		var fields []string
		rest := map[string]bool{}
		for _, f := range b.Actions[a].Fields(m.Actions[a]...) {
			rest[f] = true
		}
		for _, p := range m.Actions[a] {
			if rest[p.Name] {
				fields = append(fields, p.Name)
				delete(rest, p.Name)
			}
		}
		fields = append(fields, slices.Sorted(maps.Keys(rest))...)
		kv := ""
		for _, f := range fields {
			v := f
			if o, ok := strings.CutPrefix(f, "obs."); ok {
				v = "observed" + strings.ToUpper(o[:1]) + o[1:]
			}
			kv += fmt.Sprintf(", %q, %s", f, v)
		}
		fmt.Fprintf(&rec, "%s// qobs.Record(%s, %q, err%s) // %s\n", indent, ctx, a, kv, m.Signature(a))
	}
	claim := "qtest.Some(qtest.Rejects(\"ACTION\"))"
	if len(b.Invariants) > 0 {
		claim = fmt.Sprintf("qtest.Some(qtest.Violates(%q))", b.Invariants[0])
	}
	// what the faithful runs must show; what they exercised (every action
	// taken and fault faulted) is a test of its own, so that the first
	// green is conformance, and a fault no seed reached is named on its
	// own, with the line that excepts it
	faithful := "qtest.Conforms()"
	for _, inv := range b.Invariants {
		if b.Antecedents[inv] != "" || m.Antecedents[inv] != "" {
			faithful += ", qtest.NotVacuous()"
			break
		}
	}
	// a binding with eventual vals has them judged only after a settle
	end := "\t\treturn s.Run(time.Minute) // a task waiting forever is an error (qenv.ErrStuck)\n"
	if len(b.Eventually) > 0 {
		end = "\t\tif err := s.Run(time.Minute); err != nil {\n\t\t\treturn err // a task waiting forever is an error (qenv.ErrStuck)\n\t\t}\n" +
			"\t\t// faults off until quiet, then a settle in the trace: the binding's\n\t\t// eventual vals (" + strings.Join(b.Eventually, ", ") +
			") are judged then; an *Unsettled is the run's outcome\n\t\treturn s.Settle(time.Minute)\n"
		faithful += ", qtest.All(qtest.Settles())"
	}
	var out strings.Builder
	err := systemTemplate.Execute(&out, map[string]any{
		"Pkg": pkg, "Binding": bpath, "Records": rec.String(), "Claim": claim, "Faithful": faithful, "End": end,
		"HTTP": http, "Server": serverName(b.Module),
	})
	if err != nil {
		panic(err) // the template's own bug
	}
	return out.String()
}

// serverName is the simulated server's name for a model's module: its
// host in URLs, lower-case.
func serverName(module string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(module) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "service"
	}
	return b.String()
}

var systemTemplate = template.Must(template.New("system").Parse(`package {{.Pkg}}

import (
	"context"
{{- if .HTTP}}
	"errors"
	"net/http"
	"syscall"
{{- end}}
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
{{- if .HTTP}}
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
{{- end}}
	"github.com/chucklehead-dev/gotogether/qtest"
)

// system deploys the application in a simulation made from c. Every
// workflow runs it: Explore (seeds), qhegel.Shrink (Hegel), Sweep, Fuzz,
// Mutate. In a mutant's binary (TestMutants) it names the mutant: its
// runs are not the application's, and a suite's evidence leaves them out.
func system() qtest.System {
	return qtest.System{Binding: {{printf "%q" .Binding}}, Mutant: qtest.Mutant(), Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
{{- if .HTTP}}
		// The service's state that must outlive its process (a database:
		// a qenv.Store, or one of your own made here, once) goes here.
		// Then its process, before the client: a client that starts first
		// has its requests refused until the server has called Handle.
		// The network loses, duplicates and delays requests and loses
		// responses, and the process crashes and restarts (true): every
		// fault a choice that seeds explore and Hegel shrinks.
		var srv *qhttp.Server
		proc := s.Process({{printf "%q" .Server}}, true, func(ctx context.Context) {
			srv.Handle(ctx, handler()) // the service's main, at every incarnation
		})
		srv = qhttp.NewServer(s, {{printf "%q" .Server}}, proc)
		s.Spawn("client", func(ctx context.Context) {
			tr := qhttp.NewTransport(srv)
			tr.Refuse = true // the network refuses some connections too: send retries them
			client := &http.Client{Transport: tr, Timeout: time.Second}
			// Drive the service: the requests a client sends, each through
			// send. Its errors are the run's: the handler records what took
			// effect.
			send(ctx, client, http.MethodPost, srv.URL()+"/")
		})
{{- else}}
		// Deploy the application here: its services (qenv.NewService),
		// stores (qenv.NewStore), queues (qenv.NewQueue) and crashable
		// processes (s.Process), its code written against those seams and
		// qenv.Go, qenv.Now, qenv.Sleep. Record each model step where it
		// takes effect, with the fields the binding reads:
		s.Spawn("client", func(ctx context.Context) {
{{.Records}}		})
{{- end}}
{{.End}}	}}
}
{{- if .HTTP}}

// handler is the service's http.Handler (your own: replace this one).
// Record each model step where it takes effect, with the fields the
// binding reads, through the request's context (qhttp runs the handler
// as a task of the simulation, so the step is in the run):
func handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
{{.Records}}		http.NotFound(w, r)
	})
}

// send sends a request, retrying a refused connection after a backoff:
// nothing was sent, so the retry cannot do the request twice. Any other
// error is returned: a request that timed out or lost its response may
// have taken effect.
func send(ctx context.Context, c *http.Client, method, url string) (*http.Response, error) {
	backoff := 50 * time.Millisecond
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.Do(req)
		if errors.Is(err, syscall.ECONNREFUSED) && attempt < 5 {
			qenv.Sleep(ctx, backoff) // virtual time
			backoff *= 2
			continue
		}
		if err == nil {
			resp.Body.Close()
		}
		return resp, err
	}
}
{{- end}}

// The faithful application conforms through every seed's faults.
func TestConforms(t *testing.T) {
	qtest.RequireQuint(t)
	// until Run records steps, the exploration fails saying so
	system().Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, {{.Faithful}})
}

// The runs exercise what the model and the environment offer: every
// action taken, every fault option injected. A check of an action never
// taken or a fault never injected says nothing of it: a failure here
// names what no seed reached, and the claim that excepts it.
func TestExercised(t *testing.T) {
	qtest.RequireQuint(t)
	system().Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Exercised())
}

// TestMutant states what catches the mutant this binary was built with,
// and how: say which check fails (Rejects, Mismatch, Violates; over runs
// Some, All, None). A check no mutant can fail proves nothing.
func TestMutant(t *testing.T) {
	switch qtest.Mutant() {
	case "":
		t.Skip("the application as it is: TestMutants runs this test in each mutant's binary")
	default: // a case per mutant, named as its directory
		qtest.RequireQuint(t)
		system().Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, {{.Claim}})
	}
}

// A mutant is a directory of testdata/mutants whose files replace the
// package's of the same names: a copy of a file, the bug planted in it
// by hand. TestMutants finds each, builds the tests with its overlay and
// runs TestMutant there (qtest.Mutant() naming it).
func TestMutants(t *testing.T) {
	qtest.Optional(t, "scaffold: plant a bug as testdata/mutants/<name>/<file>.go (a copy of a file of the package, the bug in it), state how it shows in TestMutant, and remove this line")
	qtest.Mutants{}.Run(t)
}
`))
