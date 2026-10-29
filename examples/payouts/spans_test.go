package payouts

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/payouts/bank"
	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/otelio"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// capture seals the observed prefix atomically in both representations. A killed
// recorder drops its entire tail, not just the completion we intend to test.
type spanCapture struct {
	mu      sync.Mutex
	stopped bool
	native  qobs.MemorySink
	otel    *qobs.OTelSink
}

func (s *spanCapture) Record(ctx context.Context, step *qtrace.Step) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.native.Record(ctx, step)
		s.otel.Record(ctx, step)
	}
}
func (s *spanCapture) stop() { s.mu.Lock(); s.stopped = true; s.mu.Unlock() }

type spanTransport struct {
	next     http.RoundTripper
	tracer   trace.Tracer
	finished chan struct{}
}

func (r spanTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, sp := r.tracer.Start(req.Context(), "peer HTTP client", trace.WithSpanKind(trace.SpanKindClient))
	defer func() { sp.End(); r.finished <- struct{}{} }()
	req = req.Clone(ctx)
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(req.Header))
	return r.next.RoundTrip(req)
}

type spanRun struct {
	native, decoded []qtrace.Step
	data            []byte
	spans           []sdktrace.ReadOnlySpan
}

// realSpanRun drives real processors and HTTP APIs; only the durable stores are
// in-memory. "pending" stops the caller's recorder after the peer commits but
// before its response; "lost" returns one proxy 502 after that commit.
func realSpanRun(t *testing.T, mode string) spanRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(sr))
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	tr := tp.Tracer("payouts-real-tier")
	caller := &spanCapture{otel: qobs.NewOTelSink(tp)}
	peer := &spanCapture{otel: qobs.NewOTelSink(tp)}
	defer caller.stop()
	defer peer.stop()
	callerCtx := qobs.WithRecorder(ctx, qobs.NewRecorder(caller, qobs.WithID("payouts-node")))
	peerRec := qobs.NewRecorder(peer, qobs.WithID("bank-node"))
	st, bst := memstore.New("payouts"), memstore.New("bank")
	for _, tenant := range []string{"t1", "t2"} {
		if err := st.CreateTenant(ctx, tenant); err != nil {
			t.Fatal(err)
		}
		if err := bst.CreateTenant(ctx, tenant); err != nil {
			t.Fatal(err)
		}
	}
	relay, err := grist.NewRelay(st)
	if err != nil {
		t.Fatal(err)
	}
	br, err := grist.NewRelay(bst)
	if err != nil {
		t.Fatal(err)
	}
	br.RenderAs(bank.App)
	api, err := grist.NewHandler(bank.App, bst, grist.CallerFromHeader("X-Caller"))
	if err != nil {
		t.Fatal(err)
	}
	var tries atomic.Int64
	applied := make(chan struct{})
	bankServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sc := propagation.TraceContext{}.Extract(req.Context(), propagation.HeaderCarrier(req.Header))
		sc, sp := tr.Start(sc, "peer HTTP server", trace.WithSpanKind(trace.SpanKindServer))
		defer sp.End()
		sc = qobs.WithRecorder(sc, peerRec)
		out := httptest.NewRecorder()
		api.ServeHTTP(out, req.WithContext(sc))
		if err := br.Flush(sc); err != nil {
			t.Error(err)
		}
		n := tries.Add(1)
		if n == 1 && mode == "pending" {
			close(applied)
			<-ctx.Done()
			return
		}
		if n == 1 && mode == "lost" {
			http.Error(w, "proxy lost committed answer", http.StatusBadGateway)
			return
		}
		for k, v := range out.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(out.Code)
		_, _ = w.Write(out.Body.Bytes())
	}))
	// Cancel requests before closing the server, including an interrupted response.
	defer bankServer.Close()
	defer cancel()
	finished := make(chan struct{}, 32)
	bankClient := &client.Client{App: bank.App, URL: bankServer.URL, CallerHeader: "X-Caller", HTTP: &http.Client{Transport: spanTransport{http.DefaultTransport, tr, finished}}}
	callerCtx = Bank.With(callerCtx, client.Peer(bankClient))
	if err := App.Start(callerCtx, st, grist.WithRelay(relay)); err != nil {
		t.Fatal(err)
	}
	pa, err := grist.NewHandler(App, st, grist.CallerFromHeader("X-Caller"))
	if err != nil {
		t.Fatal(err)
	}
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pa.ServeHTTP(w, r.WithContext(qobs.WithRecorder(r.Context(), qobs.From(callerCtx))))
	}))
	defer ps.Close()
	pc := &client.Client{App: App, URL: ps.URL, CallerHeader: "X-Caller"}
	tenants := []string{"t1", "t2"}
	if mode == "pending" {
		tenants = tenants[:1]
	}
	for _, tenant := range tenants {
		account := "a"
		if mode == "refused" {
			account = "z"
		}
		body, _ := json.Marshal(Request{Payout: "shared", Account: account, Amount: 7})
		if _, err := pc.Command(ctx, grist.Envelope{Tenant: tenant, ID: "shared", Caller: "tester"}, "request", body); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "pending" {
		select {
		case <-applied:
		case <-ctx.Done():
			t.Fatal("peer never applied pending call")
		}
		caller.stop()
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("interrupted HTTP call did not return")
		}
	} else {
		// Checkpoints, rather than elapsed quiet time, witness processing of each
		// requested trigger. No settle or telemetry completeness is inferred.
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			done := true
			for _, tenant := range tenants {
				cp, err := st.Checkpoint(ctx, tenant, "send")
				if err != nil {
					t.Fatal(err)
				}
				if cp < 1 {
					done = false
				}
			}
			if done {
				break
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal("processor did not checkpoint: ", ctx.Err())
			}
		}
		if err := relay.Flush(callerCtx); err != nil {
			t.Fatal(err)
		}
		caller.stop()
		peer.stop()
		cancel()
	}
	peer.stop()
	bankServer.Close() // wait for the interrupted server span to end
	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	native := append(caller.native.Steps(), peer.native.Steps()...)
	spans := sr.Ended()
	data, err := json.Marshal(otelio.SpansToTracesData(spans))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := otelio.ReadSteps(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return spanRun{native, decoded, data, spans}
}

// semanticRecords compares the full native schema, exempting only the decoder's
// source location and nil/empty map allocation. JSON normalizes Go ints to
// the wire's numeric representation.
func semanticRecords(t *testing.T, steps []qtrace.Step) string {
	t.Helper()
	steps = slices.Clone(steps)
	for i := range steps {
		steps[i].Source = ""
		if steps[i].Args == nil {
			steps[i].Args = map[string]any{}
		}
		if steps[i].Obs == nil {
			steps[i].Obs = map[string]any{}
		}
		if steps[i].Order == nil {
			steps[i].Order = map[string]int64{}
		}
	}
	slices.SortFunc(steps, func(a, b qtrace.Step) int {
		if n := strings.Compare(a.Recorder, b.Recorder); n != 0 {
			return n
		}
		if a.Seq < b.Seq {
			return -1
		}
		if a.Seq > b.Seq {
			return 1
		}
		return 0
	})
	b, err := json.Marshal(steps)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func checkSpanParity(t *testing.T, r spanRun) {
	t.Helper()
	if len(r.decoded) == 0 || semanticRecords(t, r.native) != semanticRecords(t, r.decoded) {
		t.Fatalf("OTLP changed native semantic records\nnative %s\ndecoded %s", semanticRecords(t, r.native), semanticRecords(t, r.decoded))
	}
	for _, s := range r.decoded {
		if !strings.HasPrefix(s.Source, "span ") {
			t.Fatalf("missing span source: %s", s.Source)
		}
	}
	reordered := slices.Clone(r.spans)
	slices.Reverse(reordered)
	data, err := json.Marshal(otelio.SpansToTracesData(reordered))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := otelio.ReadSteps(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if semanticRecords(t, decoded) != semanticRecords(t, r.decoded) {
		t.Fatal("export order changed records")
	}
	// Every HTTP server span has the propagated client as its actual parent.
	clients := map[trace.SpanID]trace.TraceID{}
	servers := 0
	for _, s := range r.spans {
		if s.Name() == "peer HTTP client" {
			clients[s.SpanContext().SpanID()] = s.SpanContext().TraceID()
		}
	}
	for _, s := range r.spans {
		if s.Name() == "peer HTTP server" {
			servers++
			if clients[s.Parent().SpanID()] != s.SpanContext().TraceID() {
				t.Fatal("HTTP context was not propagated")
			}
		}
	}
	if servers == 0 {
		t.Fatal("no real HTTP server spans")
	}
	for _, steps := range [][]qtrace.Step{r.native, r.decoded, decoded} {
		gristtest.Linked(t, App, bank.App, steps)
		projected, err := gristtest.ProtocolSteps(steps, App, bank.App)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := gristtest.ProtocolSteps(r.native, App, bank.App)
		if err != nil {
			t.Fatal(err)
		}
		if semanticRecords(t, projected) != semanticRecords(t, expected) {
			t.Fatal("OTLP changed causal projection")
		}
	}
}
func saveSpanRun(t *testing.T, name string, r spanRun) {
	t.Helper()
	dir := os.Getenv("GOTOGETHER_SPANS_OUT")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".otlp.json"), r.data, 0644); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := qtrace.WriteJSONL(&b, r.decoded); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), b.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}
func TestDeclaredCallSpans(t *testing.T) {
	qtest.RequireQuint(t)
	for _, mode := range []string{"success", "refused", "lost", "pending"} {
		t.Run(mode, func(t *testing.T) {
			r := realSpanRun(t, mode)
			checkSpanParity(t, r)
			saveSpanRun(t, mode, r)
			qtest.Validate(t, commits, r.decoded).Expect(t, qtest.Conforms(), qtest.Not(qtest.Settles()))
			qtest.Validate(t, atBank, r.decoded).Expect(t, qtest.Conforms(), qtest.Not(qtest.Settles()))
			outcomes := map[string]int{}
			invocations := 0
			bankPays := map[string]int{}
			for _, s := range r.decoded {
				if s.Action == "bankPay" {
					if s.Invocation {
						invocations++
					} else {
						outcomes[s.Outcome]++
					}
				}
				if strings.HasPrefix(s.Actor, "bank/") && s.Action == "pay" && s.Outcome == "ok" {
					bankPays[s.Actor]++
				}
			}
			if mode == "pending" {
				trs, issues := qtrace.Reconstruct(r.decoded)
				for _, i := range issues {
					if i.Severity == "error" {
						t.Fatal(i)
					}
				}
				n := 0
				for _, tr := range trs {
					n += len(tr.Pending)
				}
				if n != 1 || outcomes["unknown"] != 0 || bankPays["bank/t1"] != 1 {
					t.Fatalf("pending evidence: %d, outcomes %v, bank pays %v", n, outcomes, bankPays)
				}
			} else if mode == "refused" {
				if outcomes["unknown-account"] < 2 || len(outcomes) != 1 {
					t.Fatalf("refusals: %v", outcomes)
				}
			} else if mode == "lost" {
				if outcomes["unknown"] != 1 || outcomes["ok"] != 2 || invocations != 3 || bankPays["bank/t1"] != 1 || bankPays["bank/t2"] != 1 {
					t.Fatalf("lost reply/retry: %v, attempts %d, payments %v", outcomes, invocations, bankPays)
				}
			} else if outcomes["ok"] != 2 || bankPays["bank/t1"] != 1 || bankPays["bank/t2"] != 1 {
				t.Fatalf("success: %v", outcomes)
			}
		})
	}
}

// These are schema-valid corruptions of actual exported records, not invented
// model answers. Their native counterpart is faithful in TestDeclaredCallSpans.
func TestSpanIdentityCorruption(t *testing.T) {
	qtest.RequireQuint(t)
	r := realSpanRun(t, "success")
	for _, field := range []string{"tenant", "key", "trigger", "command", "outcome"} {
		t.Run(field, func(t *testing.T) {
			steps := slices.Clone(r.decoded)
			for i, s := range steps {
				if s.Action != "bankPay" {
					continue
				}
				steps[i].Args = maps.Clone(s.Args)
				switch field {
				case "tenant":
					steps[i].Actor = "payouts/foreign"
				case "key":
					steps[i].Args["key"] = "payouts/send/wrong/999"
				case "trigger":
					steps[i].Args["trigger"] = int64(999)
				case "command":
					steps[i].Action = "wrongCommand"
				case "outcome":
					if !s.Invocation {
						steps[i].Outcome = "unknown-account"
					}
				}
			}
			if field == "trigger" {
				if _, err := gristtest.ProtocolSteps(steps, App, bank.App); err == nil {
					t.Fatal("missing trigger accepted")
				}
			} else if len(gristtest.Links(App, bank.App, steps)) == 0 {
				t.Fatal("identity corruption accepted")
			}
		})
	}
	// A missing model parameter must refuse validation, not turn into amount=0.
	missing := slices.Clone(r.decoded)
	for i, s := range missing {
		if s.Action == "bankPay" {
			missing[i].Args = maps.Clone(s.Args)
			delete(missing[i].Args, "amount")
		}
	}
	// Validate's malformed-input behavior is asserted separately from semantic
	// failures below; no mutation-score credit is taken for missing parameters.
	checker, err := validate.NewChecker(commits, validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	if _, err := checker.Check(t.Context(), missing); err == nil || !strings.Contains(err.Error(), "amount") {
		t.Fatalf("missing amount should refuse before verdict: %v", err)
	}
	// Dense sequence gaps are a reconstruction error, even though a lost entire
	// tail cannot be detected without independent collection evidence.
	for i, s := range r.decoded {
		if s.Invocation && s.Action == "bankPay" {
			gap := append(slices.Clone(r.decoded[:i]), r.decoded[i+1:]...)
			traces, issues := qtrace.Reconstruct(gap)
			for _, tr := range traces {
				issues = append(issues, tr.Issues...)
			}
			found := false
			for _, is := range issues {
				if is.Severity == "error" {
					found = true
				}
			}
			if !found {
				t.Fatal("interior loss was invisible")
			}
			break
		}
	}
}

func TestSpanMutant(t *testing.T) {
	if qtest.Mutant() != "freshKey" {
		t.Skip("TestSpanMutants runs the generated freshKey mutant")
	}
	r := realSpanRun(t, "lost")
	saveSpanRun(t, "freshKey", r)
	qtest.Validate(t, commits, r.decoded).Expect(t, qtest.Conforms())
	qtest.Validate(t, atBank, r.decoded).Expect(t, qtest.Violates("onePaymentPerPayout"))
	if len(gristtest.Links(App, bank.App, r.decoded)) == 0 {
		t.Fatal("freshKey escaped exported link checks")
	}
}
func TestSpanMutants(t *testing.T) {
	qtest.Mutants{Dir: t.TempDir(), Test: "TestSpanMutant", Generated: gristtest.PeerMutants(App)}.Run(t)
}
