package main

import (
	"bytes"
	"fmt"
	"github.com/bnagy/pdflex"
	"strconv"
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
// [0-9]{10} [0-9]{5} [fn]\r\n
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

// MaybeFindXref parses forward until it finds an xref token, emitting all seen
// tokens to scratch. It is responsible for maintaining the 'LastXref' parser member
// which records the start of the most recent xref section and the 'State'
// struct member which is a sanity check to verify when we think we're in the
// middle of parsing an xrefs.
func (p *Parser) MaybeFindXref() bool {
	if p.State == eof {
		return false
	}
	if p.State != outside {
		panic("[BUG] MaybeFindXref() called while still in an xref")
	}
	for i := p.NextItem(); i.Typ != pdflex.ItemEOF; i = p.NextItem() {
		p.Scratch.WriteString(i.Val)
		if i.Typ == pdflex.ItemXref {
			p.State = inside
			// FIXED - make sure to use the index of the xref in Scratch, not
			// in the shrunk input buffer, because when you change the
			// "startxref\rNNNNNNN" string size they get out of sync in files
			// with multiple xref sections
			p.LastXref = p.Scratch.Len() - len(i.Val)
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
	// Cache the contents of all tokens we evaluate so we can write them out if
	// we have to abort
	bailout := ""

	i, ok := p.CheckToken(pdflex.ItemNumber, false)

	bailout += i.Val
	if !ok || len(i.Val) != 10 {
		e = fmt.Errorf("corrupt row - want 10 digit offset, got %#v", i)
		p.Scratch.WriteString(bailout)
		return
	}
	r.Offset, e = strconv.Atoi(i.Val)
	if e != nil {
		// Still need to handle errors - something like +12.5 will pass the
		// lexer, but not Atoi
		e = fmt.Errorf("corrupt row - want 10 digit offset, got %#v", i)
		return
	}

	i, ok = p.CheckToken(pdflex.ItemSpace, false)
	bailout += i.Val
	if !ok || len(i.Val) != 1 {
		e = fmt.Errorf("corrupt row - want ItemSpace, got %#v", i)
		p.Scratch.WriteString(bailout)
		return
	}

	i, ok = p.CheckToken(pdflex.ItemNumber, false)
	bailout += i.Val
	if !ok || len(i.Val) != 5 {
		e = fmt.Errorf("corrupt row - want 5 digit generation, got %#v", i)
		p.Scratch.WriteString(bailout)
		return
	}
	r.Generation, e = strconv.Atoi(i.Val)
	if e != nil {
		e = fmt.Errorf("corrupt row - 5 digit generation, got %#v", i)
		return
	}

	i, ok = p.CheckToken(pdflex.ItemSpace, false)
	bailout += i.Val
	if !ok || len(i.Val) != 1 {
		e = fmt.Errorf("corrupt row - want ItemSpace, got %#v", i)
		p.Scratch.WriteString(bailout)
		return
	}

	i, ok = p.CheckToken(pdflex.ItemWord, false)
	bailout += i.Val
	if !ok || len(i.Val) != 1 || !(i.Val == "n" || i.Val == "f") {
		e = fmt.Errorf("corrupt row - want [nf], got %#v", i)
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
	if i.Typ == pdflex.ItemEOF {
		p.State = eof
	}
	return i, i.Typ == t

}

// ResetToHere aborts any xref parsing in progress, sets the xref-related
// state values to -1 and sets 'from' to the current position. This is done so
// that if another xref is encountered later ( which may not be corrupt ) the
// search scope in the raw data will start from wherever the previous xref
// parsing aborted.
func (p *Parser) ResetToHere() {
	// If we've reached EOF don't touch the state any more so that other
	// functions can detect it and abort.
	if p.State != eof {
		p.State = outside
	}
	p.From = p.Scratch.Len() - 1
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

// MaybeFindHeader is called directly after an xref token, or after the end of
// a section inside an xref. If tries to find a header row (offset count EOL).
// If it can't find one, it then tries to find the trailer keyword. If it
// finds a trailer it will:
//   - advance to the next startxref token
//   - fix the startxref offset
//   - reset the state variables ready to find the next xref ( if any )
//   - then return false.
func (p *Parser) MaybeFindHeader() bool {
	if p.State != inside {
		p.ResetToHere()
		return false
	}

	i := p.NextItem()

	p.Scratch.WriteString(i.Val)
	var err error

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

				// don't accept in this call to CheckToken, we will write our
				// own number
				if i, ok := p.CheckToken(pdflex.ItemNumber, false); !ok {
					p.Scratch.WriteString(i.Val)
					p.ResetToHere()
					return false
				}
				p.Scratch.WriteString(fmt.Sprintf("%d", p.LastXref))

				// Next tokens should be ItemEOL then ItemComment "%%EOF", but
				// we don't actually care, let the general parsing loop emit
				// them.

				p.ResetToHere()
				return false
			}
		}

	case pdflex.ItemNumber:

		p.Offset, err = strconv.Atoi(i.Val)
		if err != nil {
			p.ResetToHere()
			return false
		}

		if _, ok := p.CheckToken(pdflex.ItemSpace, true); !ok {
			p.ResetToHere()
			return false
		}

		i, ok := p.CheckToken(pdflex.ItemNumber, true)
		if !ok {
			p.ResetToHere()
			return false
		}
		p.Entries, err = strconv.Atoi(i.Val)
		if err != nil {
			p.ResetToHere()
			return false
		}

		if _, ok := p.CheckToken(pdflex.ItemEOL, true); !ok {
			p.ResetToHere()
			return false
		}

		p.Idx = p.Offset
		if !p.SeemsLegit() {
			panic("BUG: logic broken in MaybeFindHeader")
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

// FixXrefs is a parsing loop. Essentially it seeks to an xref token, then
// loops through parsing the xref header rows and object entry rows. When no
// more xref tokens are found it runs through until the end of the file. This
// consumes the supplied lexer, so it can only be used once.
func (p *Parser) FixXrefs() []byte {
mainLoop:
	for {

		found := p.MaybeFindXref()
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

		// found a new xref section now
		for p.MaybeFindHeader() {
			if !p.SeemsLegit() {
				panic("BUG: SeemsLegit() failed after we found a header!")
			}
		entryLoop:
			for i := 0; i < p.Entries; i++ {

				row, err := p.FindRow()
				if err != nil {
					p.ResetToHere()
					continue mainLoop
				}

				if row.Active {
					objOffset := locateObj(p.Scratch.Bytes()[p.From:p.LastXref], p.Idx+i)
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
					continue entryLoop
				}
				if i.Typ == pdflex.ItemSpace && len(i.Val) == 1 {
					// not CRLF, but it was SP ...still OK if we get a linebreak now
					if j, ok := p.CheckToken(pdflex.ItemEOL, true); ok && len(j.Val) == 1 {
						// single CR or LF - all is well. Strictly speaking we
						// should only accept \r, not \n. Meh.
						continue entryLoop
					}
				}

				// line is invalid, bail.
				p.ResetToHere()
				continue mainLoop
			}
		}
		p.ResetToHere() // probably not neccessary, but idempotent
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
	// Add 1 to the offset so the index is ahead of the \n or \r
	return idx + 1
}
