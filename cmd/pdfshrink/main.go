package main

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"flag"
	"fmt"
	"github.com/bnagy/pdflex"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path"
	"strconv"
	"strings"
)

const MAXDATA = 1024

var xref = []byte("xref")
var startxref = []byte("startxref")
var trailer = []byte("trailer")
var pref85 = []byte("<~")
var suff85 = []byte("~>")

var (
	flagStrict *bool = flag.Bool("strict", false, "Abort on xref parsing errors etc")
	flagMax    *int  = flag.Int("max", MAXDATA, "Trim streams whose size is greater than this value")
)

type parseState int

const (
	outside parseState = iota
	inside
	eof
)

// Spec: 7.5.5 - 7.5.6
// Section Header rows are Offset, count
// 'f' is free 'n' is live object
// Object entries are specified as exactly 20 bytes.
// [0-9]{10} [0-9]{5} [fn] \r\n
// 8 1 <- one entry expected, Offset 8
// 0000037413 00000 n <- object 8
// 10 1
// 0000037503 00000 n <- object 10
// 12 2
// 0000037629 00000 n
// 0000037791 00000 n <- object 13
// 15 1
// 0000037931 00000 n
//
// Multiple xref sections can appear in one file, covering from the last %%EOF
// to the %%EOF after the end of the trailer

// Parser represents the state of the input parser
type Parser struct {
	From     int // range of whole input buffer this xref covers
	LastXref int //
	Idx      int // Object Index of the current object
	Offset   int // Header Section Offset
	Entries  int // Number of object entries for this section
	*pdflex.Lexer
	State   parseState
	Scratch bytes.Buffer
}

// Row represents one object entry in an xrefs section
type Row struct {
	Offset     int
	Generation int
	Active     bool
}

// FindXref parses forward until it finds an xref token, emitting all seen
// tokens to scratch. It is responsible for maintaining the 'to' parser member
// which records the start of the most recent xref section and the 'inside'
// struct member which is a sanity check to verify when we think we're in the
// middle of parsing an xrefs.
func (p *Parser) FindXref() bool {
	if p.State == eof {
		return false
	}
	if p.State != outside {
		panic("[BUG] FindXref() called while still in an xref")
	}
	for i := p.NextItem(); i.Typ != pdflex.ItemEOF; i = p.NextItem() {
		p.Scratch.WriteString(i.Val)
		if i.Typ == pdflex.ItemXref {
			p.State = inside
			p.LastXref = int(i.Pos)
			return true
		}
	}
	p.State = eof
	return false
}

// FindRow parses and consumes one object entry in an xref section. It does NOT
// consume the trailing EOL marker. If the row is unable to be parsed, it will
// emit all seen tokens to scratch before returning an error.
func (p *Parser) FindRow() (r Row, e error) {
	// Save the contents of all tokens we evaluate so we can write them out if
	// we have to abort
	bailout := ""

	i, ok := p.CheckToken(pdflex.ItemNumber, false)
	bailout += i.Val
	if !ok || len(i.Val) != 10 {
		e = fmt.Errorf("Corrupt row")
		p.Scratch.WriteString(bailout)
		return
	}
	r.Offset, _ = strconv.Atoi(i.Val)

	i, ok = p.CheckToken(pdflex.ItemSpace, false)
	bailout += i.Val
	if !ok || len(i.Val) != 1 {
		e = fmt.Errorf("Corrupt row")
		p.Scratch.WriteString(bailout)
		return
	}

	i, ok = p.CheckToken(pdflex.ItemNumber, false)
	bailout += i.Val
	if !ok || len(i.Val) != 5 {
		e = fmt.Errorf("Corrupt row")
		p.Scratch.WriteString(bailout)
		return
	}
	r.Generation, _ = strconv.Atoi(i.Val)

	i, ok = p.CheckToken(pdflex.ItemSpace, false)
	bailout += i.Val
	if !ok || len(i.Val) != 1 {
		e = fmt.Errorf("Corrupt row")
		p.Scratch.WriteString(bailout)
		return
	}

	i, ok = p.CheckToken(pdflex.ItemWord, false)
	bailout += i.Val
	if !ok || len(i.Val) != 1 || !(i.Val == "n" || i.Val == "f") {
		e = fmt.Errorf("Corrupt row")
		p.Scratch.WriteString(bailout)
		return
	}
	if i.Val == "n" {
		r.Active = true
	}

	return
}

// CheckToken is used to check the type of the next token, returning the token
// itself and a match boolean. If accept is true the token will be emitted to
// scratch, whether or not the check matches.
func (p *Parser) CheckToken(t pdflex.ItemType, accept bool) (pdflex.Item, bool) {
	i := p.NextItem()
	if accept {
		p.Scratch.WriteString(i.Val)
	}
	return i, i.Typ == t

}

// ResetToHere aborts any xref parsing in progress, sets the xref-related
// state values to -1 and sets 'from' to the current position. This is done so
// that if another xref is encountered later ( which may not be corrupt ) the
// search scope in the raw data will start from wherever the previous xref
// parsing aborted.
func (p *Parser) ResetToHere() {
	p.State = outside
	p.From = int(p.Pos())
	p.LastXref, p.Idx, p.Offset, p.Entries = -1, -1, -1, -1
}

// SeemsLegit is a quick call to make sure none of the xref-related state
// entries are set to their reset values.
func (p *Parser) SeemsLegit() bool {
	return !(p.LastXref < 0 ||
		p.Idx < 0 ||
		p.Offset < 0 ||
		p.Entries < 0)
}

// FindHeader is called directly after an xref token, or after the end of a
// section inside an xref. If tries to find EITHER a header row (Offset count
// EOL) or the trailer keyword. If it finds a trailer it will advance to the
// next %%EOF token, reset the state variables ready to find the next xref
// ( if any ) and then return false.
func (p *Parser) FindHeader() bool {
	if p.State != inside {
		p.ResetToHere()
		return false
	}

	i := p.NextItem()
	p.Scratch.WriteString(i.Val)
	switch i.Typ {
	case pdflex.ItemTrailer:
		// no more headers in this section. Try to find and fix the startxref
		// entry, and then reset to the outside state. Even if there is a
		// missing %%EOF token we're not going to abort or anything...
		for {
			i, bad := p.CheckToken(pdflex.ItemEOF, true)
			if bad {
				p.State = eof
				return false
			}

			if i.Typ == pdflex.ItemStartXref {
				if _, ok := p.CheckToken(pdflex.ItemEOL, true); !ok {
					p.ResetToHere()
					return false
				}

				// don't accept in this call to CheckToken, we might want to write
				// a different number.
				if i, ok := p.CheckToken(pdflex.ItemNumber, false); !ok {
					p.Scratch.WriteString(i.Val)
					p.ResetToHere()
					return false
				}
				p.Scratch.WriteString(fmt.Sprintf("%d", p.LastXref)) // last xref Pos

				// Next tokens should be ItemEOL then ItemComment %%EOF, but
				// we don't actually care, let the general parsing loop emit
				// them.
				p.ResetToHere()
				return false
			}
		}

	case pdflex.ItemNumber:

		p.Offset, _ = strconv.Atoi(i.Val)

		if _, ok := p.CheckToken(pdflex.ItemSpace, true); !ok {
			p.ResetToHere()
			return false
		}

		i, ok := p.CheckToken(pdflex.ItemNumber, true)
		if !ok {
			p.ResetToHere()
			return false
		}
		p.Entries, _ = strconv.Atoi(i.Val)

		if _, ok := p.CheckToken(pdflex.ItemEOL, true); !ok {
			p.ResetToHere()
			return false
		}

		p.Idx = p.Offset
		if !p.SeemsLegit() {
			panic("BUG: logic broken in FindHeader")
		}

		return true

	case pdflex.ItemEOF:
		p.State = eof
		return false
	default:
		// we assume that this was a truncated xref section or something, so
		// we'll report no header row found, but still set the "from" index.
		// That means that if there's another xref later the search scope will
		// be (hopefully correctly) from the end of this truncated / corrupt
		// xrefs to the start of the next one.
		p.ResetToHere()
		return false
	}

}

// FixXrefs is the main parsing loop. Essentially it seeks to an xref token,
// then loops through parsing the xref header rows and object entry rows. When
// no more xref tokens are found runs through until the end of the file.
func (p *Parser) FixXrefs(in []byte) []byte {
mainLoop:
	for {

		found := p.FindXref()
		if !found {
			if p.State != eof {
				// just checking...
				panic("[BUG] No xref found but not at EOF!")
			}
			return p.Scratch.Bytes()
		}

		if _, ok := p.CheckToken(pdflex.ItemEOL, true); !ok {
			p.ResetToHere()
			continue mainLoop
		}
		// found a new xref section now ( this label just for clarity )
		for p.FindHeader() {
			// found a header - from, to, idx, Offset are set
			for i := 0; i < p.Entries; i++ {

				row, err := p.FindRow()
				if err != nil {
					p.ResetToHere()
					continue mainLoop
				}

				if row.Active {
					objOffset := locateObj(in[p.From:p.LastXref], p.Idx+i)
					// no matching object, emit the row unmodified
					if objOffset < 0 {
						objOffset = row.Offset
					} else {
						// If we found it in a subslice, add the from index to
						// get the true index from the start of the input.
						objOffset += p.From
					}
					p.Scratch.WriteString(fmt.Sprintf("%.10d %.5d n", objOffset, row.Generation))
				} else {
					p.Scratch.WriteString(fmt.Sprintf("%.10d %.5d f", row.Offset, row.Generation))

				}

				// Correct line terminators are: SP CR, SP LF, or CRLF
				// This makes a correct line exactly 20 bytes.
				// Spec section 7.5.4 p 41
				i, ok := p.CheckToken(pdflex.ItemEOL, true)
				if ok && len(i.Val) == 2 {
					// CRLF - done with this line
					continue
				}
				if i.Typ == pdflex.ItemSpace && len(i.Val) == 1 {
					// not CRLF, but it was SP ...still OK
					if j, ok := p.CheckToken(pdflex.ItemEOL, true); ok && len(j.Val) == 1 {
						// single CR or LF - all is well.
						continue
					}
				}

				// line is invalid, bail.
				p.ResetToHere()
				continue mainLoop
			}
		}
		p.ResetToHere()
	}
}

func locateObj(in []byte, i int) int {
	idx := bytes.Index(in, []byte(fmt.Sprintf("\n%d 0 obj", i)))
	if idx < 0 {
		idx = bytes.Index(in, []byte(fmt.Sprintf("\r%d 0 obj", i)))
		if idx < 0 {
			return idx
		}
	}
	// We found something. Add 1 to the offset so the index is ahead of the \n
	// or \r
	return idx + 1
}

func fix(in []byte) []byte {
	p := Parser{Lexer: pdflex.NewLexer("", string(in))}
	return p.FixXrefs(in)
}

func inflate(s string) (string, error) {

	in := strings.NewReader(s)
	decom, err := zlib.NewReader(in)
	if err != nil {
		return "", err
	}

	var b bytes.Buffer
	_, err = io.Copy(&b, decom)
	decom.Close()

	return b.String(), err
}

func deflate(s string) (string, error) {

	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	_, err := w.Write([]byte(s))
	w.Close()
	if err != nil {
		return "", err
	}

	return b.String(), nil
}

func un85(s string) (string, error) {

	in := []byte(s)
	// Caller is expected to trim <~ ~> if present
	in = bytes.TrimPrefix(in, pref85)
	in = bytes.TrimSuffix(in, suff85)
	out := make([]byte, 0, len(in))

	n, _, err := ascii85.Decode(out, in, true)
	if err != nil {
		return "", err
	}

	return string(out[:n]), nil
}

func re85(s string) (string, error) {
	var b bytes.Buffer
	w := ascii85.NewEncoder(&b)
	_, err := w.Write([]byte(s))
	w.Close()
	if err != nil {
		return "", err
	}
	return b.String(), nil
}

func main() {

	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			"  Usage: %s file [file file ...]\n",
			path.Base(os.Args[0]),
		)
	}

	flag.Parse()
	if *flagMax < 0 {
		flag.Usage()
		os.Exit(1)
	}

	for _, arg := range flag.Args() {

		raw, err := ioutil.ReadFile(arg)
		if err != nil {
			os.Exit(1)
		}

		l := pdflex.NewLexer(arg, string(raw))
		var out bytes.Buffer
		zipped := false
		asc85 := false
		for i := l.NextItem(); i.Typ != pdflex.ItemEOF; i = l.NextItem() {
			if i.Typ == pdflex.ItemStreamBody {
				s := i.Val

				if asc85 {
					s, err = un85(s)
					if err != nil {
						log.Fatalf("[STRICT] Failed to un85: %s", err)
					}
				}

				if zipped {
					s2, err := inflate(s)
					if err != nil && *flagStrict {
						log.Fatalf("[STRICT] Error unzipping internal stream: %s", err)
					}
					// If not strict, we ignore any errors here. If it's
					// unexpected EOF we'll get partial unzipped data, so use
					// that for truncation. Other errors will read a zero
					// length string, in which case we fall back to truncating
					// the original (corrupt) zipped stream.
					if len(s2) > 0 {
						s = s2
					}
				}

				if len(s) > *flagMax {
					s = s[:*flagMax]
				} else {
					// write the original string
					out.WriteString(i.Val)
					zipped = false
					asc85 = false
					continue
				}

				if zipped {
					s, err = deflate(s)
					if err != nil {
						// should never happen, strict mode or not
						log.Fatalf("Error zipping truncated string: %s", err)
					}
				}
				if asc85 {
					s, err = re85(s)
					if err != nil {
						// ditto
						log.Fatalf("Error Ascii85ing zipped string: %s", err)
					}
				}
				out.WriteString(s)
				zipped = false
				asc85 = false

			} else {

				if i.Typ == pdflex.ItemName && i.Val == "/FlateDecode" {
					zipped = true
				}
				if i.Typ == pdflex.ItemName && i.Val == "/ASCII85Decode" {
					asc85 = true
				}
				out.WriteString(i.Val)
			}

			if i.Typ == pdflex.ItemError {
				break
			}
		}

		fixed := fix(out.Bytes())
		newfn := strings.TrimSuffix(path.Base(arg), path.Ext(arg)) + "-small" + path.Ext(arg)
		newfn = path.Join(path.Dir(arg), newfn)
		err = ioutil.WriteFile(newfn, fixed, 0600)
		if err != nil {
			log.Fatalf("Unable to write %s: %s", newfn, err)
		}
	}

}
