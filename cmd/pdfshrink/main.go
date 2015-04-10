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
	scratch = append(scratch, []byte(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrIdx))...)
	return scratch
}

func fixXrefs(in []byte) []byte {
	out := new(bytes.Buffer)
	tr := bytes.LastIndex(in, trailer)
	xr := bytes.LastIndex(in[:tr], xref)
	xrSection := in[xr:tr]
	normalized := strings.Replace(string(xrSection), "\r", "\n", -1)
	normalized = strings.Replace(normalized, "\n\n", "\n", -1)
	scanner := bufio.NewScanner(strings.NewReader(normalized))
	scanner.Scan()
	ff := strings.Fields(scanner.Text())
	if ff[0] != "xref" {
		log.Fatalf("Corrupt xref section\n%#v\n", string(xrSection))
	}
	fmt.Fprintln(out, scanner.Text())
	scanner.Scan()
	ff = strings.Fields(scanner.Text())
	if len(ff) < 2 {
		log.Fatalf("Corrupt xref section\n%#v\n", string(xrSection))
	}
	expected, err := strconv.Atoi(ff[1])
	if err != nil {
		log.Fatalf("Corrupt xref section\n%#v\n", string(xrSection))
	}

	for i := 0; i < expected; i++ {
		if !scanner.Scan() {
			log.Fatalf("Short xref section\n")
		}
		ff := strings.Fields(scanner.Text())
		if ff[2] == "n" {
			idx := -1
			idx = bytes.Index(in, []byte(fmt.Sprintf("\n%d 0 obj", i)))
			if idx < 0 {
				idx = bytes.Index(in, []byte(fmt.Sprintf("\r%d 0 obj", i)))
			}
			if idx >= 0 {
				fmt.Fprintf(out, "%.10d %s %s\n", idx, ff[1], ff[2])
				continue
			}
		}
		// in all other cases
		fmt.Fprintln(out, scanner.Text())
	}
	head := in[:xr]
	tail := in[tr:]
	final := append(head, out.Bytes()...)
	final = append(final, tail...)
	return fixStartXref(final)
}

func inflate(s string) (string, error) {
	in := strings.NewReader(s)
	decom, err := zlib.NewReader(in)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	io.Copy(&b, decom)
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
	in = bytes.TrimPrefix(in, pref85)
	in = bytes.TrimSuffix(in, suff85)
	out := make([]byte, len(in))

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
						log.Fatalf("Failed to un85: %s", err)
					}
				}

				if zipped {

					s2, err := inflate(s)

					if err != nil {
						log.Fatalf("%d Failed to inflate internal stream: %s", i.Pos, err)
					}
					if len(s2) > 0 {
						s = s2
					}
				}

				if len(s) > MAXDATA {
					s = s[:MAXDATA]
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
						log.Fatalf("Error zipping truncated string: %s", err)
					}
				}
				if asc85 {
					s, err = re85(s)
					if err != nil {
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
		fmt.Println(string(fixed))
	}

}
