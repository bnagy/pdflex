package main

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

// These hashes are especially important because we're sensitive to \r\n vs \n
// style line endings, which lots of editors like to screw with.

var tfUnmodified = testFile{
	name: "test-unmodified.pdf",
	md5:  "c0a7b4f6575620dc3f970fb9a7c7bc94",
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
