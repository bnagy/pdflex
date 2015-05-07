// Package pdflex is a lexer for PDF files, based on the spec PDF32000_2008.pdf.

// Initial code inspiration text/template/parse, which is licensed as:

// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Imitation is the sincerest form of flattery.
// (c) Ben Nagy 2015

package pdflex

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Pos int

// item represents a token or text string returned from the scanner.
type Item struct {
	Typ ItemType // The type of this item.
	Pos          // The starting position, in bytes, of this item in the input string.
	Val string   // The value of this item.
}

// itemType identifies the type of lex items.
type ItemType int

const (
	ItemError ItemType = iota // error occurred; value is text of error
	ItemEOF
	ItemNumber    // PDF Number 7.3.3
	ItemSpace     // run of space characters 7.2.2 Table 1
	ItemEOL 	  // special just token for line breaks. \n, \r or \r\n
	ItemLeftDict  // Just the << token
	ItemRightDict // >> token
	ItemLeftArray
	ItemRightArray
	ItemStreamBody // raw contents of a stream
	ItemString     // PDF Literal String 7.3.4.2
	ItemHexString  // PDF Hex String 7.3.4.3
	ItemComment    // 7.2.3
	ItemName       // PDF Name Object 7.3.5
	ItemWord       // catchall for an unrecognised blob of alnums
	// Keywords appear after all the rest.
	ItemKeyword // used only to delimit the keywords
	ItemObj     // just the obj and endobj markers
	ItemEndObj
	ItemStream // just the markers
	ItemEndStream
	ItemTrailer
	ItemXref
	ItemStartXref
	ItemTrue  // not really keywords, they're actually types of
	ItemFalse // PDF Basic Object, but this is cleaner 7.3.2
	ItemNull
)

// If they need to be used directly in code then a constant string is easiest
const (
	leftDict    = "<<"
	rightDict   = ">>"
	leftStream  = "stream"
	rightStream = "endstream"
)

// keytoks maps special strings to itemTypes
var keytoks = map[string]ItemType{
	"obj":       ItemObj,
	"endobj":    ItemEndObj,
	leftStream:  ItemStream,
	rightStream: ItemEndStream,
	"trailer":   ItemTrailer,
	"xref":      ItemXref,
	"startxref": ItemStartXref,
	"true":      ItemTrue,
	"false":     ItemFalse,
	"null":      ItemNull,
}

const eof = -1

// stateFn represents the state of the scanner as a function that returns the next state.
type stateFn func(*Lexer) stateFn

// lexer holds the state of the scanner.
type Lexer struct {
	name       string    // the name of the input; used only for error reports
	input      string    // the string being scanned
	state      stateFn   // the next lexing function to enter
	pos        Pos       // current position in the input
	start      Pos       // start position of this item
	width      Pos       // width of last rune read from input
	lastPos    Pos       // position of most recent item returned by nextItem
	items      chan Item // channel of scanned items
	arrayDepth int       // nesting depth of [], <<>>
	dictDepth  int
}

func (l *Lexer) Pos() Pos     { return l.pos }
func (l *Lexer) Start() Pos   { return l.start }
func (l *Lexer) Width() Pos   { return l.width }
func (l *Lexer) LastPos() Pos { return l.lastPos }

// next returns the next rune in the input.
func (l *Lexer) next() rune {
	if int(l.pos) >= len(l.input) {
		l.width = 0
		return eof
	}
	r, w := utf8.DecodeRuneInString(l.input[l.pos:])
	l.width = Pos(w)
	l.pos += l.width
	return r
}

// peek returns but does not consume the next rune in the input.
func (l *Lexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

// backup steps back one rune. Must only be called once per call of next.
func (l *Lexer) backup() {
	l.pos -= l.width
}

// emit passes an item back to the client.
func (l *Lexer) emit(t ItemType) {
	l.items <- Item{t, l.start, l.input[l.start:l.pos]}
	l.start = l.pos
}

// ignore skips over the pending input before this point.
func (l *Lexer) ignore() {
	l.start = l.pos
}

// accept consumes the next rune if it's from the valid set.
func (l *Lexer) accept(valid string) bool {
	if strings.IndexRune(valid, l.next()) >= 0 {
		return true
	}
	l.backup()
	return false
}

// acceptRun consumes a run of runes from the valid set.
func (l *Lexer) acceptRun(valid string) {
	for strings.IndexRune(valid, l.next()) >= 0 {
	}
	l.backup()
}

// lineNumber reports which line we're on, based on the position of
// the previous item returned by nextItem. Doing it this way
// means we don't have to worry about peek double counting.
func (l *Lexer) LineNumber() int {
	return 1 + strings.Count(l.input[:l.lastPos], "\n")
}

// errorf returns an error token and terminates the scan by passing
// back a nil pointer that will be the next state, terminating l.nextItem.
func (l *Lexer) errorf(format string, args ...interface{}) stateFn {
	l.items <- Item{ItemError, l.start, fmt.Sprintf(format, args...)}
	return nil
}

// nextItem returns the next item from the input.
func (l *Lexer) NextItem() Item {
	item := <-l.items
	l.lastPos = item.Pos
	return item
}

// NewLexer creates a new scanner for the input string.
func NewLexer(name, input string) *Lexer {
	l := &Lexer{
		name:  name,
		input: input,
		items: make(chan Item),
	}
	go l.run()
	return l
}

// run runs the state machine for the lexer.
func (l *Lexer) run() {
	for l.state = lexDefault; l.state != nil; {
		l.state = l.state(l)
	}
}

// state functions

// lexDefault is the main lexing state. The rules here work for the root
// namespace, as well as inside dicts <<>> and arrays [].
func lexDefault(l *Lexer) stateFn {
	switch r := l.next(); {
	case r == '\r':
		l.accept("\n")
		l.emit(ItemEOL)
		return lexDefault
	case r == '\n':
		l.emit(ItemEOL)
		return lexDefault
	case unicode.IsSpace(r):
		return lexSpace
	case r == '/':
		return lexName
	case r == '+' || r == '-' || r == '.' || ('0' <= r && r <= '9'):
		l.backup()
		return lexNumber
		// strings and hex objects have stricter rules
	case isAlphaNumeric(r):
		return lexWord
	case r == '(':
		return lexStringObj
	// dicts and arrays can nest arbitrarily deeply. We're not a parser, but
	// let's just sanity check termination.
	case r == '<':
		if l.peek() == '<' {
			l.backup()
			l.dictDepth++
			return lexLeftDict
		}
		return lexHexObj
	// Arrays are just collections of objects, so all these default rules are still fine
	case r == '[':
		l.emit(ItemLeftArray)
		l.arrayDepth++
		return lexDefault
	case r == ']':
		l.arrayDepth--
		if l.arrayDepth < 0 {
			return l.errorf("unexexpected array terminator")
		}
		l.emit(ItemRightArray)
		return lexDefault
	case r == '%':
		return lexComment
	case r == '>':
		if l.peek() == '>' {
			l.dictDepth--
			if l.dictDepth < 0 {
				return l.errorf("unexexpected dict terminator")
			}
			l.backup()
			return lexRightDict
		}
		// '>' as part of a hex object should have been consumed in lexHex, so
		// a stray '>' in this state is not valid.
		fallthrough
	case r == eof:
		if l.arrayDepth > 0 {
			return l.errorf("unterminated array")
		}
		if l.dictDepth > 0 {
			return l.errorf("unterminated dict")
		}
		l.emit(ItemEOF)
		return nil

	default:
		return l.errorf("illegal character: %#U", r)
	}
	return lexDefault
}

// lexStream quickly skips over all the contents of PDF stream objects. The
// 'stream' header has already been consumed and emitted in lexWord.
func lexStream(l *Lexer) stateFn {

	// emit a space token for the space(s) terminating the stream marker
	if !l.scanEOL() {
		return l.errorf("expected EOL terminator for stream keyword, got: %#U", l.peek())
	}
	l.emit(ItemEOL)

	i := strings.Index(l.input[l.pos:], rightStream)
	if i < 0 {
		return l.errorf("unclosed stream")
	}

	substr := l.input[l.pos : l.pos+Pos(i)]
	// We have now consumed the stream contents AND a whitespace separator. We
	// actually want to emit the stream body token 'bare', so now we need to
	// walk backwards past those spaces.
	for {
		r, size := utf8.DecodeLastRuneInString(substr)
		if !unicode.IsSpace(r) || len(substr) <= 0 {
			break
		}
		substr = substr[:len(substr)-size]
	}

	l.pos += Pos(len(substr))
	l.emit(ItemStreamBody)

	// let lexDefault take care of lexing the space and the endstream token

	return lexDefault
}

// lexLeftDict scans the left delimiter, which is known to be present.
func lexLeftDict(l *Lexer) stateFn {
	l.pos += Pos(len(leftDict))
	l.emit(ItemLeftDict)
	return lexDefault
}

// lexComment lexes a PDF comment from a comment marker % to the next EOL
// marker. However, '\r\n' (specifically) is treated as one EOL marker. Some
// comments such as %%EOF and %PDF-1.7 are special to reader software, but
// that's parser business.
// cf PDF3200_2008.pdf 7.2.2
func lexComment(l *Lexer) stateFn {

	var r rune
	for !isEndOfLine(l.peek()) {
		r = l.next()
		if r == eof {
			l.emit(ItemComment)
			return lexDefault
		}
	}

	// any single EOL marker has been consumed above. Check for CRLF.
	if r == '\r' {
		l.accept("\n")
	}

	l.emit(ItemComment)
	return lexDefault
}

// lexRightDict scans the right delimiter, which is known to be present.
func lexRightDict(l *Lexer) stateFn {
	l.pos += Pos(len(rightDict))
	l.emit(ItemRightDict)
	return lexDefault
}

// lexName scans a PDF Name object, which is a SOLIDUS (lol) '/' followed by a
// run of non-special characters. Unprintable ASCII must be escaped with '#XX'
// codes.
// cf PDF3200_2008.pdf 7.3.5
func lexName(l *Lexer) stateFn {
	for {
		switch r := l.next(); {
		case isDelim(r) || unicode.IsSpace(r) || r == eof:
			l.backup()
			l.emit(ItemName)
			return lexDefault
		case 0x20 < r && r < 0x7f:
			break
		default:
			return l.errorf("illegal character in name: %#U", r)
		}
	}
}

// lexStringObj scans a PDF String object which is any collection of bytes
// enclosed in parens (). Strings can contain balanced parens, or unbalanced
// parens that are escaped with '\'. There are some other rules about what to
// do with parsing linebreaks and escaped special chars, but that's above our
// pay grade here.
// cf PDF3200_2008.pdf 7.3.4.2
func lexStringObj(l *Lexer) stateFn {
	balance := 1
	for {
		switch r := l.next(); {
		case r == '\\':
			// escaped parens don't count towards balance
			l.accept("()")
		case r == '(':
			balance++
		case r == ')':
			balance--
			if balance <= 0 {
				l.emit(ItemString)
				return lexDefault
			}
		case r == eof:
			return l.errorf("unterminated string object")
		default:
		}
	}
}

// lexHexObj scans a hex string, which is any number of hexadecimal characters
// or whitespace enclosed by '<' '>'. The '<' rune has already been consumed.
// cf PDF3200_2008.pdf 7.3.4.3
func lexHexObj(l *Lexer) stateFn {
	digits := "0123456789abcdefABCDEF"
	for {
		switch r := l.next(); {
		case strings.IndexRune(digits, r) >= 0 || unicode.IsSpace(r):
			//
		case r == '>':
			l.emit(ItemHexString)
			return lexDefault
		case r == eof:
			return l.errorf("unterminated hexstring")
		default:
			return l.errorf("illegal character in hexstring: %#U", r)
		}
	}
}

// lexSpace scans a run of space characters one of which has already been seen.
// cf PDF3200_2008.pdf 7.2.2
func lexSpace(l *Lexer) stateFn {
	// This is more permissive than the spec, which doesn't mention U+0085
	// (NEL), U+00A0 (NBSP)
	// We don't allow space runs that include any EOL chars.
	for isSpace(l.peek()) {
		l.next()
	}
	l.emit(ItemSpace)
	return lexDefault
}

// lexWord scans a run of basic alnums, one of which has already been seen. It
// will emit known tokens as their special types, call new state functions for
// types that require special lexing, and, failing that, emit the run as a
// catchall itemWord and then return to lexDefault
func lexWord(l *Lexer) stateFn {

	for isAlphaNumeric(l.peek()) {
		l.next()
	}

	tok, found := keytoks[l.input[l.start:l.pos]]
	if found {
		// known token type, emit it
		l.emit(tok)
		switch tok {
		case ItemStream:
			return lexStream
		default:
			return lexDefault
		}
	}

	l.emit(ItemWord)
	return lexDefault
}

// lexNumber scans a decimal or real number
// cf PDF3200_2008.pdf 7.3.3
func lexNumber(l *Lexer) stateFn {
	if !l.scanNumber() {
		return l.errorf("bad number syntax: %q", l.input[l.start:l.pos])
	}
	l.emit(ItemNumber)
	return lexDefault
}

func (l *Lexer) scanNumber() bool {
	// Optional leading sign.
	l.accept("+-")
	digits := "0123456789"
	l.acceptRun(digits)
	if l.accept(".") {
		l.acceptRun(digits)
	}
	// Next thing must be a delimeter, space char or eof
	if isDelim(l.peek()) || unicode.IsSpace(l.peek()) || l.peek() == eof {
		return true
	}
	l.next()
	return false
}

func (l *Lexer) scanEOL() bool {
	if !isEndOfLine(l.peek()) {
		return false
	}
	r := l.next()
	if r == '\r' {
		l.accept("\n")
	}
	return true
}

// isEndOfLine reports whether r is an end-of-line character.
func isEndOfLine(r rune) bool {
	return r == '\r' || r == '\n'
}

func isSpace(r rune) bool {
	return unicode.IsSpace(r) && !isEndOfLine(r)
}

// isDelim reports whether r is one of the 10 reserved PDF delimiter characters
// cf PDF3200_2008.pdf 7.2.2
func isDelim(r rune) bool {
	return strings.IndexRune("[]{}()<>/%", r) >= 0
}

// isAlphaNumeric reports whether r is an alphabetic, digit, or underscore.
func isAlphaNumeric(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
