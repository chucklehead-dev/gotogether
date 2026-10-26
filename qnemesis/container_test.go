package qnemesis

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// A Container is killed (and waited for), restarted, paused and resumed
// through the docker CLI. It needs a docker daemon and an image
// (QNEMESIS_IMAGE, default alpine:3.20) with sleep.
func TestContainer(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("docker"); err != nil {
		skip.Optional(t, "no docker CLI")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		skip.Optional(t, "no docker daemon")
	}
	image := os.Getenv("QNEMESIS_IMAGE")
	if image == "" {
		image = "alpine:3.20"
	}
	name := fmt.Sprintf("qnemesis-test-%d", time.Now().UnixNano())
	if out, err := exec.Command("docker", "run", "-d", "--name", name, image, "sleep", "600").CombinedOutput(); err != nil {
		skip.Optional(t, fmt.Sprintf("docker run %s: %v: %s", image, err, out))
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	c := &Container{Name: name}
	ctx := context.Background()
	n := &Nemesis{Targets: map[string]Target{"box": c}}
	if !c.Alive() || c.Incarnation() != 1 {
		t.Fatalf("a running container: alive %v, incarnation %d", c.Alive(), c.Incarnation())
	}
	if err := n.Run(ctx, Schedule{{Kind: "nemesis.pause.box", For: 100 * time.Millisecond}, {Kind: "nemesis.crash.box", For: 100 * time.Millisecond, Follows: []int{0}}}); err != nil {
		t.Fatal(err)
	}
	if !c.Alive() || c.Incarnation() != 2 {
		t.Fatalf("after a crash: alive %v, incarnation %d; want running, 2", c.Alive(), c.Incarnation())
	}
	if err := c.Kill(ctx); err != nil || c.Alive() {
		t.Fatalf("Kill: %v, alive %v", err, c.Alive())
	}
	if err := n.Stop(ctx); err != nil || !c.Alive() {
		t.Fatalf("Stop: %v, alive %v; want the container started", err, c.Alive())
	}
}
