package validate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/chucklehead-dev/gotogether/internal/atomicdir"
)

// SourceSnapshot identifies the bytes exported by SourceGuard.Export. Paths are
// slash-separated and relative to the exported directory. Files includes Spec
// and the guard's conservative source inventory, each with its captured hash.
// No source is rewritten: relative imports retain their original relationships.
type SourceSnapshot struct {
	Spec  File
	Files []File
}

// Export copies the captured model sources into a fresh directory, preserving
// relative imports and publishing the verified directory atomically. Absolute
// imports or imports outside the captured inventory are refused. Symlinks are
// materialized as regular files. Export checks observed source changes before
// and after copying; it does not provide an atomic snapshot of the source tree.
func (g *SourceGuard) Export(ctx context.Context, dir string) (*SourceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g == nil || len(g.files) == 0 {
		return nil, fmt.Errorf("source snapshot: capture the model sources before exporting")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(dir); err == nil {
		return nil, fmt.Errorf("source snapshot: %s already exists: choose a fresh directory", dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := g.Verify(); err != nil {
		return nil, err
	}
	if err := snapshotImports(ctx, g.files); err != nil {
		return nil, err
	}
	root := filepath.Dir(g.files[0].Path)
	for _, f := range g.files[1:] {
		for !within(root, f.Path) {
			next := filepath.Dir(root)
			if next == root {
				return nil, fmt.Errorf("source snapshot: source files do not share a filesystem root")
			}
			root = next
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+".stage-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	snapshot := &SourceSnapshot{}
	spec, err := filepath.Abs(g.spec)
	if err != nil {
		return nil, err
	}
	for _, f := range g.files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, f.Path)
		if err != nil {
			return nil, err
		}
		to := filepath.Join(stage, rel)
		if !within(stage, to) {
			return nil, fmt.Errorf("source snapshot: file %s escapes the staged directory", f.Path)
		}
		if err := snapshotCopy(ctx, f, to); err != nil {
			return nil, err
		}
		out := File{Path: filepath.ToSlash(rel), SHA256: f.SHA256}
		snapshot.Files = append(snapshot.Files, out)
		if filepath.Clean(f.Path) == filepath.Clean(spec) {
			snapshot.Spec = out
		}
	}
	if snapshot.Spec.Path == "" {
		return nil, fmt.Errorf("source snapshot: main spec is absent from the captured inventory")
	}
	// Re-scanning the staged model verifies both import closure and the same
	// conservative sibling inventory; checking copied bytes alone misses an
	// accidentally omitted dependency.
	copied, ok := specFilesAll(filepath.Join(stage, filepath.FromSlash(snapshot.Spec.Path)))
	if !ok || len(copied) != len(snapshot.Files) {
		return nil, fmt.Errorf("source snapshot: staged model source inventory is incomplete")
	}
	for i, f := range copied {
		rel, err := filepath.Rel(stage, f.Path)
		if err != nil || filepath.ToSlash(rel) != snapshot.Files[i].Path || f.SHA256 != snapshot.Files[i].SHA256 {
			return nil, fmt.Errorf("source snapshot: staged model source inventory differs from the captured sources")
		}
	}
	if err := g.Verify(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := atomicdir.Publish(stage, dir); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func snapshotImports(ctx context.Context, files []File) error {
	inventory := map[string]bool{}
	for _, f := range files {
		inventory[filepath.Clean(f.Path)] = true
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		src, err := os.ReadFile(f.Path)
		if err != nil {
			return err
		}
		for _, ref := range importPaths(string(src)) {
			if absoluteReference(ref) {
				return fmt.Errorf("source snapshot: spec %s has absolute import %q: use relative imports before exporting", f.Path, ref)
			}
			target := filepath.Join(filepath.Dir(f.Path), filepath.FromSlash(ref))
			if filepath.Ext(target) != ".qnt" {
				target += ".qnt"
			}
			if !inventory[filepath.Clean(target)] {
				return fmt.Errorf("source snapshot: import %q in %s is absent from the captured inventory: capture the complete model before exporting", ref, f.Path)
			}
		}
	}
	return nil
}

func snapshotCopy(ctx context.Context, f File, to string) error {
	in, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source snapshot: %s is not a regular file", f.Path)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, hash), snapshotReader{ctx: ctx, r: in})
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("source snapshot: %s changed since capture: restore the sources before exporting", f.Path)
	}
	return nil
}

type snapshotReader struct {
	ctx context.Context
	r   io.Reader
}

func (r snapshotReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
