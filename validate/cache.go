package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// A cache of quint's typechecks and compiles across processes, by
// content: a model is typechecked once per process (loadModel) and its
// evaluation module compiled once (compileEval), but many processes ask
// for the same ones (a suite's packages' test binaries, its overlay
// mutants' subprocesses, the CLI per invocation), each a quint process of
// seconds. An entry is quint's own output (typecheck JSON, compile IR),
// parsed as a fresh one is, under the user's cache directory.
//
// Its key is everything the output depends on, conservatively:
//   - quint's version (`quint --version` of the binary used);
//   - the spec's and every file it imports' absolute paths and contents
//     (specFiles' transitive imports), and every .qnt file in each of
//     their directories too, so an import importPaths missed in a
//     sibling still changes the key; an import that cannot be read
//     gives no key (no cache);
//   - the subcommand and every argument that is not a path (compile's
//     main, init and step), and the evaluation module's text;
//   - the cache's own format (cacheFormat).
//
// GOTOGETHER_CACHE=off bypasses it; GOTOGETHER_CACHE=<dir> puts it there.

// cacheFormat names the key's and the entries' form: bump it when either
// changes, and old entries are never read.
const cacheFormat = "gotogether quint cache 1"

// cacheRoot is the cache's directory, "" when it is off.
func cacheRoot() string {
	switch v := os.Getenv("GOTOGETHER_CACHE"); {
	case v == "off":
		return ""
	case v != "":
		return v
	}
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "gotogether", "quint")
}

// cacheKey is the key of quint's kind output for spec with args, or
// false when there is none (the cache off, quint's version unknown, a
// file the spec imports unreadable).
func cacheKey(quint, kind, spec string, args ...string) (string, bool) {
	if cacheRoot() == "" {
		return "", false
	}
	v := quintVersion(quint) // (manifest.go's, asked once a binary)
	if v == "" {
		return "", false
	}
	files, ok := specFilesAll(spec)
	if !ok {
		return "", false
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00", cacheFormat, v, kind)
	for _, a := range args {
		fmt.Fprintf(h, "%d:%s\x00", len(a), a)
	}
	for _, f := range files {
		fmt.Fprintf(h, "%s=%s\x00", f.Path, f.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// specFilesAll are the files quint's output on spec may depend on, sorted
// by path: the spec, its imports (transitively), and every .qnt file in
// their directories; false if an import cannot be read.
func specFilesAll(spec string) ([]File, bool) {
	f, imports, err := specFiles(spec)
	if err != nil {
		return nil, false
	}
	files := map[string]File{f.Path: *f}
	todo := []string{f.Path}
	for _, i := range imports {
		files[i.Path] = i
		todo = append(todo, i.Path)
	}
	// specFiles leaves out an import it cannot read: here that is no key
	for _, p := range todo {
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, false
		}
		for _, imp := range importPaths(string(src)) {
			q := filepath.Join(filepath.Dir(p), filepath.FromSlash(imp))
			if filepath.Ext(q) != ".qnt" {
				q += ".qnt"
			}
			if _, ok := files[q]; !ok {
				return nil, false
			}
		}
	}
	dirs := map[string]bool{}
	for p := range files {
		dirs[filepath.Dir(p)] = true
	}
	for d := range dirs {
		siblings, err := filepath.Glob(filepath.Join(d, "*.qnt"))
		if err != nil {
			return nil, false
		}
		for _, s := range siblings {
			if _, ok := files[s]; ok {
				continue
			}
			h, err := hashed(s)
			if err != nil {
				return nil, false
			}
			files[h.Path] = *h
		}
	}
	out := make([]File, 0, len(files))
	for _, f := range files {
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return out, true
}

// cachePath is the entry of key, of kind.
func cachePath(kind, key string) string {
	return filepath.Join(cacheRoot(), kind, key[:2], key+".json")
}

// cacheGet is the entry of key, if there is one, its time touched: an
// entry no process has used for cacheAge is pruned.
func cacheGet(kind, key string) ([]byte, bool) {
	path := cachePath(kind, key)
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	now := time.Now()
	os.Chtimes(path, now, now)
	return data, true
}

// cacheAge is how long an unused entry is kept: the cache holds what
// every spec ever checked compiled to (a test's temporary specs too), a
// grist model's typecheck about 3 MB.
const cacheAge = 7 * 24 * time.Hour

// Process-wide: whether this process pruned the cache.
var pruned sync.Once

// prune removes the entries no process used for cacheAge, and writes a
// process left unfinished, once a process.
func prune() {
	pruned.Do(func() {
		root := cacheRoot()
		cut := time.Now().Add(-cacheAge)
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, err := d.Info(); err == nil && info.ModTime().Before(cut) {
				os.Remove(p)
			}
			return nil
		})
	})
}

// cachePut stores data as key's entry, whole or not at all: written
// beside it under a name of its own, then renamed, so a process reading
// it never sees part of one another is writing. A failure leaves no
// entry, and is no error: the cache only saves work.
func cachePut(kind, key string, data []byte) {
	prune()
	path := cachePath(kind, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
}

// lockWait bounds how long a process waits for another's entry: past it
// (a lock left by a killed process, a quint far slower than usual) it
// asks quint itself.
const lockWait = 3 * time.Minute

// cacheLock is key's lock, so that processes starting at once (a suite's
// test binaries, in parallel) ask quint once between them, not once
// each: held, it returns unlock (the caller asks quint and puts the
// entry); when another process held it and wrote the entry meanwhile,
// the entry. A lock it cannot take in lockWait, or at all, is no lock:
// the caller asks quint.
func cacheLock(kind, key string) (unlock func(), data []byte, ok bool) {
	path := cachePath(kind, key)
	lock := path + ".lock"
	none := func() {}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return none, nil, false
	}
	deadline := time.Now().Add(lockWait)
	for time.Now().Before(deadline) {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			if data, ok := cacheGet(kind, key); ok { // written by the holder before
				os.Remove(lock)
				return none, data, true
			}
			return func() { os.Remove(lock) }, nil, false
		}
		if !errors.Is(err, fs.ErrExist) {
			return none, nil, false
		}
		if data, ok := cacheGet(kind, key); ok {
			return none, data, true
		}
		if info, err := os.Stat(lock); err == nil && time.Since(info.ModTime()) > lockWait {
			os.Remove(lock) // its holder died
			continue
		}
		time.Sleep(100 * time.Millisecond)
	}
	return none, nil, false
}
