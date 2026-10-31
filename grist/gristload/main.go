package gristload

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Main is the entry point of an app's sizing binary, cmd/<app>-sizing:
// func main() { gristload.Main(app) }. `<app> sizing ...` runs it by name
// (grist.Main), and so may an operator, with its flags (`<app> sizing -h`). It exits
// 2 on a usage error, 1 on any other.
func Main(app *grist.App) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := Run(ctx, app, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	var bad *UsageError
	switch {
	case errors.As(err, &bad):
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// A UsageError is a command line sizing cannot run.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// usage is the sizing command's flags.
const usage = `usage: <app> sizing -steps FILES -rate R -burst FxD [flags]
  -steps PATH          a run's step files (<app> serve -steps; repeatable, or a directory of *.jsonl): the service times
  -rate R              new client calls per second, all tenants (40 or 40/s)
  -burst FxD           the burst to survive: F times the rate for D (3x30s)
  -out PATH            where to write (default deploy/sizing.yaml; its evidence in deploy/sizing/)
  -within D            recovery bound after the burst (default 60s); -prob P its probability (default 0.99)
  -timeout D           the per-try timeout in force (default 2s)
  -gateway-post N      a Gateway's tries per client attempt on commands, in force (default 1: none)
  -gateway-get N       on queries (default 1)
  -client-attempts N   grist/client's MaxAttempts in force (default 50); -budget RATIO/MAX its Budget (default 0.2/10)
  -hot SHARE           the busiest tenant's share of the rate (default: the recording's)
  -concurrency N       requests a pod serves at once (default 10); -max-replicas N (default 8); -zones N (default 3)
  -cpu D               CPU per request, from a real tier's profile (0: not sized); -heap BYTES peak heap (0: not sized)
  -endpoint-lag D      the cluster's endpoint lag (default 5s); -max-connections N, -reserve N the store's
  -slow F              how many times slower a dependency gets in the processors' stressor (default 10)
  -seeds N             simulated seeds per confirmation (default 6; -1: no recovery confirmations; -calibrate still simulates)
  -calibrate           fit the chain's timeout to simulated trajectories first (slower)`

// Run is sizing's command line on args (Main's, after the binary's
// name): Report on the steps, written with Write; the settings on
// stdout as text.
func Run(ctx context.Context, app *grist.App, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet(app.Name+" sizing", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var steps multi
	fs.Var(&steps, "steps", "")
	rate := fs.String("rate", "", "")
	burst := fs.String("burst", "", "")
	out := fs.String("out", filepath.Join("deploy", "sizing.yaml"), "")
	within := fs.Duration("within", 0, "")
	prob := fs.Float64("prob", 0, "")
	timeout := fs.Duration("timeout", 0, "")
	post := fs.Int("gateway-post", 1, "")
	get := fs.Int("gateway-get", 1, "")
	att := fs.Int("client-attempts", 0, "")
	bud := fs.String("budget", "", "")
	hot := fs.Float64("hot", 0, "")
	conc := fs.Int("concurrency", 0, "")
	maxRep := fs.Int("max-replicas", 0, "")
	zones := fs.Int("zones", 0, "")
	cpu := fs.Duration("cpu", 0, "")
	heap := fs.Int64("heap", 0, "")
	lag := fs.Duration("endpoint-lag", 0, "")
	maxConn := fs.Int("max-connections", 0, "")
	reserve := fs.Int("reserve", 0, "")
	seeds := fs.Int("seeds", 0, "")
	slow := fs.Float64("slow", 0, "")
	calibrate := fs.Bool("calibrate", false, "")
	if err := fs.Parse(args); err != nil {
		return &UsageError{Msg: fmt.Sprintf("%s sizing: %v\n%s", app.Name, err, usage)}
	}
	if len(steps) == 0 || *rate == "" || *burst == "" {
		return &UsageError{Msg: fmt.Sprintf("%s sizing: give -steps, -rate and -burst\n%s", app.Name, usage)}
	}
	l := Load{Within: *within, Prob: *prob, Timeout: *timeout, Gateway: Gateway{Commands: *post, Queries: *get}, HotShare: *hot,
		Concurrency: *conc, MaxReplicas: *maxRep, Zones: *zones, CPU: *cpu, Heap: *heap, EndpointLag: *lag,
		MaxConnections: *maxConn, Reserve: *reserve, Seeds: *seeds, Slow: *slow, Calibrate: *calibrate,
		Client: &client.Client{MaxAttempts: *att}}
	var err error
	if l.Rate, err = ParseRate(*rate); err != nil {
		return &UsageError{Msg: fmt.Sprintf("%s sizing: -rate: %v", app.Name, err)}
	}
	if l.Burst, err = ParseBurst(*burst); err != nil {
		return &UsageError{Msg: fmt.Sprintf("%s sizing: -burst: %v", app.Name, err)}
	}
	if *bud != "" {
		r, m, ok := strings.Cut(*bud, "/")
		ratio, e1 := strconv.ParseFloat(r, 64)
		most, e2 := strconv.ParseFloat(m, 64)
		if !ok || e1 != nil || e2 != nil {
			return &UsageError{Msg: fmt.Sprintf("%s sizing: -budget %q: give RATIO/MAX (0.2/10)", app.Name, *bud)}
		}
		l.Client.Budget = &client.Budget{Ratio: ratio, Max: most}
	}
	all, err := ReadSteps(steps...)
	if err != nil {
		return err
	}
	s, err := Report(app, all, l)
	if err != nil {
		return err
	}
	if err := s.Write(*out, app.Name+" sizing "+strings.Join(withoutOut(args), " ")); err != nil {
		return err
	}
	fmt.Fprint(stdout, s.Text())
	fmt.Fprintf(stderr, "%s sizing: wrote %s\n", app.Name, *out)
	return ctx.Err()
}

// withoutOut is args without -out and its path: the header names what
// produced the values, not where they were written.
func withoutOut(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-out" || a == "--out":
			i++
		case strings.HasPrefix(a, "-out=") || strings.HasPrefix(a, "--out="):
		default:
			out = append(out, a)
		}
	}
	return out
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// ReadSteps reads step files (qtrace.ReadJSONL), a directory's *.jsonl
// among them. A torn last line is kept to before it (the run was
// killed), not an error.
func ReadSteps(paths ...string) ([]qtrace.Step, error) {
	var files []string
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if fi.IsDir() {
			m, _ := filepath.Glob(filepath.Join(p, "*.jsonl"))
			files = append(files, m...)
			continue
		}
		files = append(files, p)
	}
	var all []qtrace.Step
	for _, f := range files {
		r, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		steps, err := qtrace.ReadJSONL(r)
		r.Close()
		var torn *qtrace.TornTail
		if err != nil && !errors.As(err, &torn) {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		all = append(all, steps...)
	}
	return all, nil
}

// ParseRate reads a rate: 40, 40/s, 2400/m.
func ParseRate(s string) (float64, error) {
	num, unit, _ := strings.Cut(s, "/")
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("%q: give a positive rate (40/s)", s)
	}
	switch unit {
	case "", "s":
		return v, nil
	case "m", "min":
		return v / 60, nil
	case "h":
		return v / 3600, nil
	}
	return 0, fmt.Errorf("%q: per s, m or h", s)
}

// ParseBurst reads a burst: 3x30s (three times the rate for 30 s).
func ParseBurst(s string) (Burst, error) {
	f, d, ok := strings.Cut(s, "x")
	factor, err := strconv.ParseFloat(f, 64)
	dur, err2 := time.ParseDuration(d)
	if !ok || err != nil || err2 != nil || factor <= 1 || dur <= 0 {
		return Burst{}, fmt.Errorf("%q: give FACTORxDURATION, a factor above 1 (3x30s)", s)
	}
	return Burst{Factor: factor, For: dur}, nil
}
