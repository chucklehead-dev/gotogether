package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
)

// Reuse is limited to this reviewed managed CLI layout. This identifies a local
// tool closure, not an authenticated producer. External OS/shared libraries must
// remain stable during the iterator. Arbitrary wrappers/proxies are ineligible.
const nativeManagedCLIHash = "952a50a28932282528b5c9b86ac5542405d85da4f80876aabe8cb8c7d5a6d0e8"
const nativeManagedNodeHash = "94efd062ed066c1a0b332e18249859c905e920eeada5df55d24c99b919629a90"

func resolveNativeToolIdentity(ctx context.Context, quint string) (string, bool) {
	for _, name := range []string{"NODE_OPTIONS", "NODE_PATH", "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH", "APALACHE_JAR", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS", "JVM_ARGS", "JVM_GC_ARGS", "JAVA_OPTS", "CLASSPATH"} {
		if os.Getenv(name) != "" {
			return "", false
		}
	}
	resolve := func(name string) (string, bool) {
		path, err := exec.LookPath(name)
		if err != nil {
			return "", false
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", false
		}
		path, err = filepath.Abs(path)
		return path, err == nil
	}
	if quint == "" {
		quint = "quint"
	}
	cli, ok := resolve(quint)
	if !ok || filepath.Base(cli) != "cli.js" || filepath.Base(filepath.Dir(cli)) != "src" || filepath.Base(filepath.Dir(filepath.Dir(cli))) != "dist" {
		return "", false
	}
	cliHash, err := nativeHashFile(ctx, cli)
	if err != nil || cliHash != nativeManagedCLIHash {
		return "", false
	}
	pkgRoot := filepath.Dir(filepath.Dir(filepath.Dir(cli)))
	metadata, err := os.ReadFile(filepath.Join(pkgRoot, "package.json"))
	if err != nil {
		return "", false
	}
	var pkg struct{ Name, Version string }
	if json.Unmarshal(metadata, &pkg) != nil || pkg.Name != "@informalsystems/quint" || pkg.Version != "0.33.1" {
		return "", false
	}
	modules := filepath.Dir(filepath.Dir(pkgRoot))
	if filepath.Base(modules) != "node_modules" {
		return "", false
	}
	node, ok := resolve("node")
	if !ok {
		return "", false
	}
	nodeHash, err := nativeHashFile(ctx, node)
	if err != nil || nodeHash != nativeManagedNodeHash {
		return "", false
	}
	java, ok := resolve("java")
	if !ok || filepath.Base(java) != "java" || filepath.Base(filepath.Dir(java)) != "bin" || !nativeELF(java) {
		return "", false
	}
	javaRoot := filepath.Dir(filepath.Dir(java))
	if _, err := os.Stat(filepath.Join(javaRoot, "release")); err != nil {
		return "", false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	quintHome := os.Getenv("QUINT_HOME")
	if quintHome == "" {
		quintHome = filepath.Join(home, ".quint")
	}
	if !filepath.IsAbs(quintHome) {
		return "", false
	}
	apalache := filepath.Join(quintHome, "apalache-dist-0.62.4", "apalache")
	if _, err := os.Stat(filepath.Join(apalache, "lib", "apalache.jar")); err != nil {
		return "", false
	} // no speculative cold-backend reuse
	roots := []string{modules, javaRoot, apalache}
	h := sha256.New()
	env := os.Environ()
	slices.Sort(env)
	encoded, _ := json.Marshal(struct {
		CLI, Node, Java string
		Env             []string
	}{cli, node, java, env})
	h.Write(encoded)
	for _, name := range []string{"bash", "dirname", "readlink", "mktemp", "mkdir", "env"} {
		path, ok := resolve(name)
		if !ok {
			return "", false
		}
		if !nativeELF(path) {
			return "", false
		}
		digest, err := nativeHashFile(ctx, path)
		if err != nil {
			return "", false
		}
		encoded, _ := json.Marshal([]string{path, digest})
		h.Write(encoded)
	}
	visited := map[string]bool{}
	var captureTree func(string) error
	captureTree = func(root string) error {
		root, err := filepath.EvalSymlinks(root)
		if err != nil {
			return err
		}
		if visited[root] {
			return nil
		}
		visited[root] = true
		return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			info, err := os.Stat(target)
			if err != nil {
				return err
			}
			if info.IsDir() {
				// Managed JDKs may link directories outside their installation root.
				// Retain both the link identity and its complete directory contents.
				encoded, _ := json.Marshal([]string{path, target, info.Mode().String()})
				h.Write(encoded)
				return captureTree(target)
			}
			if !info.Mode().IsRegular() {
				return fs.ErrInvalid
			}
			digest, err := nativeHashFile(ctx, target)
			if err != nil {
				return err
			}
			encoded, _ := json.Marshal([]string{path, target, digest, info.Mode().String()})
			h.Write(encoded)
			return nil
		})
	}
	for _, root := range roots {
		if err := captureTree(root); err != nil {
			return "", false
		}
	}
	// Node may reside outside all captured roots. Its full byte hash is required.
	h.Write([]byte(nodeHash))
	return hex.EncodeToString(h.Sum(nil)), true
}

func nativeHashFile(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := file.Read(buffer)
		if n > 0 {
			h.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Script launchers can delegate outside the captured installation. Eligibility
// requires native executables; this is not authentication of arbitrary binaries.
func nativeELF(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var magic [4]byte
	_, err = io.ReadFull(file, magic[:])
	return err == nil && string(magic[:]) == "\x7fELF"
}
