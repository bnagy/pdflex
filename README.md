pdflex
=======

## Documentation

Unless you are a hacker or a weirdo this is not what you are looking for.

```bash
$ ./pdftok *.pdf
pdflex.Item{Typ:11, Pos:0, Val:"%PDF-1.1"}
pdflex.Item{Typ:3, Pos:8, Val:"\n"}
pdflex.Item{Typ:11, Pos:9, Val:"%¥±ë"}
pdflex.Item{Typ:3, Pos:16, Val:"\n\n"}
pdflex.Item{Typ:2, Pos:18, Val:"1"}
pdflex.Item{Typ:3, Pos:19, Val:" "}
pdflex.Item{Typ:2, Pos:20, Val:"0"}
pdflex.Item{Typ:3, Pos:21, Val:" "}
pdflex.Item{Typ:15, Pos:22, Val:"obj"}
pdflex.Item{Typ:3, Pos:25, Val:"\n  "}
pdflex.Item{Typ:4, Pos:28, Val:"<<"}
pdflex.Item{Typ:3, Pos:30, Val:" "}
pdflex.Item{Typ:12, Pos:31, Val:"/Type"}
pdflex.Item{Typ:3, Pos:36, Val:" "}
pdflex.Item{Typ:12, Pos:37, Val:"/Catalog"}
pdflex.Item{Typ:3, Pos:45, Val:"\n     "}
pdflex.Item{Typ:12, Pos:51, Val:"/Pages"}
pdflex.Item{Typ:3, Pos:57, Val:" "}
pdflex.Item{Typ:2, Pos:58, Val:"2"}
pdflex.Item{Typ:3, Pos:59, Val:" "}
pdflex.Item{Typ:2, Pos:60, Val:"0"}
pdflex.Item{Typ:3, Pos:61, Val:" "}
pdflex.Item{Typ:13, Pos:62, Val:"R"}
[...]
```

Obviously you can `grep` `sed` `cut` or whatever. If you're a Go user, the lexing API is dirt simple ( check [pdftok/main.go](pdftok) ) if you want to do something cooler. If you do, shoot me a PR.

Token types (EOF -> 1, ItemNumber -> 2 etc):
```go
const (
  ItemError itemType = iota // error occurred; value is text of error
  ItemEOF
  ItemNumber    // PDF Number 7.3.3
  ItemSpace     // run of space characters 7.2.2 Table 1
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
```

## Installation

You should follow the [instructions](https://golang.org/doc/install) to
install Go, if you haven't already done so.

Now, install eg pdftok:
```bash
$ go get -u github.com/bnagy/pdflex/cmd/pdftok
```

## Tools

`pdftok` just emits the raw lexed stream of tokens, write your own parser on top if you like

`pdfshrink` brutally truncates the contents of pdf `stream` objects. The idea is that this will shrink PDF files so that they can be used for fuzzing. The files will be invalid/corrupt in assorted ways, but hopefully not corrupt enough that parsers won't be able to open them.

## TODO

I lexed a bunch of the Adobe Engineering test files (eg from [here](http://acroeng.adobe.com/wp/?page_id=10)) and put the Literal Name tokens in [toks_raw.txt](toks_raw.txt). These have been further curated (by hand) in [toks_curated.txt](toks_curated.txt) - I am using these to augment my AFL PDF dictionary. You will need to write your own script to emit each line as a file, which AFL requires.

## Contributing

Fork and send a pull request.

Report issues.

## License & Acknowledgements

BSD style, see LICENSE file for details.

Code heavily based on [this](http://cuddle.googlecode.com/hg/talk/lex.html) awesome talk by Rob Pike, and its implementation in the Go standard library in the `text/template` package.

