package pdflex

import (
	"bytes"
	"testing"
)

var pdf = `%PDF-1.1
%¥±ë

1 0 obj
  << /Type /Catalog
     /Pages 2 0 R
  >>
endobj

2 0 obj
  << /Type /Pages
     /Kids [3 0 R]
     /Count 1
     /MediaBox [0 0 300 144]
  >>
endobj

3 0 obj
  <<  /Type /Page
      /Parent 2 0 R
      /Resources
       << /Font
           << /F1
               << /Type /Font
                  /Subtype /Type1
                  /BaseFont /Times-Roman
               >>
           >>
       >>
      /Contents 4 0 R
  >>
endobj

4 0 obj
  << /Length 55 >>
stream
  BT
    /F1 18 Tf
    0 0 Td
    (Hello World) Tj
  ET
endstream
endobj

xref
0 5
0000000000 65535 f 
0000000018 00000 n 
0000000077 00000 n 
0000000178 00000 n 
0000000457 00000 n 
trailer
  <<  /Root 1 0 R
      /Size 5
  >>
startxref
565
%%EOF
`

var escapedSlash = `/Author (Fred Nerk\\\\)`
var unterminatedDict = `/Author (Fred Nerk)<<`
var extraDictTerminator = `/Author (Fred Nerk)>>`
var unterminatedArray = `/Author (Fred Nerk)[`
var extraArrayTerminator = `/Author (Fred Nerk)]`

func TestRewrite(t *testing.T) {
	l := NewLexer("test", pdf)
	var b bytes.Buffer
	for i := l.NextItem(); i.Typ != ItemEOF; i = l.NextItem() {
		b.WriteString(i.Val)
	}
	if b.String() != pdf {
		t.Fatalf("Failed in rewrite - strings not equal")
	}
}

func TestEscapedSlash(t *testing.T) {
	l := NewLexer("test", escapedSlash)
	var toks []string
	for i := l.NextItem(); i.Typ != ItemEOF; i = l.NextItem() {
		toks = append(toks, i.Val)
	}
	if toks[2] != `(Fred Nerk\\\\)` {
		t.Fatalf("failed to parse escaped backslash")
	}
}

func TestUnterminatedDict(t *testing.T) {
	l := NewLexer("test", unterminatedDict)
	var toks []string
	for i := l.NextItem(); i.Typ != ItemEOF; i = l.NextItem() {
		toks = append(toks, i.Val)
	}
	if toks[4] != "unterminated dict" {
		t.Logf("%q ", toks)
		t.Fatalf("failed to recognise unterminated dict")
	}
}

func TestUnterminatedArray(t *testing.T) {
	l := NewLexer("test", unterminatedArray)
	var toks []string
	for i := l.NextItem(); i.Typ != ItemEOF; i = l.NextItem() {
		toks = append(toks, i.Val)
	}
	if toks[4] != "unterminated array" {
		t.Logf("%q ", toks)
		t.Fatalf("failed to recognise unterminated array")
	}
}

func TestExtraDictTerminator(t *testing.T) {
	l := NewLexer("test", extraDictTerminator)
	var toks []string
	for i := l.NextItem(); i.Typ != ItemEOF; i = l.NextItem() {
		toks = append(toks, i.Val)
	}
	if toks[4] != "unexexpected dict terminator" {
		t.Logf("%q ", toks)
		t.Fatalf("failed to recognise unexpected dict terminator")
	}
}

func TestExtraArrayTerminator(t *testing.T) {
	l := NewLexer("test", extraArrayTerminator)
	var toks []string
	for i := l.NextItem(); i.Typ != ItemEOF; i = l.NextItem() {
		toks = append(toks, i.Val)
	}
	if toks[4] != "unexexpected array terminator" {
		t.Logf("%q ", toks)
		t.Fatalf("failed to recognise unexpected array terminator")
	}
}
