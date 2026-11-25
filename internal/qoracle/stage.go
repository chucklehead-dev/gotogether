package qoracle

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A Program is a corpus entry ready to run: its main file and the files
// it imports, by path.
type Program struct {
	ID string
	// File is the main file's absolute path.
	File string
	// Text, if set, replaces File's contents (a generated or shrunk
	// program at File's place, so its relative imports resolve).
	Text string
	// Main, Init, Step and Invariants are compile's; no Main, no compile.
	Main, Init, Step string
	Invariants       []string
}

var importFrom = regexp.MustCompile(`from\s*"([^"]+)"`)

// stripComments is Quint text without its comments (and with string
// literals kept), for scanning imports and constructs.
func stripComments(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' && s[j] != '\n' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				j = len(s) - 1
			}
			b.WriteString(s[i : j+1])
			i = j
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			// keep the lines, so positions found in the result stay on theirs
			b.WriteString(strings.Repeat("\n", strings.Count(s[i:i+2+end+2], "\n")))
			i += end + 3
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// closure is file and every file it imports, transitively, by `from`:
// absolute paths, file first. An import that names no file is skipped
// (quint reports it, QNT013).
func closure(file, text string) []string {
	out := []string{file}
	seen := map[string]bool{file: true}
	for i := 0; i < len(out); i++ {
		src := text
		if i > 0 || text == "" {
			b, err := os.ReadFile(out[i])
			if err != nil {
				continue
			}
			src = string(b)
		}
		for _, m := range importFrom.FindAllStringSubmatch(stripComments(src), -1) {
			p := m[1]
			if filepath.Ext(p) != ".qnt" {
				p += ".qnt"
			}
			if !filepath.IsAbs(p) {
				p = filepath.Join(filepath.Dir(out[i]), p)
			}
			if seen[p] {
				continue
			}
			if _, err := os.Stat(p); err != nil {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// stage copies a program's files into dir, each at its path relative to
// their common directory: two front ends run on equal copies, their
// outputs naming files relative to dir. It returns the main file's copy.
func stage(p Program, dir string) (string, error) {
	files := closure(p.File, p.Text)
	common := filepath.Dir(files[0])
	for _, f := range files[1:] {
		for !strings.HasPrefix(f, common+string(filepath.Separator)) && common != filepath.Dir(common) {
			common = filepath.Dir(common)
		}
	}
	var mainCopy string
	for i, f := range files {
		rel, err := filepath.Rel(common, f)
		if err != nil {
			return "", err
		}
		dst := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		var data []byte
		if i == 0 && p.Text != "" {
			data = []byte(p.Text)
		} else if data, err = os.ReadFile(f); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return "", err
		}
		if i == 0 {
			mainCopy = dst
		}
	}
	return mainCopy, nil
}
