package qlint_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qlint"
)

// Lint finds what escapes a simulation, in files that import qenv (tests
// too: a System's Run closure lives in one), and leaves the rest (and
// lines marked //qenv:ok) alone.
func TestLint(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("sim.go", `package app

import (
	"context"
	clock "time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

func run(s *qenv.Sim) {
	go func() {}()
	_ = clock.Now()
	clock.Sleep(clock.Second)
	qobs.Record(context.Background(), "settle", nil)
	qobs.Record(s.Context(), "settle", nil)
	_ = clock.Now() //qenv:ok the real server's clock
	_ = clock.Duration(1)
}
`)
	write("server.go", `package app

import "time"

func serve() { go func() {}(); _ = time.Now() }
`)
	write("sim_test.go", `package app

import (
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

var _ = qenv.Nominal{}
var t0 = time.Now()
var t1 = time.Now() //qenv:ok measures the real cost
`)
	fs, err := qlint.Sim(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		got = append(got, f.Pos[strings.LastIndex(f.Pos, "/")+1:]+" "+f.What)
	}
	want := []string{
		"sim.go:12:2 goroutine started with `go`, outside the simulation",
		"sim.go:13:6 time.Now reads the wall clock, which does not move in a simulation",
		"sim.go:14:2 time.Sleep reads the wall clock, which does not move in a simulation",
		"sim.go:15:2 step recorded with context.Background()",
		"sim_test.go:10:10 time.Now reads the wall clock, which does not move in a simulation",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// LintWoven finds go statements and lock method values that weaving
// cannot rewrite.
func TestLintWoven(t *testing.T) {
	dir := t.TempDir()
	src := `package app

import "sync"

type st struct{ mu sync.RWMutex }

type guarded struct {
	sync.Mutex
	inner struct{ *sync.WaitGroup }
}

func (s *st) acquire() func() {
	s.mu.RLock()
	go func() {}()
	//qenv:go
	go func() {}()
	go func() {}() //qenv:ok
	defer func() { s.mu.RUnlock() }()
	return s.mu.RUnlock
}
`
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := qlint.Woven(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		got = append(got, f.Pos[strings.LastIndex(f.Pos, "/")+1:]+" "+f.What)
	}
	want := []string{
		"app.go:8:2 embedded sync.Mutex: calls promoted through the struct are not woven (weaving matches the receiver's type)",
		"app.go:9:16 embedded sync.WaitGroup: calls promoted through the struct are not woven (weaving matches the receiver's type)",
		"app.go:14:2 go statement not marked //qenv:go: its goroutine runs outside the simulation",
		"app.go:19:9 method value RUnlock is not woven (weaving rewrites calls)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Lint finds math/rand's (and crypto/rand's) package functions and a
// select of two or more cases in simulation code: neither replays. A
// generator made from a seed, and a select of one case and a default,
// are left alone.
func TestLintRandAndSelect(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sim.go"), []byte(`package app

import (
	crand "crypto/rand"
	"math/rand/v2"

	"github.com/chucklehead-dev/gotogether/qenv"
)

func run(s *qenv.Sim, a, b chan int) {
	_ = rand.IntN(3)
	g := rand.New(rand.NewPCG(1, 2))
	_ = g.IntN(3)
	_, _ = crand.Read(nil)
	select {
	case <-a:
	case <-b:
	}
	select {
	case <-a:
	default:
	}
	select { //qenv:ok only a can be ready here
	case <-a:
	case <-b:
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := qlint.Sim(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		got = append(got, f.Pos[strings.LastIndex(f.Pos, "/")+1:]+" "+f.What)
	}
	want := []string{
		"sim.go:11:6 math/rand/v2.IntN draws from a source seeded at random, so a run does not replay",
		"sim.go:14:9 crypto/rand.Read draws from a source seeded at random, so a run does not replay",
		"sim.go:15:2 select with 2 cases: when several are ready Go picks one at random, which no run replays",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The wall clock's and the random packages' functions are found as
// values too, where a call of the value escapes later; a type of the
// random package (rand.Rand) and a seeded generator's methods are not.
func TestLintFunctionValues(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sim.go"), []byte(`package app

import (
	"math/rand/v2"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

type clock struct{ now func() time.Time }

func run(s *qenv.Sim) {
	now := time.Now
	c := clock{now: time.Now}
	draw := rand.IntN
	var g *rand.Rand = rand.New(rand.NewPCG(1, 2))
	pick := g.IntN
	_, _, _, _ = now, c, draw, pick
	after := time.After //qenv:ok a real timeout beside the simulation
	_ = after
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := qlint.Sim(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		got = append(got, f.Pos[strings.LastIndex(f.Pos, "/")+1:]+" "+f.What)
	}
	want := []string{
		"sim.go:13:9 time.Now used as a value: a call of it reads the wall clock, which does not move in a simulation",
		"sim.go:14:18 time.Now used as a value: a call of it reads the wall clock, which does not move in a simulation",
		"sim.go:15:10 math/rand/v2.IntN used as a value: a call of it draws from a source seeded at random, so a run does not replay",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A grist application's code is checked: its non-test files importing
// grist (decisions, folds, handlers and effects gristsim runs in a
// simulation), each finding naming grist's fix (an event's Meta.Time, a
// command's arguments, a processor); its tests, which drive a real tier
// or gristsim, are not, unless they import qenv.
func TestLintGristApp(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("processors.go", `package shop

import (
	"math/rand"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
)

var App = grist.New("shop")

func stamp() int64 {
	go func() {}()
	if rand.Intn(2) == 0 {
		return 0
	}
	return time.Now().UnixMilli()
}

func ok() time.Time { return time.Now() } //qenv:ok a log line's time
`)
	write("real_test.go", `package shop

import "time"

import "github.com/chucklehead-dev/gotogether/grist"

var _ = grist.New
var started = time.Now()
`)
	fs, err := qlint.Sim(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		got = append(got, filepath.Base(f.Pos)+" "+f.What+": "+f.Fix)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"processors.go:13:2 goroutine started with `go` in a grist application",
		"make it a processor (grist.NewProcessor)",
		"processors.go:14:5 math/rand.Intn draws from a source seeded at random in a grist application",
		"take the value from the command's arguments",
		"processors.go:17:9 time.Now reads the wall clock in a grist application",
		"use the event's Meta.Time",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("findings lack %q:\n%s", want, all)
		}
	}
	if len(fs) != 3 {
		t.Errorf("%d findings, want 3 (the test file and the //qenv:ok line are not):\n%s", len(fs), all)
	}
}
