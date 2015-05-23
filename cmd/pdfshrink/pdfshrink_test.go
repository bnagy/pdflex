package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"github.com/bnagy/pdflex"
	"io"
	"io/ioutil"
	"os"
	"testing"
)

type testFile struct {
	name string
	md5  string
}

// These hashes are especially important because we're sensitive to \r\n vs \n
// style line endings, which lots of editors like to screw with.

var tfUnmodified = testFile{
	name: "test-unmodified.pdf",
	md5:  "c0a7b4f6575620dc3f970fb9a7c7bc94",
}

var tfCorrupt = testFile{
	name: "test-corrupt.pdf",
	md5:  "76ae58e2fd8358dc150b899cc228daf6",
}

var tfTruncate = testFile{
	name: "test-truncate.pdf",
	md5:  "826ac13a21386acc7e79ca3c0e44b8c5",
}

// This has one xref row hex-edited to end with <SP><LF> to increase coverage
var tf85 = testFile{
	name: "test-85.pdf",
	md5:  "fa7e8078b43b17c6b79deabb8f143ca2",
}

func openVerify(tf testFile) ([]byte, error) {

	fr, err := os.Open(tf.name)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s", tf.name)
	}

	md5 := md5.New()
	tr := io.TeeReader(fr, md5)
	contents, err := ioutil.ReadAll(tr)
	if err != nil {
		return nil, fmt.Errorf("failed to read from %s: %s", tf.name, err)
	}

	hsh := hex.EncodeToString(md5.Sum(nil))
	if hsh != tf.md5 {
		return nil, fmt.Errorf("validation for %s failed: want MD5 %s, got %s", tf.name, tf.md5, hsh)
	}

	return contents, nil
}

func TestRewrite(t *testing.T) {
	contents, err := openVerify(tfUnmodified)
	if err != nil {
		t.Fatalf(err.Error())
	}
	fixed := fix(contents)
	for i, b := range fixed {
		if b != contents[i] {
			t.Fatalf("%s was modified during fix()", tfUnmodified.name)
		}
	}
}

func TestCorruptFirstXref(t *testing.T) {
	contents, err := openVerify(tfCorrupt)
	if err != nil {
		t.Fatalf(err.Error())
	}
	contents = fix(contents)

	p := Parser{Lexer: pdflex.NewLexer("", string(contents))}

	// Find the first xref, make sure the parser is right about LastXref
	if !p.MaybeFindXref() {
		t.Fatalf("failed to find first xref")
	}
	xridx := bytes.Index(contents, []byte("xref"))
	if p.LastXref != xridx || p.LastXref != 16196 {
		t.Fatalf("incorrect index for first xref. Want %d, got %d", p.LastXref, xridx)
	}

	if _, ok := p.CheckToken(pdflex.ItemEOL, true); !ok {
		t.Fatalf("Missing EOL after xref token")
	}

	// Find the first Header
	if !p.MaybeFindHeader() {
		t.Fatalf("failed to find first header")
	}

	// Hardcoded
	if p.Entries != 97 || p.Offset != 0 {
		t.Fatalf(
			"incorrect values for header. Want Offset 0, "+
				"Entries 97, got Offset %d, Entries %d",
			p.Offset,
			p.Entries,
		)
	}

	r, e := p.FindRow()
	if e != nil || r.Generation != 65535 {
		t.Fatalf("unexpected first row %#v", r)
	}
	p.Scratch.WriteString(fmt.Sprintf("%.10d %.5d f", r.Offset, r.Generation))

	if r, e := p.FindRow(); e == nil {
		t.Fatalf("failed to error on corrupt row %#v", r)
	}
	p.ResetToHere()

	// Find the second xref, make sure the parser is right about LastXref
	if !p.MaybeFindXref() {
		t.Fatalf("failed to find second xref")
	}
	if _, ok := p.CheckToken(pdflex.ItemEOL, true); !ok {
		t.Fatalf("failed to find EOL")
	}
	if p.LastXref != 21619 {
		t.Fatalf("incorrect index for second xref. Want 21619, got %d", p.LastXref)
	}
	if !p.MaybeFindHeader() {
		t.Fatalf("failed to find second header")
	}
	// Hardcoded
	if p.Entries != 1 || p.Offset != 4 {
		t.Fatalf(
			"incorrect values for header. Want Offset 4, "+
				"Entries 1, got Offset %d, Entries %d",
			p.Offset,
			p.Entries,
		)
	}

	// Both of the startxref values have been manually edited to 12345. The
	// first one should be unmodified because the parsing bails out at the
	// first corrupt row. The second one should still have been corrected.
	first := bytes.Index(contents, []byte("startxref"))
	wantFirst := "startxref\r12345"
	gotFirst := string(contents[first : first+len(wantFirst)])
	if gotFirst != wantFirst {
		t.Fatalf("unexpected value at first startxref, want %q, got %q", wantFirst, gotFirst)
	}

	second := bytes.LastIndex(contents, []byte("startxref"))
	wantSecond := "startxref\r21619"
	gotSecond := string(contents[second : second+len(wantSecond)])
	if gotSecond != wantSecond {
		t.Fatalf("unexpected value at second startxref, want %q, got %q", wantSecond, gotSecond)
	}
}

func TestTruncate(t *testing.T) {
	contents, err := openVerify(tfTruncate)
	if err != nil {
		t.Fatalf(err.Error())
	}
	contents = fix(contents)
	// This is set to "9999999999 00000 n\r\n" in the testfile
	want := "0000021142 00000 n\r\n"
	got := string(contents[len(contents)-len(want):])
	if want != got {
		t.Fatalf("failed to fix xref row, want %q got %q", want, got)
	}
}

func TestShrink(t *testing.T) {
	contents, err := openVerify(tf85)
	if err != nil {
		t.Fatalf(err.Error())
	}
	shrink128, err := shrink(contents, 128) // should be a noop
	for i, b := range shrink128 {
		if b != contents[i] {
			t.Fatalf("%s was modified during shrink()", tf85.name)
		}
	}

	shrink127, err := shrink(contents, 127) // should shrink
	if err != nil {
		t.Fatalf("error while shrinking: %s", err)
	}

	shrink127 = fix(shrink127)
	idx := bytes.LastIndex(shrink127, []byte("startxref"))
	want := "startxref\r55370"
	got := string(shrink127[idx : idx+len(want)])
	if got != want {
		t.Fatalf("unexpected value at startxref, want %q, got %q", want, got)
	}
}
