package pdflex

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"testing"
)

type testFile struct {
	name string
	md5  string
}

type xrefError struct {
	desc  string
	input string
}

// These hashes are especially important because we're sensitive to \r\n vs \n
// style line endings, which lots of editors like to screw with.

var tfCorrupt = testFile{
	name: "test-corrupt.pdf",
	md5:  "76ae58e2fd8358dc150b899cc228daf6",
}

var tfTruncate = testFile{
	name: "test-truncate.pdf",
	md5:  "826ac13a21386acc7e79ca3c0e44b8c5",
}

var xrClean = `xref
0 1
0000018286 00000 n
trailer
<</Size 111/Root 83 0 R/Info 94 0 R/ID[<CBADA98C42F6D90E286F6A1B3C52084F><F993129E77AB41D9A2951A6AB40174DA>]/Prev 425853 >>
startxref
0
%%EOF`

var fixErrors = []xrefError{
	xrefError{
		input: `xref 0 1
0000018286 00000 n
22 60
trailer
`,
		desc: "no EOL after xref",
	},

	xrefError{
		input: `xref
A B
0000018286 00000 n
2.2 60
trailer
`,
		desc: "invalid first header",
	},

	xrefError{
		input: `xref
0 1
0000018286 QQQQQ n
22 60
trailer
`,
		desc: "invalid first row",
	},

	xrefError{
		input: `xref
0 1
0000018286 00000 n		
22 60
trailer
`,
		desc: "invalid line termination at first row",
	},
}

var headerErrors = []xrefError{

	xrefError{
		input: `xref
0 1
0000018286 00000 n
2.2 60
trailer
`,
		desc: "lexable offset fails Atoi",
	},

	xrefError{
		input: `xref
0 1
0000018286 00000 n
22 6.0
trailer
`,
		desc: "lexable entries fails Atoi",
	},

	xrefError{
		input: `xref
0 1
0000018286 00000 n
22 xyzzy
trailer
`,
		desc: "entries is not a number",
	},

	xrefError{
		input: `xref
0 1
0000018286 00000 n
22 1	Q
trailer
`,
		desc: "<SP> + invalid token after header",
	},

	xrefError{
		input: `xref
0 1
0000018286 00000 n
22 1>>
trailer
`,
		desc: "invalid token after header",
	},

	xrefError{
		input: `xref
0 1
0000018286 00000 n
xyzzy 1
trailer
`,
		desc: "offset is not a number",
	},

	xrefError{
		input: `xref
0 1
0000018286 00000 n
22
60
trailer
`,
		desc: "linebreak after offset",
	},

	xrefError{
		input: `xref
0 1
0000018286 00000 n
trailer`,
		desc: "EOF after trailer",
	},

	xrefError{
		input: `xref
0 1
0000018286 00000 n`,
		desc: "EOF after row",
	},

	xrefError{
		input: `xref
0 1
0000018286 00000 n
`,
		desc: "EOF after row + <EOL>",
	},

	xrefError{
		input: `xref
0 1
0000018286 00000 n
trailer
<</Size 111/Root 83 0 R/Info 94 0 R/ID[<CBADA98C42F6D90E286F6A1B3C52084F><F993129E77AB41D9A2951A6AB40174DA>]/Prev 425853 >>
startxref 0
%%EOF`,
		desc: "no EOL after startxref",
	},

	xrefError{
		input: `xref
0 1
0000018286 00000 n
trailer
<</Size 111/Root 83 0 R/Info 94 0 R/ID[<CBADA98C42F6D90E286F6A1B3C52084F><F993129E77AB41D9A2951A6AB40174DA>]/Prev 425853 >>
startxref
xyzzy
%%EOF`,
		desc: "startxref entry not a number",
	},
}

var rowErrors = []xrefError{
	xrefError{
		input: `xref
0 1
000018286 00000 n
`,
		desc: "short offset",
	},
	xrefError{
		input: `xref
0 1
0000018286
00000 n
trailer
`,
		desc: "linebreak after offset",
	},
	xrefError{
		input: `xref
0 1
0000018286 00000
 n
trailer
`,
		desc: "linebreak after generation",
	},
	xrefError{
		input: `xref
0 1
0000018286 00.00 n
trailer
`,
		desc: "lexable generation fails Atoi",
	},
	xrefError{
		input: `xref
0 1
+000018.86 00000 n
trailer
`,
		desc: "lexable offset fails Atoi",
	},
	xrefError{
		input: `xref
0 1
0000018286 ABCD n
trailer
`,
		desc: "generation not a number",
	},
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

func fix(in []byte) []byte {
	p := Parser{Lexer: NewLexer("", string(in))}
	return p.FixXrefs()
}

func TestCorruptFirstXref(t *testing.T) {
	contents, err := openVerify(tfCorrupt)
	if err != nil {
		t.Fatalf(err.Error())
	}
	contents = fix(contents)

	p := Parser{Lexer: NewLexer("", string(contents))}

	// Find the first xref, make sure the parser is right about LastXref
	if !p.MaybeFindXref() {
		t.Fatalf("failed to find first xref")
	}
	xridx := bytes.Index(contents, []byte("xref"))
	if p.LastXref != xridx || p.LastXref != 16196 {
		t.Fatalf("incorrect index for first xref. Want %d, got %d", p.LastXref, xridx)
	}

	if _, ok := p.Accept(ItemEOL, true); !ok {
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
	if _, ok := p.Accept(ItemEOL, true); !ok {
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

func TestXrefClean(t *testing.T) {
	p := Parser{Lexer: NewLexer("", xrClean)}
	if !p.MaybeFindXref() || p.LastXref != 0 {
		t.Fatalf("failed to find xref")
	}
	p.Accept(ItemEOL, true)
	if !p.MaybeFindHeader() {
		t.Fatalf("failed to find header")
	}
	_, err := p.FindRow()
	if err != nil {
		t.Fatalf("failed to find row")
	}
	if p.MaybeFindHeader() {
		t.Fatalf("shouldn't have found a header")
	}
}

func TestFixXrefs(t *testing.T) {
	for _, fixErr := range fixErrors {
		p := Parser{Lexer: NewLexer("", fixErr.input)}
		out := p.FixXrefs()
		if string(out) != fixErr.input {
			t.Fatalf("broken xref was modified by fix")
		}
	}
}

func TestFindRow(t *testing.T) {
	for _, rowErr := range rowErrors {
		p := Parser{Lexer: NewLexer("", rowErr.input)}
		if !p.MaybeFindXref() || p.LastXref != 0 {
			t.Fatalf("failed to find xref")
		}
		p.Accept(ItemEOL, true)
		if !p.MaybeFindHeader() {
			t.Fatalf("failed to find header")
		}
		_, err := p.FindRow()
		if err == nil {
			t.Fatalf("failed to detect error with %s", rowErr.desc)
		}
	}
}

func TestMaybeFindHeader(t *testing.T) {
	for _, headerErr := range headerErrors {
		p := Parser{Lexer: NewLexer("", headerErr.input)}
		if !p.MaybeFindXref() || p.LastXref != 0 {
			t.Fatalf("failed to find xref")
		}
		p.Accept(ItemEOL, true)
		if !p.MaybeFindHeader() {
			t.Fatalf("failed to find first header")
		}
		_, err := p.FindRow()
		if err != nil {
			t.Fatalf("failed to find row")
		}
		p.Accept(ItemEOL, true)
		if p.MaybeFindHeader() {
			t.Fatalf("failed to detect invalid header with %s", headerErr.desc)
		}
	}
}
