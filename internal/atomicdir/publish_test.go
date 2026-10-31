package atomicdir

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func supported(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("exclusive directory publication unsupported")
	}
}

func TestPublishRefusesExistingDestination(t *testing.T) {
	supported(t)
	for _, kind := range []string{"empty", "nonempty", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			stage, dst := filepath.Join(parent, "stage"), filepath.Join(parent, "dst")
			if err := os.Mkdir(stage, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stage, "new"), []byte("new evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "empty", "nonempty":
				if err := os.Mkdir(dst, 0700); err != nil {
					t.Fatal(err)
				}
				if kind == "nonempty" {
					if err := os.WriteFile(filepath.Join(dst, "old"), []byte("original"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "file":
				if err := os.WriteFile(dst, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("missing", dst); err != nil {
					t.Skipf("symlink: %v", err)
				}
			}
			if err := Publish(stage, dst); err == nil {
				t.Fatal("replaced existing destination")
			}
			if _, err := os.Stat(filepath.Join(stage, "new")); err != nil {
				t.Fatal("failed publication lost staging:", err)
			}
			if _, err := os.Lstat(dst); err != nil {
				t.Fatal("failed publication lost destination:", err)
			}
			if kind == "nonempty" {
				b, err := os.ReadFile(filepath.Join(dst, "old"))
				if err != nil || string(b) != "original" {
					t.Fatalf("changed original: %s %v", b, err)
				}
			}
		})
	}
}

func TestPublishConcurrentWriters(t *testing.T) {
	supported(t)
	parent := t.TempDir()
	dst := filepath.Join(parent, "published")
	const n = 12
	var wg sync.WaitGroup
	results := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		stage, err := os.MkdirTemp(parent, "stage-")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stage, "evidence"), []byte(stage), 0600); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- Publish(stage, dst) }()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("published %d competing directories, want exactly one", successes)
	}
	b, err := os.ReadFile(filepath.Join(dst, "evidence"))
	if err != nil || len(b) == 0 {
		t.Fatalf("partial publication: %s %v", b, err)
	}
}
