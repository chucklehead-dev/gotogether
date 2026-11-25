package syntax

import (
	"slices"

	"github.com/chucklehead-dev/gotogether/internal/quintnames"
)

// TokKind is a token's class, as Quint.g4's lexer rules make them.
type TokKind int

// The token classes. A keyword or a punctuation token is told apart by
// its text (Tok.Text): Quint.g4's literal tokens.
const (
	TEOF TokKind = iota
	TLowID
	TCapID
	TInt
	TString
	TBool
	TDoc
	THashbang
	TKeyword
	TPunct
)

// A Tok is a token: its class, its text, and where it is (Line 1-based
// and Col 0-based as ANTLR's tokens; Start and Stop the indices of its
// first and last characters, code points, Stop inclusive).
type Tok struct {
	Kind        TokKind
	Text        string
	Line, Col   int
	Start, Stop int
}

// keywords are Quint.g4's literal tokens that look like identifiers: an
// identifier of this text is the keyword (ANTLR gives a literal token
// priority over LOW_ID and CAP_ID of the same length). They are
// internal/quintnames' Keywords, which its tests check against quint's
// lexer, less the BOOL token's true and false, and the literal tokens
// that are built-ins' names (and, or, iff, implies, leadsTo, Set, List),
// with from and as (read-only).
var keywords = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range quintnames.Keywords {
		if k != "true" && k != "false" {
			m[k] = true
		}
	}
	for _, k := range []string{"and", "or", "iff", "implies", "leadsTo", "Set", "List", "from", "as"} {
		m[k] = true
	}
	return m
}()

// puncts are Quint.g4's punctuation tokens, longest first (read-only).
var puncts = []string{
	"...", "::", "=>", "->", ">=", "<=", "!=", "==",
	"{", "}", "(", ")", "[", "]", ",", ";", ":", ".", "|", "'", "=", "+", "-", "*", "/", "%", ">", "<", "^", "_",
}

// A LexError is a character no token starts with: ANTLR's "token
// recognition error", at the start of the text it could not read.
type LexError struct {
	Line, Col, Index int
	Text             string
}

// Lex cuts src into tokens as Quint.g4's lexer does (whitespace and
// comments skipped; DOCCOMMENT and HASHBANG_LINE kept), ending with an
// EOF token. A character no rule reads is a LexError and is skipped, as
// ANTLR's lexer skips it.
func Lex(src string) ([]Tok, []LexError) {
	l := &lexer{rs: []rune(src), line: 1}
	return l.run()
}

type lexer struct {
	rs        []rune
	i         int
	line, col int
	toks      []Tok
	errs      []LexError
}

func (l *lexer) at(k int) rune {
	if l.i+k < len(l.rs) {
		return l.rs[l.i+k]
	}
	return 0
}

func (l *lexer) has(k int) bool { return l.i+k < len(l.rs) }

// advance moves past n characters, counting lines as ANTLR does (on '\n').
func (l *lexer) advance(n int) {
	for ; n > 0; n-- {
		if l.rs[l.i] == '\n' {
			l.line++
			l.col = 0
		} else {
			l.col++
		}
		l.i++
	}
}

func (l *lexer) emit(kind TokKind, n int) {
	t := Tok{Kind: kind, Text: string(l.rs[l.i : l.i+n]), Line: l.line, Col: l.col, Start: l.i, Stop: l.i + n - 1}
	l.toks = append(l.toks, t)
	l.advance(n)
}

// until is the length of the text from the cursor through the first
// occurrence of end after skip characters (end included), or -1.
func (l *lexer) until(skip int, end string) int {
	e := []rune(end)
	for j := l.i + skip; j+len(e) <= len(l.rs); j++ {
		if slices.Equal(l.rs[j:j+len(e)], e) {
			return j + len(e) - l.i
		}
	}
	return -1
}

func isLow(r rune) bool   { return 'a' <= r && r <= 'z' }
func isCap(r rune) bool   { return 'A' <= r && r <= 'Z' }
func isDigit(r rune) bool { return '0' <= r && r <= '9' }
func isHex(r rune) bool {
	return isDigit(r) || ('a' <= r && r <= 'f') || ('A' <= r && r <= 'F')
}
func isIDChar(r rune) bool { return isLow(r) || isCap(r) || isDigit(r) || r == '_' }

func (l *lexer) run() ([]Tok, []LexError) {
	for l.i < len(l.rs) {
		r := l.at(0)
		switch {
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			l.advance(1)
		case r == '#' && l.at(1) == '!' && l.has(1):
			if n := l.until(2, "\n"); n > 0 {
				l.emit(THashbang, n)
			} else {
				l.fail()
			}
		case r == '/' && l.at(1) == '/' && l.has(1):
			// DOCCOMMENT and LINE_COMMENT end at a newline; with none,
			// neither matches and the slashes are DIV tokens
			n := l.until(2, "\n")
			switch {
			case n < 0:
				l.emit(TPunct, 1)
			case l.at(2) == '/' && l.has(2):
				l.emit(TDoc, n)
			default:
				l.advance(n)
			}
		case r == '/' && l.at(1) == '*' && l.has(1):
			if n := l.until(2, "*/"); n > 0 {
				l.advance(n)
			} else {
				l.emit(TPunct, 1)
			}
		case r == '"':
			if n := l.until(1, "\""); n > 0 {
				l.emit(TString, n)
			} else {
				l.fail()
			}
		case isDigit(r):
			l.emit(TInt, l.intLen())
		case isLow(r) || isCap(r) || (r == '_' && isIDChar(l.at(1)) && l.has(1)):
			n := 1
			for l.has(n) && isIDChar(l.at(n)) {
				n++
			}
			text := string(l.rs[l.i : l.i+n])
			switch {
			case text == "true" || text == "false":
				l.emit(TBool, n)
			case keywords[text]:
				l.emit(TKeyword, n)
			case isCap(r):
				l.emit(TCapID, n)
			default:
				l.emit(TLowID, n)
			}
		default:
			matched := false
			for _, p := range puncts {
				if l.hasPrefix(p) {
					l.emit(TPunct, len(p))
					matched = true
					break
				}
			}
			if !matched {
				l.fail()
			}
		}
	}
	l.toks = append(l.toks, Tok{Kind: TEOF, Text: "<EOF>", Line: l.line, Col: l.col, Start: l.i, Stop: l.i - 1})
	return l.toks, l.errs
}

func (l *lexer) hasPrefix(p string) bool {
	for k, c := range p {
		if !l.has(k) || l.at(k) != c {
			return false
		}
	}
	return true
}

// intLen is INT's longest match at the cursor: 0, a decimal with '_'
// between digits, or 0x and hex digits so separated.
func (l *lexer) intLen() int {
	if l.at(0) == '0' {
		if l.at(1) == 'x' && l.has(2) && isHex(l.at(2)) {
			n := 3
			for {
				if l.has(n) && isHex(l.at(n)) {
					n++
				} else if l.at(n) == '_' && l.has(n+1) && isHex(l.at(n+1)) {
					n += 2
				} else {
					return n
				}
			}
		}
		return 1
	}
	n := 1
	for {
		if l.has(n) && isDigit(l.at(n)) {
			n++
		} else if l.at(n) == '_' && l.has(n+1) && isDigit(l.at(n+1)) {
			n += 2
		} else {
			return n
		}
	}
}

// fail is a character no rule reads: reported, and skipped.
// ANTLR's lexer reads as far as some token could go, and on failing
// skips what it read and the character it failed at: an unclosed string
// or a #! line without a newline skips the rest of the text, a '!' or a
// '#' the character after it as well.
func (l *lexer) fail() {
	n := 1
	switch r := l.at(0); {
	case r == '"' || (r == '#' && l.at(1) == '!' && l.has(1)):
		n = len(l.rs) - l.i
	case (r == '!' || r == '#') && l.has(1):
		n = 2
	}
	l.errs = append(l.errs, LexError{Line: l.line, Col: l.col, Index: l.i, Text: string(l.rs[l.i : l.i+n])})
	l.advance(n)
}

// IsKeyword says whether s lexes as a keyword token.
func IsKeyword(s string) bool { return keywords[s] }

// reserved are the keywords an identifier may be, with QNT008 (Quint.g4's
// `reserved`); keywordAsID those it may be freely (read-only).
var (
	reserved    = []string{"and", "or", "iff", "implies", "leadsTo", "match", "import", "export"}
	keywordAsID = []string{"from", "as", "Set", "List"}
)

func isReserved(t Tok) bool { return t.Kind == TKeyword && slices.Contains(reserved, t.Text) }
func isKeywordAsID(t Tok) bool {
	return t.Kind == TKeyword && slices.Contains(keywordAsID, t.Text)
}
