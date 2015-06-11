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
	"strings"
)

var xref = []byte("xref")
var startxref = []byte("startxref")
var trailer = []byte("trailer")
var pref85 = "<~"
var suff85 = "~>"

var (
	flagStrict = flag.Bool("strict", false, "Abort on xref parsing errors etc")
	flagMax    = flag.Int("max", 128, "Trim streams whose size is greater than this value")
)

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
	// Caller is expected to trim <~ ~> if present
	s = strings.TrimPrefix(s, pref85)
	s = strings.TrimSuffix(s, suff85)
	dec := ascii85.NewDecoder(strings.NewReader(s))
	out, err := ioutil.ReadAll(dec)

	if err != nil {
		return "", err
	}

	return string(out), nil
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

func shrink(in []byte, max int) ([]byte, error) {

	l := pdflex.NewLexer("", string(in))
	var out bytes.Buffer
	zipped := false
	asc85 := false
	var err error

	for i := l.NextItem(); i.Typ != pdflex.ItemEOF; i = l.NextItem() {
		if i.Typ == pdflex.ItemStreamBody {

			s := i.Val

			if asc85 {
				s, err = un85(s)
				if err != nil && *flagStrict {
					log.Fatalf("[STRICT] Failed to un85: %s", err)
				}
			}

			if zipped {
				s2, err := inflate(s)
				if err != nil && *flagStrict {
					log.Fatalf("[STRICT] Error unzipping internal stream: %s\n", err)
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

			if len(s) > max {
				s = s[:max]
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
					return nil, fmt.Errorf("error zipping truncated string: %s", err)
				}
			}
			if asc85 {
				s, err = re85(s)
				if err != nil {
					// ditto
					return nil, fmt.Errorf("error Ascii85ing string: %s", err)
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
	return out.Bytes(), nil
}

func fix(in []byte) []byte {
	p := Parser{Lexer: pdflex.NewLexer("", string(in))}
	return p.FixXrefs()
}

func main() {

	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			"  Usage: %s file [file file ...]\n"+
				"    -max=128: Trim streams whose size is greater than this value\n"+
				"    -strict=false: Abort on xref parsing errors etc\n",
			path.Base(os.Args[0]),
		)
	}

	flag.Parse()
	if *flagMax < 0 {
		flag.Usage()
		os.Exit(1)
	}

	for _, arg := range flag.Args() {

		fmt.Fprintf(os.Stderr, "[SHRINKING] %s\n", arg)

		// Read in
		raw, err := ioutil.ReadFile(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[SKIPPED] %s - %s\n", arg, err)
			continue
		}

		// Shrink
		shrunk, err := shrink(raw, *flagMax)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[SKIPPED] %s - strict mode: %s\n", arg, err)
			continue
		}

		// Fix up xrefs
		fixed := fix(shrunk)

		// Write out
		newfn := strings.TrimSuffix(path.Base(arg), path.Ext(arg)) + "-small" + path.Ext(arg)
		newfn = path.Join(path.Dir(arg), newfn)
		err = ioutil.WriteFile(newfn, fixed, 0600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[SKIPPED] %s - %s", newfn, err)
		}
	}

}
