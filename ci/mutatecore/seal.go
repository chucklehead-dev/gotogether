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
	"slices"
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
	Sources  []sourceRecord `json:"sources"`
	Suites   int            `json:"suites"`
	Selected []int          `json:"selected"`
}

func validateJournal(path string, manifest producerManifest) (completeRecord, error) {
	if len(manifest.Suites) == 0 || len(manifest.Binaries) != len(manifest.Suites) {
		return completeRecord{}, errors.New("manifest suite/binary roster incomplete")
	}
	for i, s := range manifest.Suites {
		b := manifest.Binaries[i]
		hash, size, err := digestFile(s.Binary)
		if err != nil {
			return completeRecord{}, err
		}
		if b.Suite != i || b.SHA256 != hash || b.Size != size {
			return completeRecord{}, fmt.Errorf("suite %d binary identity changed", i)
		}
	}
	complete, _, err := validateJournalContent(path, manifest)
	return complete, err
}

// validateJournalContent checks a journal against an independent manifest
// without its binaries (the aggregate has only the records): its settings,
// suites and passing baselines, the complete ordered inventory admitted, the
// shard's selection of it, a valid outcome for exactly each selected source,
// and a completion that reconciles. It returns the journal's outcomes.
func validateJournalContent(path string, manifest producerManifest) (completeRecord, []outcomeRecord, error) {
	var zero completeRecord
	if decoded, err := hex.DecodeString(manifest.InputDigest); err != nil || len(decoded) != sha256.Size {
		return zero, nil, errors.New("manifest input digest missing or invalid")
	}
	if len(manifest.Binaries) != len(manifest.Suites) {
		return zero, nil, errors.New("manifest suite/binary roster incomplete")
	}
	suites, err := manifestSuites(manifest)
	if err != nil {
		return zero, nil, err
	}
	candidates, err := admit(suites, func(suite) error { return nil })
	if err != nil {
		return zero, nil, err
	}
	expected := make([]sourceRecord, len(candidates))
	for i, c := range candidates {
		expected[i] = sourceMetadata(c)
	}
	settings, err := decodeStart(manifest.Start)
	if err != nil {
		return zero, nil, err
	}
	if settings.InputDigest != manifest.InputDigest {
		return zero, nil, errors.New("manifest start input digest differs")
	}
	gate := settings.gate()
	selection := settings.Shard.selection(len(expected))
	bySource := map[source]int{}
	for _, k := range selection {
		bySource[source{expected[k].File, expected[k].Ordinal}] = k
	}
	var outcomes []outcomeRecord
	f, err := os.Open(path)
	if err != nil {
		return zero, nil, err
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
			return zero, nil, fmt.Errorf("incomplete journal: %w", err)
		}
		if complete {
			return zero, nil, errors.New("journal content after completion")
		}
		var e sealedEvent
		if err := decodeJSON(raw, &e); err != nil {
			return zero, nil, err
		}
		if e.At.IsZero() {
			return zero, nil, errors.New("journal event timestamp missing")
		}
		switch phase {
		case 0:
			if e.Event != "start" {
				return zero, nil, errors.New("journal must start with start")
			}
			var actual, expectedStart any
			if err := decodeJSON(e.Data, &actual); err != nil {
				return zero, nil, err
			}
			if err := decodeJSON(manifest.Start, &expectedStart); err != nil {
				return zero, nil, err
			}
			if !reflect.DeepEqual(actual, expectedStart) {
				return zero, nil, errors.New("journal start differs from manifest")
			}
			phase = 1
		case 1:
			if e.Event != "suite" {
				return zero, nil, errors.New("missing suite record")
			}
			var s suiteRecord
			if err := decodeJSON(e.Data, &s); err != nil {
				return zero, nil, err
			}
			if !reflect.DeepEqual(s, manifest.Suites[index]) {
				return zero, nil, errors.New("suite differs from independent manifest")
			}
			index++
			if index == len(suites) {
				phase = 2
				index = 0
			}
		case 2:
			if e.Event != "baseline" {
				return zero, nil, errors.New("missing baseline")
			}
			var b sealedBaseline
			if err := decodeJSON(e.Data, &b); err != nil {
				return zero, nil, err
			}
			if b.Package != suites[index].pkg || !b.Passed || b.Error != "" || b.Execution.validate() != nil || !b.Execution.passed() {
				return zero, nil, errors.New("baseline did not pass")
			}
			index++
			if index == len(suites) {
				phase = 3
			}
		case 3:
			if e.Event != "admitted" {
				return zero, nil, errors.New("missing admission")
			}
			var a sealedAdmitted
			if err := decodeJSON(e.Data, &a); err != nil {
				return zero, nil, err
			}
			if a.Suites != len(suites) || !reflect.DeepEqual(a.Sources, expected) {
				return zero, nil, errors.New("admission differs from independent union")
			}
			if !slices.Equal(a.Selected, selection) {
				return zero, nil, fmt.Errorf("shard %s selection differs from every %dth source of the complete inventory", settings.Shard, settings.Shard.Count)
			}
			phase = 4
		case 4:
			if e.Event == "complete" {
				var c completeRecord
				if err := decodeJSON(e.Data, &c); err != nil {
					return zero, nil, err
				}
				if len(seen) != len(selection) || c.Total != len(selection) || c.Survivors != survivors || c.Gate != gate.verdict(survivors) {
					return zero, nil, errors.New("completion does not reconcile")
				}
				zero = c
				complete = true
				continue
			}
			if e.Event != "outcome" {
				return zero, nil, errors.New("unexpected execution event")
			}
			var o outcomeRecord
			if err := decodeJSON(e.Data, &o); err != nil {
				return zero, nil, err
			}
			i, ok := bySource[source{o.Source.File, o.Source.Ordinal}]
			if !ok || seen[i] || !reflect.DeepEqual(o.Source, expected[i]) {
				return zero, nil, errors.New("unknown/duplicate source outcome, or one outside the shard's selection")
			}
			if err := validateOutcome(o); err != nil {
				return zero, nil, err
			}
			seen[i] = true
			outcomes = append(outcomes, o)
			if o.Outcome == "survived" {
				survivors++
			}
		}
	}
	if !complete {
		return zero, nil, errors.New("journal lacks complete evidence")
	}
	return zero, outcomes, nil
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
