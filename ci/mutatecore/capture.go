package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

type generatedInput struct {
	Source  string       `json:"source"`
	Deleted bool         `json:"deleted"`
	File    fileIdentity `json:"file"`
}
type capturedInvocation struct {
	Program           string           `json:"program"`
	Directory         string           `json:"directory"`
	EnvironmentDigest string           `json:"environment_digest"`
	Arguments         []string         `json:"arguments"`
	Inputs            []generatedInput `json:"inputs"`
}

// captureGo preserves the actual generated overlay bytes before the owned Go
// command consumes them. Exec keeps compiler descendants in qweave's group.
func captureGo(ctx context.Context, args []string) error {
	if len(args) < 3 {
		return errors.New("invalid owned Go capture invocation")
	}
	dir, realGo, argv := args[0], args[1], args[2:]
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	invocation, err := os.MkdirTemp(dir, "invocation-")
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	env := os.Environ()
	slices.Sort(env)
	envDigest, err := valueDigest(env)
	if err != nil {
		return err
	}
	record := capturedInvocation{Program: realGo, Directory: cwd, EnvironmentDigest: envDigest, Arguments: append([]string(nil), argv...)}
	overlay := ""
	for i, a := range argv {
		if p, ok := strings.CutPrefix(a, "-overlay="); ok {
			overlay = p
		}
		if a == "-overlay" && i+1 < len(argv) {
			overlay = argv[i+1]
		}
	}
	if overlay != "" {
		raw, err := os.ReadFile(overlay)
		if err != nil {
			return err
		}
		var data struct{ Replace map[string]string }
		if err := json.Unmarshal(raw, &data); err != nil {
			return err
		}
		names := make([]string, 0, len(data.Replace))
		for name := range data.Replace {
			names = append(names, name)
		}
		slices.Sort(names)
		for i, name := range names {
			input := generatedInput{Source: name, Deleted: data.Replace[name] == ""}
			if !input.Deleted {
				content, err := os.ReadFile(data.Replace[name])
				if err != nil {
					return err
				}
				path := filepath.Join(invocation, fmt.Sprintf("input-%06d", i))
				if err := writeCapturedInput(path, content); err != nil {
					return err
				}
				input.File, err = identifyFile(path)
				if err != nil {
					return err
				}
			}
			record.Inputs = append(record.Inputs, input)
		}
	}
	if err := publishJSON(filepath.Join(invocation, "invocation.json"), record); err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := errors.Join(parent.Sync(), parent.Close()); err != nil {
		return err
	}
	return syscall.Exec(realGo, append([]string{realGo}, argv...), os.Environ())
}

func prepareCapture(cfg workerConfig) (string, error) {
	if err := os.Mkdir(cfg.Capture, 0700); err != nil {
		return "", err
	}
	dir := filepath.Join(cfg.Work, "go-wrapper")
	if err := os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nexec " + quote(exe) + " --capture-go " + quote(cfg.Capture) + " " + quote(cfg.Go) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func captureManifest(dir string) ([]capturedInvocation, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var records []capturedInvocation
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "invocation-") {
			return nil, fmt.Errorf("unexpected generated capture input %s", entry.Name())
		}
		var r capturedInvocation
		if err := readJSONFile(filepath.Join(dir, entry.Name(), "invocation.json"), &r); err != nil {
			return nil, err
		}
		for _, input := range r.Inputs {
			if input.Deleted {
				continue
			}
			rel, err := filepath.Rel(filepath.Join(dir, entry.Name()), input.File.Path)
			if err != nil || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
				return nil, errors.New("generated input escapes owned capture")
			}
			actual, err := identifyFile(input.File.Path)
			if err != nil {
				return nil, err
			}
			if actual != input.File {
				return nil, errors.New("generated overlay content changed")
			}
		}
		records = append(records, r)
	}
	return records, nil
}

func writeCapturedInput(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	return errors.Join(writeErr, f.Sync(), f.Close())
}
