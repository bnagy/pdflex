package main

import (
	"bufio"
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

func fixStartXref(in []byte) []byte {

	sxrIdx := bytes.LastIndex(in, startxref)
	if sxrIdx < 0 {
		return in
	}

	xrIdx := bytes.LastIndex(in[:sxrIdx], xref)
	if xrIdx < 0 {
		return in
	}

	scratch := []byte{}
	scratch = append(scratch, in[:sxrIdx]...)
	scratch = append(scratch, []byte(fmt.Sprintf("startxref\n%d\n%%%%EOF", xrIdx))...)
	return scratch
}

func fixXrefs(in []byte) []byte {

	out := new(bytes.Buffer)
	tr := bytes.LastIndex(in, trailer)
	xr := bytes.LastIndex(in[:tr], xref)
	if xr < 0 || tr < 0 || tr < xr {
		return in
	}
	xrSection := in[xr:tr]

	// Try to normalize the xrefs so lines are delimited by one \n. This works
	// for \n, \r, \r\n...
	normalized := strings.Replace(string(xrSection), "\r", "\n", -1)
	normalized = strings.Replace(normalized, "\n\n", "\n", -1)

	// Validate / parse the header rows
	scanner := bufio.NewScanner(strings.NewReader(normalized))
	scanner.Scan()
	ff := strings.Fields(scanner.Text())
	if ff[0] != "xref" {
		if *flagStrict {
			log.Fatalf("[STRICT] Corrupt xref section\n%#v\n", string(xrSection))
		}
		return in
	}
	fmt.Fprintln(out, scanner.Text())
	scanner.Scan()
	ff = strings.Fields(scanner.Text())
	if len(ff) < 2 {
		if *flagStrict {
			log.Fatalf("[STRICT] Corrupt xref section\n%#v\n", string(xrSection))
		}
		return in
	}
	expected, err := strconv.Atoi(ff[1])
	if err != nil {
		if *flagStrict {
			log.Fatalf("[STRICT] Corrupt xref section\n%#v\n", string(xrSection))
		}
		return in
	}

	// Parse the xrefs entries and try to fix up indirect ref offsets
	for i := 0; i < expected; i++ {
		if !scanner.Scan() {
			if *flagStrict {
				log.Fatalf("[STRICT] Short xref section\n")
			}
			return in
		}
		ff := strings.Fields(scanner.Text())
		// Get indirect refs like 0000037118 00000 n
		// Don't know what 0000000389 00001 f refs are
		if ff[2] == "n" {
			idx := -1
			// Do it this way instead of using a regex, because the multi-line
			// regexes / anchors seem wonky for PDFs that use \r as a "bare"
			// line delimeter
			idx = bytes.Index(in, []byte(fmt.Sprintf("\n%d 0 obj", i)))
			if idx < 0 {
				idx = bytes.Index(in, []byte(fmt.Sprintf("\r%d 0 obj", i)))
			}
			if idx >= 0 {
				fmt.Fprintf(out, "%.10d %s %s\n", idx, ff[1], ff[2])
				continue
			}
		}
		// Not an indirect ref OR couldn't find that obj declaration
		// Emit this line unmodified
		fmt.Fprintln(out, scanner.Text())
	}

	// Replace the xrefs with our modified version. The length might be
	// slightly different if we squeezed one or more \r\n into \n.
	final := append(in[:xr], out.Bytes()...)
	final = append(final, in[tr:]...)
	return fixStartXref(final)
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

	for _, arg := range os.Args[1:] {

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
					// If not struct, we ignore any errors here. If it's
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

		fixed := fixXrefs(out.Bytes())
		newfn := strings.TrimSuffix(path.Base(arg), path.Ext(arg)) + "-small" + path.Ext(arg)
		newfn = path.Join(path.Dir(arg), newfn)
		err = ioutil.WriteFile(newfn, fixed, 0600)
		if err != nil {
			log.Fatalf("Unable to write %s: %s", newfn, err)
		}
	}

}
