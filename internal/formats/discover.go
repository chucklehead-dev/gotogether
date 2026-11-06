package formats

import (
	"encoding/json"
	"os"
	"strings"
)

// IsFile reports whether a top-level format member names this family, at any
// major. JSON member order is irrelevant. This is discovery, not validation:
// a matching header routes even unsupported or subsequently malformed files to
// their reader, which supplies the error instead of silently omitting evidence.
func (f Format) IsFile(path string) bool {
	in, err := os.Open(path)
	if err != nil {
		return false
	}
	defer in.Close()
	d := json.NewDecoder(in)
	d.UseNumber() // unrelated values need not fit float64
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return false
		}
		value, err := d.Token()
		if err != nil {
			return false
		}
		if name, ok := value.(string); strings.EqualFold(key.(string), "format") && ok && f.Is(name) {
			return true
		}
		// Stream past unrelated values rather than retaining whole ITF states
		// or treating nested format members as the document's discriminator.
		depth := 0
		if value == json.Delim('{') || value == json.Delim('[') {
			depth = 1
		}
		for depth > 0 {
			tok, err := d.Token()
			if err != nil {
				return false
			}
			switch tok {
			case json.Delim('{'), json.Delim('['):
				depth++
			case json.Delim('}'), json.Delim(']'):
				depth--
			}
		}
	}
	return false
}
