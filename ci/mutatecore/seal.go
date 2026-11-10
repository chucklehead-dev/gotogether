package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// producerManifest is captured independently of the journal being validated.
// Binary paths remain available until the supervisor has checked and sealed it.
type producerManifest struct {
	InputDigest string           `json:"input_digest"`
	Start       json.RawMessage  `json:"start"`
	Suites      []suiteRecord    `json:"suites"`
	Binaries    []binaryIdentity `json:"binaries"`
}
type binaryIdentity struct {
	Suite  int    `json:"suite"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func digestFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	err = errors.Join(err, f.Close())
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// publishJSON makes a new immutable-by-convention receipt name. No prior
// receipt is replaced. A directory sync error is a failed publication even if
// the name is already visible, so callers must not reinterpret it as success.
func publishJSON(path string, value any) error {
	return publishJSONContext(context.Background(), path, value)
}

func publishJSONContext(ctx context.Context, path string, value any) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".receipt-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	// Validation, hashing, encoding and Sync may all take time. Publication is
	// the final boundary: cancellation during any of them must not acquire a
	// successful receipt name.
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err = os.Link(tmp, path); err != nil {
		return fmt.Errorf("publish receipt without replacement: %w", err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// decodeJSON rejects duplicate names, aliases, missing required fields and
// unknown fields before typed decoding. encoding/json alone accepts aliases
// and duplicate object fields, which is unsuitable for sealed evidence.
func decodeJSON(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := jsonValue(dec)
	if err != nil {
		return err
	}
	if _, err = dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON content")
	}
	if err = checkJSONShape(value, reflect.TypeOf(dst).Elem()); err != nil {
		return err
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
func jsonValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); ok {
		switch d {
		case '{':
			result := map[string]any{}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return nil, err
				}
				name, ok := k.(string)
				if !ok {
					return nil, errors.New("non-string object key")
				}
				if _, ok = result[name]; ok {
					return nil, fmt.Errorf("duplicate JSON field %q", name)
				}
				v, err := jsonValue(dec)
				if err != nil {
					return nil, err
				}
				result[name] = v
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return result, nil
		case '[':
			var result []any
			for dec.More() {
				v, err := jsonValue(dec)
				if err != nil {
					return nil, err
				}
				result = append(result, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return result, nil
		}
		return nil, errors.New("unexpected JSON delimiter")
	}
	return tok, nil
}
func jsonFields(t reflect.Type) map[string]reflect.StructField {
	fields := map[string]reflect.StructField{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" && !f.Anonymous {
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "-" {
			continue
		}
		if f.Anonymous && tag == "" {
			for k, v := range jsonFields(f.Type) {
				fields[k] = v
			}
			continue
		}
		if tag == "" {
			tag = f.Name
		}
		fields[tag] = f
	}
	return fields
}
func checkJSONShape(value any, t reflect.Type) error {
	if t == reflect.TypeFor[json.RawMessage]() || t == reflect.TypeFor[time.Time]() {
		if value == nil {
			return errors.New("null required JSON value")
		}
		return nil
	}
	if t.Kind() == reflect.Pointer {
		if value == nil {
			return errors.New("null optional field; omit it instead")
		}
		return checkJSONShape(value, t.Elem())
	}
	if t.Kind() == reflect.Interface {
		return nil
	}
	if t.Kind() == reflect.Struct {
		obj, ok := value.(map[string]any)
		if !ok {
			return errors.New("expected JSON object")
		}
		fields := jsonFields(t)
		for k, v := range obj {
			f, ok := fields[k]
			if !ok {
				return fmt.Errorf("unknown JSON field %q", k)
			}
			if err := checkJSONShape(v, f.Type); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
		}
		for k, f := range fields {
			if _, ok := obj[k]; !ok && !strings.Contains(f.Tag.Get("json"), ",omitempty") {
				return fmt.Errorf("missing JSON field %q", k)
			}
		}
	} else if t.Kind() == reflect.Slice {
		if value == nil {
			return nil
		}
		arr, ok := value.([]any)
		if !ok {
			return errors.New("expected JSON array")
		}
		for _, v := range arr {
			if err := checkJSONShape(v, t.Elem()); err != nil {
				return err
			}
		}
	} else if value == nil {
		return errors.New("null scalar")
	}
	return nil
}

type sealedEvent struct {
	Event string          `json:"event"`
	At    time.Time       `json:"at"`
	Data  json.RawMessage `json:"data"`
}
type sealedBaseline struct {
	Package   string          `json:"package"`
	Passed    bool            `json:"passed"`
	Execution executionResult `json:"execution"`
	Error     string          `json:"error,omitempty"`
}
type sealedAdmitted struct {
	Sources []sourceRecord `json:"sources"`
	Suites  int            `json:"suites"`
}

func validateJournal(path string, manifest producerManifest) (completeRecord, error) {
	var zero completeRecord
	if decoded, err := hex.DecodeString(manifest.InputDigest); err != nil || len(decoded) != sha256.Size {
		return zero, errors.New("manifest input digest missing or invalid")
	}
	if len(manifest.Suites) == 0 || len(manifest.Binaries) != len(manifest.Suites) {
		return zero, errors.New("manifest suite/binary roster incomplete")
	}
	suites := make([]suite, len(manifest.Suites))
	packages := map[string]bool{}
	for i, s := range manifest.Suites {
		if s.Index != i || s.Package == "" || packages[s.Package] {
			return zero, errors.New("manifest suite roster invalid")
		}
		packages[s.Package] = true
		b := manifest.Binaries[i]
		hash, size, err := digestFile(s.Binary)
		if err != nil {
			return zero, err
		}
		if b.Suite != i || b.SHA256 != hash || b.Size != size {
			return zero, fmt.Errorf("suite %d binary identity changed", i)
		}
		suites[i] = suite{pkg: s.Package, dir: s.Directory, binary: s.Binary}
		counts := map[string]int{}
		for j, m := range s.Inventory {
			if m.ID != j || m.File == "" || m.Descriptor == "" || m.Ordinal != counts[m.File] {
				return zero, errors.New("manifest inventory invalid")
			}
			counts[m.File]++
			suites[i].mutants = append(suites[i].mutants, mutant{source: source{m.File, m.Ordinal}, descriptor: m.Descriptor, id: m.ID})
		}
	}
	candidates, err := admit(suites, func(suite) error { return nil })
	if err != nil {
		return zero, err
	}
	expected := make([]sourceRecord, len(candidates))
	bySource := map[source]int{}
	for i, c := range candidates {
		expected[i] = sourceMetadata(c)
		bySource[c.source] = i
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(manifest.Start, &settings); err != nil {
		return zero, err
	}
	var inputDigest string
	if err := json.Unmarshal(settings["input_digest"], &inputDigest); err != nil || inputDigest != manifest.InputDigest {
		return zero, errors.New("manifest start input digest differs")
	}
	var max int
	if raw, ok := settings["max_survivors"]; !ok {
		return zero, errors.New("manifest maximum missing")
	} else if err := json.Unmarshal(raw, &max); err != nil {
		return zero, err
	}
	var version int
	if err := json.Unmarshal(settings["version"], &version); err != nil || version != 1 {
		return zero, errors.New("unsupported journal version")
	}
	f, err := os.Open(path)
	if err != nil {
		return zero, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	phase, index, survivors := 0, 0, 0
	seen := map[int]bool{}
	complete := false
	for {
		var raw json.RawMessage
		err := dec.Decode(&raw)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return zero, fmt.Errorf("incomplete journal: %w", err)
		}
		if complete {
			return zero, errors.New("journal content after completion")
		}
		var e sealedEvent
		if err := decodeJSON(raw, &e); err != nil {
			return zero, err
		}
		if e.At.IsZero() {
			return zero, errors.New("journal event timestamp missing")
		}
		switch phase {
		case 0:
			if e.Event != "start" {
				return zero, errors.New("journal must start with start")
			}
			var actual, expectedStart any
			if err := decodeJSON(e.Data, &actual); err != nil {
				return zero, err
			}
			if err := decodeJSON(manifest.Start, &expectedStart); err != nil {
				return zero, err
			}
			if !reflect.DeepEqual(actual, expectedStart) {
				return zero, errors.New("journal start differs from manifest")
			}
			phase = 1
		case 1:
			if e.Event != "suite" {
				return zero, errors.New("missing suite record")
			}
			var s suiteRecord
			if err := decodeJSON(e.Data, &s); err != nil {
				return zero, err
			}
			if !reflect.DeepEqual(s, manifest.Suites[index]) {
				return zero, errors.New("suite differs from independent manifest")
			}
			index++
			if index == len(suites) {
				phase = 2
				index = 0
			}
		case 2:
			if e.Event != "baseline" {
				return zero, errors.New("missing baseline")
			}
			var b sealedBaseline
			if err := decodeJSON(e.Data, &b); err != nil {
				return zero, err
			}
			if b.Package != suites[index].pkg || !b.Passed || b.Error != "" || b.Execution.validate() != nil || !b.Execution.passed() {
				return zero, errors.New("baseline did not pass")
			}
			index++
			if index == len(suites) {
				phase = 3
			}
		case 3:
			if e.Event != "admitted" {
				return zero, errors.New("missing admission")
			}
			var a sealedAdmitted
			if err := decodeJSON(e.Data, &a); err != nil {
				return zero, err
			}
			if a.Suites != len(suites) || !reflect.DeepEqual(a.Sources, expected) {
				return zero, errors.New("admission differs from independent union")
			}
			phase = 4
		case 4:
			if e.Event == "complete" {
				var c completeRecord
				if err := decodeJSON(e.Data, &c); err != nil {
					return zero, err
				}
				if len(seen) != len(expected) || c.Total != len(expected) || c.Survivors != survivors || c.GatePassed != (max < 0 || survivors <= max) {
					return zero, errors.New("completion does not reconcile")
				}
				zero = c
				complete = true
				continue
			}
			if e.Event != "outcome" {
				return zero, errors.New("unexpected execution event")
			}
			var o outcomeRecord
			if err := decodeJSON(e.Data, &o); err != nil {
				return zero, err
			}
			i, ok := bySource[source{o.Source.File, o.Source.Ordinal}]
			if !ok || seen[i] || !reflect.DeepEqual(o.Source, expected[i]) {
				return zero, errors.New("unknown/duplicate source outcome")
			}
			if err := validateOutcome(o); err != nil {
				return zero, err
			}
			seen[i] = true
			if o.Outcome == "survived" {
				survivors++
			}
		}
	}
	if !complete {
		return zero, errors.New("journal lacks complete evidence")
	}
	return zero, nil
}
func validateOutcome(o outcomeRecord) error {
	if o.Error != "" || len(o.Attempts) == 0 || len(o.Attempts) > len(o.Source.Targets) {
		return errors.New("incomplete outcome attempts")
	}
	killed := false
	for i, a := range o.Attempts {
		if a.targetRecord != o.Source.Targets[i] || a.Error != "" || a.Execution.validate() != nil {
			return errors.New("invalid attempt identity/execution")
		}
		passed := a.Execution.passed()
		if passed && a.Outcome != "passed" || !passed && a.Outcome != "failed" {
			return errors.New("attempt outcome contradicts execution")
		}
		if !passed {
			if i != len(o.Attempts)-1 {
				return errors.New("attempt follows killing execution")
			}
			killed = true
		}
	}
	if killed {
		if o.Outcome != "killed" {
			return errors.New("failed execution not classified killed")
		}
	} else if o.Outcome != "survived" || len(o.Attempts) != len(o.Source.Targets) {
		return errors.New("survival lacks every target")
	}
	return nil
}
