package syntax

import (
	"fmt"
	"strings"
	"testing"
)

func kinds(toks []Tok) string {
	var b []string
	for _, t := range toks {
		switch t.Kind {
		case TEOF:
			b = append(b, "EOF")
		case TLowID:
			b = append(b, "low:"+t.Text)
		case TCapID:
			b = append(b, "cap:"+t.Text)
		case TInt:
			b = append(b, "int:"+t.Text)
		case TString:
			b = append(b, "str:"+t.Text)
		case TBool:
			b = append(b, "bool:"+t.Text)
		case TDoc:
			b = append(b, "doc")
		case THashbang:
			b = append(b, "hashbang")
		case TKeyword:
			b = append(b, "kw:"+t.Text)
		case TPunct:
			b = append(b, t.Text)
		}
	}
	return strings.Join(b, " ")
}

// The lexer cuts as Quint.g4's: literal tokens before identifiers of the
// same text, the longest match, comments skipped, doc comments kept.
func TestLex(t *testing.T) {
	cases := []struct{ src, want string }{
		{"val values valx", "kw:val low:values low:valx EOF"},
		{"_ _x _1 x_", "_ low:_x low:_1 low:x_ EOF"},
		{"Set List from as and or match import", "kw:Set kw:List kw:from kw:as kw:and kw:or kw:match kw:import EOF"},
		{"true false trueish", "bool:true bool:false low:trueish EOF"},
		{"0 0x1F 1_000 0123 1__0 0x", "int:0 int:0x1F int:1_000 int:0 int:123 int:1 low:__0 int:0 low:x EOF"},
		{`"a b" "x\ny"`, `str:"a b" str:"x\ny" EOF`},
		{"a // c\nb /* d\n */ c", "low:a low:b low:c EOF"},
		{"/// doc\n//// also doc\nx", "doc doc low:x EOF"},
		{"x // no newline", "low:x / / low:no low:newline EOF"},
		{"x /* open", "low:x / * low:open EOF"},
		{"... .. :: : => -> >= > <= < != == = '", "... . . :: : => -> >= > <= < != == = ' EOF"},
		{"#!/usr/bin/env quint\nmodule", "hashbang kw:module EOF"},
		{"x'", "low:x ' EOF"},
	}
	for _, c := range cases {
		toks, errs := Lex(c.src)
		if len(errs) > 0 {
			t.Errorf("Lex(%q): errors %v", c.src, errs)
		}
		if got := kinds(toks); got != c.want {
			t.Errorf("Lex(%q):\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

// A character no rule reads is an error at its position, and skipped,
// with what ANTLR read trying it: the character after a '!', the rest of
// the text after a string's quote that never closes.
func TestLexErrors(t *testing.T) {
	toks, errs := Lex("a\n  @ b !c d \"open\nx")
	if got := kinds(toks); got != "low:a low:b low:d EOF" {
		t.Errorf("tokens: %s", got)
	}
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Text))
	}
	if want := []string{"2:2 @", "2:6 !c", "2:11 \"open\nx"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("errors %v, want %v", got, want)
	}
}

// Positions are ANTLR's: lines from 1, columns and indices from 0, in
// code points, the stop index inclusive.
func TestLexPositions(t *testing.T) {
	toks, _ := Lex("é x\n  yz")
	want := []Tok{
		{Kind: TLowID, Text: "x", Line: 1, Col: 2, Start: 2, Stop: 2},
		{Kind: TLowID, Text: "yz", Line: 2, Col: 2, Start: 6, Stop: 7},
	}
	// é is no token: an error, skipped
	if len(toks) != 3 {
		t.Fatalf("tokens %v", toks)
	}
	for i, w := range want {
		if toks[i] != w {
			t.Errorf("token %d: %+v, want %+v", i, toks[i], w)
		}
	}
	if eof := toks[2]; eof.Kind != TEOF || eof.Start != 8 || eof.Stop != 7 || eof.Line != 2 || eof.Col != 4 {
		t.Errorf("EOF %+v", eof)
	}
}
