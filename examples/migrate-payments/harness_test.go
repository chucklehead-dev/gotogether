package payments_test

// The migration's harness: the legacy tests run again with their steps
// recorded, the production log's requests replayed, and the staging
// workload whose spans testdata/spans holds. None of it changes the
// legacy code: the steps come from its audit log (auditsteps) and its
// spans (payments.convert.yaml).

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/convert"
	payments "github.com/chucklehead-dev/gotogether/examples/migrate-payments"
	"github.com/chucklehead-dev/gotogether/examples/migrate-payments/auditsteps"
	"github.com/chucklehead-dev/gotogether/otelio"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const (
	bindingFile   = "payments.binding.yaml"
	rulesFile     = "payments.convert.yaml"
	draftBinding  = "testdata/draft/payments.draft.yaml"
	draftRules    = "testdata/draft/payments.convert.yaml"
	productionLog = "testdata/logs/payments-2026-10-02.jsonl"
	rotatedLog    = "testdata/logs/payments-2026-09-30.jsonl"
	stagingSpans  = "testdata/spans/staging.otlp.json"
)

// legacyTests are the legacy suite (payments_test.go), each run again
// recorded.
var legacyTests = []struct {
	name string
	run  func(*testing.T)
}{
	{"TestAuthorize", TestAuthorize},
	{"TestAuthorizeInvalid", TestAuthorizeInvalid},
	{"TestPartialCapture", TestPartialCapture},
	{"TestCaptureTooMuch", TestCaptureTooMuch},
	{"TestCaptureTwice", TestCaptureTwice},
	{"TestRefund", TestRefund},
	{"TestPartialRefund", TestPartialRefund},
	{"TestRefundTooMuch", TestRefundTooMuch},
	{"TestRefundBeforeCapture", TestRefundBeforeCapture},
	{"TestVoid", TestVoid},
	{"TestVoidAfterCapture", TestVoidAfterCapture},
	{"TestLookupUnknown", TestLookupUnknown},
}

// recorded runs a scenario with the service's audit records as the run's
// steps: the legacy tests build their service with slog.Default(), which
// auditsteps.Handler stands in for while it runs. The handler is called on
// the test's goroutine (httptest.NewRecorder), so each step finds the run
// qtest.Record started there. slog's default is process-wide: these tests
// do not run in parallel.
func recorded(t *testing.T, scenario func(*testing.T)) *qtest.Run {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(auditsteps.Handler(nil)))
	defer slog.SetDefault(prev)
	return qtest.Record(t, bindingFile, func(*qobs.Recorder) { scenario(t) })
}

// legacyRuns are the legacy tests' recorded runs. A legacy test's own
// assertion failing fails t: these runs are of a suite that passes.
func legacyRuns(t *testing.T) []*qtest.Run {
	t.Helper()
	var runs []*qtest.Run
	for _, lt := range legacyTests {
		runs = append(runs, recorded(t, lt.run))
	}
	return runs
}

// withoutReads drops the lookups: the stage-3 draft has no reads, so its
// runs are the others' steps alone.
func withoutReads(steps []qtrace.Step) []qtrace.Step {
	return slices.DeleteFunc(slices.Clone(steps), func(s qtrace.Step) bool { return s.Action == "lookup" })
}

// logSteps reads an audit log's steps.
func logSteps(t *testing.T, path string) []qtrace.Step {
	t.Helper()
	steps, err := auditsteps.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return steps
}

// replay sends the requests of a log's steps to a new service, in order:
// the production log's day, run again on the code as it is.
func replay(steps []qtrace.Step) func(*testing.T) {
	return func(t *testing.T) {
		c := newClient(t)
		for _, s := range steps {
			id, _ := s.Args["id"].(string)
			key, _ := s.Args["key"].(string)
			amount, _ := s.Args["amount"].(int64)
			switch s.Action {
			case "authorize":
				c.do("POST", "/charges", key, amount)
			case "capture", "refund":
				c.do("POST", "/charges/"+id+"/"+s.Action, "", amount)
			case "void":
				c.do("POST", "/charges/"+id+"/void", "", 0)
			case "lookup":
				c.do("GET", "/charges/"+id, "", 0)
			default:
				t.Fatalf("%s: no request for action %s", s.Source, s.Action)
			}
		}
	}
}

// staging is the staging environment's smoke workload, whose spans
// testdata/spans/staging.otlp.json holds: a retried key, a partial
// capture and refund, a void, a refused refund, a lookup of no charge. It
// refunds no charge twice: the legacy bug never ran in staging. It
// asserts nothing (a mutant's answers differ): its spans are its record.
func staging(t *testing.T, tp trace.TracerProvider) {
	c := &client{t, payments.New(payments.Config{Instance: "staging-1", TracerProvider: tp, Logger: slog.New(slog.DiscardHandler)}).Handler()}
	steps := []struct {
		method, path, key string
		amount            int64
	}{
		{"POST", "/charges", "st-1", 1000},
		{"POST", "/charges", "st-1", 1000}, // the client's retry
		{"POST", "/charges/ch_1/capture", "", 600},
		{"POST", "/charges/ch_1/refund", "", 200},
		{"GET", "/charges/ch_1", "", 0},
		{"POST", "/charges", "st-2", 500},
		{"POST", "/charges/ch_2/void", "", 0},
		{"POST", "/charges", "st-3", 800},
		{"POST", "/charges/ch_3/capture", "", 800},
		{"POST", "/charges/ch_3/refund", "", 900},
		{"GET", "/charges/ch_404", "", 0},
	}
	for _, s := range steps {
		c.do(s.method, s.path, s.key, s.amount) // its answers are in its spans
	}
}

// stagingSpansNow runs the staging workload and returns its spans.
func stagingSpansNow(t *testing.T) []otelio.TracesData {
	sr := tracetest.NewSpanRecorder()
	staging(t, sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr), sdktrace.WithResource(stagingResource())))
	return []otelio.TracesData{otelio.SpansToTracesData(sr.Ended())}
}

func stagingResource() *resource.Resource {
	return resource.NewSchemaless(attribute.String("service.name", "payments"), attribute.String("service.instance.id", "staging-1"))
}

// readSpans reads an OTLP/JSON file.
func readSpans(t *testing.T, path string) []otelio.TracesData {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tds, err := otelio.ReadTracesData(f)
	if err != nil {
		t.Fatal(err)
	}
	return tds
}

// converted converts spans by a rule file, and lists the spans no rule
// matched.
func converted(t *testing.T, rules string, tds []otelio.TracesData) ([]qtrace.Step, []convert.Unmatched) {
	t.Helper()
	r, err := convert.Load(rules)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := r.Steps(tds)
	if err != nil {
		t.Fatal(err)
	}
	un, err := r.Unmatched(tds)
	if err != nil {
		t.Fatal(err)
	}
	return steps, un
}

// TestWriteStagingSpans rewrites testdata/spans/staging.otlp.json from the
// staging workload, with UPDATE_STAGING_SPANS=1.
func TestWriteStagingSpans(t *testing.T) {
	if os.Getenv("UPDATE_STAGING_SPANS") == "" {
		t.Skip("UPDATE_STAGING_SPANS=1 rewrites " + stagingSpans)
	}
	f, err := os.Create(stagingSpans)
	if err != nil {
		t.Fatal(err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(otelio.NewFileExporter(f)), sdktrace.WithResource(stagingResource()))
	staging(t, tp)
	if err := tp.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
