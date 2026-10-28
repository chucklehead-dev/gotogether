// Package session runs an exploration's run navigation session: the
// test binary of the run's test, compiled once, answering
// validate.RunRequest lines with validate.RunResponse lines
// (GOTOGETHER_NAV=serve, DESIGN §58), so that each node is a replay
// rather than a go test process.
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/internal/overlay"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A Session is a running navigation session of one run.
type Session struct {
	// Info is the session's first line: the run, its choices, its notes
	Info validate.RunSession

	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Scanner
	dir  string
	pipe io.Closer  // the responses' read end, where they have their own
	mu   sync.Mutex // one request at a time
	logs *lockedBuffer
}

// Test is the test a run's reference names ("TestX/sub#2:seed=17" names
// TestX/sub), and the -run pattern that runs it alone.
func Test(ref string) (name, pattern string, err error) {
	colon := strings.LastIndex(ref, ":")
	if colon < 0 {
		return "", "", fmt.Errorf("%q names no exploration's run: want 'TestX#2:seed=17', as a failing run's report gives it", ref)
	}
	hash := strings.LastIndex(ref[:colon], "#")
	if hash <= 0 {
		return "", "", fmt.Errorf("%q names no exploration's run: want 'TestX#2:seed=17', as a failing run's report gives it", ref)
	}
	name = ref[:hash]
	return name, "^" + strings.ReplaceAll(regexp.QuoteMeta(name), "/", "$/^") + "$", nil
}

// Start compiles the test binary of pkg (a directory) and starts it as
// ref's session: an error, with the binary's output, if it names no node.
// A mutant's run is its mutant binary's: GOTOGETHER_MUTANT names it, and
// the binary is built with its overlay (testdata/mutants/<name>).
func Start(pkg, ref string) (*Session, error) {
	_, pattern, err := Test(ref)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(pkg)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "gotogether-nav-")
	if err != nil {
		return nil, err
	}
	bin := filepath.Join(dir, "nav.test")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	args := []string{"test", "-c", "-o", bin}
	// a mutant's run (GOTOGETHER_MUTANT, as its navigate line sets it):
	// the binary built with the mutant's overlay, if it has one
	if o, err := overlay.Of(abs, overlay.Mutant(), dir); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("the session of %s: %w", ref, err)
	} else if o != "" {
		args = append(args, "-overlay", o)
	}
	build := exec.Command("go", append(args, ".")...)
	build.Dir = abs
	if out, err := build.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("go test -c in %s: %v\n%s", abs, err, out)
	}
	s := &Session{dir: dir, logs: &lockedBuffer{}}
	s.cmd = exec.Command(bin, "-test.run", pattern, "-test.count=1")
	s.cmd.Dir = abs
	s.cmd.Env = append(os.Environ(), "GOTOGETHER_RUN="+ref, "GOTOGETHER_NAV=serve")
	s.cmd.Stderr = s.logs
	var out io.Reader
	if runtime.GOOS == "windows" {
		// no inherited descriptors: the responses share stdout with the
		// test's own output, a response a line
		pipe, err := s.cmd.StdoutPipe()
		if err != nil {
			return nil, s.fail(err)
		}
		out = pipe
	} else {
		// the responses on their own descriptor: what the system under
		// test prints stays out of them
		r, w, err := os.Pipe()
		if err != nil {
			return nil, s.fail(err)
		}
		s.cmd.ExtraFiles = []*os.File{w}
		s.cmd.Env = append(s.cmd.Env, "GOTOGETHER_NAV_OUT=/dev/fd/3")
		s.cmd.Stdout = s.logs
		out, s.pipe = r, r
	}
	if s.in, err = s.cmd.StdinPipe(); err != nil {
		return nil, s.fail(err)
	}
	err = s.cmd.Start()
	// the binary holds the write end now: closed here, so that its end is
	// the reader's EOF
	for _, f := range s.cmd.ExtraFiles {
		f.Close()
	}
	if err != nil {
		return nil, s.fail(err)
	}
	s.out = bufio.NewScanner(out)
	s.out.Buffer(make([]byte, 0, 64<<10), 64<<20)
	r, err := s.read()
	if err != nil {
		s.Close() // waits for the binary: its output is all in the logs
		return nil, fmt.Errorf("the session of %s in %s: %w:\n%s", ref, abs, err, s.logs.String())
	}
	if r.Session == nil {
		s.Close()
		return nil, fmt.Errorf("the session of %s: its first line names no session: %s", ref, r.Error)
	}
	s.Info = *r.Session
	return s, nil
}

func (s *Session) fail(err error) error {
	if s.pipe != nil {
		s.pipe.Close()
	}
	os.RemoveAll(s.dir)
	return err
}

// read is the session's next response.
func (s *Session) read() (validate.RunResponse, error) {
	for s.out.Scan() {
		line := bytes.TrimSpace(s.out.Bytes())
		if !bytes.HasPrefix(line, []byte("{")) {
			continue // the test's own output (windows)
		}
		var r validate.RunResponse
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		if r.Session == nil && r.Node == nil && r.Error == "" {
			continue
		}
		return r, nil
	}
	err := s.out.Err()
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return validate.RunResponse{}, fmt.Errorf("the test binary ended (%v)", err)
}

// Node asks the session for the node req names.
func (s *Session) Node(req validate.RunRequest) (*validate.RunNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := s.in.Write(append(b, '\n')); err != nil {
		return nil, fmt.Errorf("the session: %w\n%s", err, s.logs.String())
	}
	r, err := s.read()
	if err != nil {
		return nil, fmt.Errorf("%w:\n%s", err, s.logs.String())
	}
	if r.Error != "" {
		return nil, errors.New(r.Error)
	}
	if r.Node == nil {
		return nil, fmt.Errorf("the session answered no node")
	}
	return r.Node, nil
}

// Close ends the session (its input closed, the binary left to finish)
// and removes the compiled binary.
func (s *Session) Close() error {
	var err error
	if s.in != nil {
		s.in.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		if werr := s.cmd.Wait(); werr != nil {
			err = fmt.Errorf("the test binary: %v\n%s", werr, s.logs.String())
		}
	}
	if s.pipe != nil {
		s.pipe.Close()
	}
	os.RemoveAll(s.dir)
	return err
}

// lockedBuffer is the binary's output, written by exec's copying
// goroutines and read on an error.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
