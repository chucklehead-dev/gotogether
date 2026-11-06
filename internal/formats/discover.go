package formats

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// IsFile reports whether a top-level format member names this family, at any
// major. JSON member order is irrelevant. This is discovery, not validation:
// a matching header routes even unsupported or subsequently malformed files to
// their reader, which supplies the error instead of silently omitting evidence.
// Encountered I/O errors are returned; an unmatched syntax prefix is false/nil.
func (f Format) IsFile(path string) (bool, error) {
	found := false
	err := visitFileFormats(path, func(name string) bool {
		found = f.Is(name)
		return found
	})
	return found, err
}

// DiscoverEvidence identifies both exploration evidence and run manifests in
// one pass, at any major. A file may name both families through duplicate format
// members; the caller must refuse that ambiguity before selecting a reader.
// I/O errors are returned rather than silently excluding unreadable evidence.
// Syntax is checked by the selected reader; malformed unrelated JSON is skipped.
func DiscoverEvidence(path string) (exploration, manifest bool, err error) {
	err = visitFileFormats(path, func(name string) bool {
		exploration = exploration || Evidence.Is(name)
		manifest = manifest || Manifest.Is(name)
		return exploration && manifest
	})
	return
}

func visitFileFormats(path string, visit func(string) bool) error {
	in, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("discover JSON formats: %w", err)
	}
	defer in.Close()
	if err := visitFormats(in, visit); err != nil {
		return fmt.Errorf("%s: discover JSON formats: %w", path, err)
	}
	return nil
}

func visitFormats(in io.Reader, visit func(string) bool) error {
	// Match encoding/json's reader semantics, including duplicate names and
	// replacement of invalid Unicode. Discovery must not hide a file its reader
	// accepts simply because an unrelated value uses those representations.
	d := jsontext.NewDecoder(in, jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	tok, err := d.ReadToken()
	if err != nil || tok.Kind() != '{' {
		return discoveryError(err)
	}
	for {
		key, err := d.ReadToken()
		if err != nil || key.Kind() == '}' {
			return discoveryError(err)
		}
		// Tokens are invalidated by the next decoder call, including PeekKind.
		isFormat := strings.EqualFold(key.String(), "format")
		if isFormat && d.PeekKind() == '"' {
			value, err := d.ReadToken()
			if err != nil {
				return discoveryError(err)
			}
			if visit(value.String()) {
				return nil
			}
		} else if err := d.SkipValue(); err != nil {
			// Skip nested payloads without retaining or boxing their contents.
			return discoveryError(err)
		}
	}
}

// Unrecognized malformed JSON is not evidence. A recognized header survives a
// later syntax error so its authoritative reader can report it. Actual read
// failures are operational errors, even when no header could yet be read.
func discoveryError(err error) error {
	if _, syntax := errors.AsType[*jsontext.SyntacticError](err); syntax || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return err
}
