package main

import (
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

var tfUnmodified = testFile{
	name: "test-unmodified.pdf",
	md5:  "c0a7b4f6575620dc3f970fb9a7c7bc94",
}

func openVerify(tf testFile) ([]byte, error) {
	fr, err := os.Open(tf.name)
	if err != nil {
		return nil, fmt.Errorf("Failed to open %s", tf.name)
	}
	md5 := md5.New()
	tr := io.TeeReader(fr, md5)
	contents, err := ioutil.ReadAll(tr)
	if err != nil {
		return nil, fmt.Errorf("Failed to read from %s: %s", tf.name, err)
	}
	hsh := hex.EncodeToString(md5.Sum(nil))
	if hsh != tf.md5 {
		return nil, fmt.Errorf("Validation for %s failed: want MD5 %s, got %s", tf.name, tf.md5, hsh)
	}
	return contents, nil
}

func TestRewrite(t *testing.T) {
	contents, err := openVerify(tfUnmodified)
	if err != nil {
		t.Fatalf(err.Error())
	}
	t.Logf("Unmodified: %d bytes", len(contents))
	fixed := fix(contents)
	for i, b := range fixed {
		if b != contents[i] {
			t.Fatalf("%s was modified during fix()", tfUnmodified.name)
		}
	}
}
