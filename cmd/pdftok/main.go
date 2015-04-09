package main

import (
	"flag"
	"fmt"
	"github.com/bnagy/pdflex"
	"io/ioutil"
	"os"
	"path"
)

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
			fmt.Fprintf(os.Stderr, "Unable to open %s: %s", arg, err)
			os.Exit(1)
		}
		l := pdflex.NewLexer(arg, string(raw))
		for i := l.NextItem(); i.Typ != pdflex.ItemEOF; i = l.NextItem() {
			fmt.Printf("%#v\n", i)
			if i.Typ == pdflex.ItemError {
				fmt.Fprintf(os.Stderr, "Aborting %s at line %d, pos %d\n", arg, l.LineNumber(), l.Pos())
				break
			}
		}
	}

}
