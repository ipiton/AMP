package tools

import (
	"bytes"
	"errors"
	"testing"
)

func TestClampLines(t *testing.T) {
	cases := map[int64]int64{-5: 1, 0: 1, 1: 1, 100: 100, maxToolLines: maxToolLines, 100000: maxToolLines}
	for in, want := range cases {
		if got := clampLines(in); got != want {
			t.Errorf("clampLines(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestReadLimited(t *testing.T) {
	ok := bytes.Repeat([]byte("a"), maxToolResponseBytes)
	got, err := readLimited(bytes.NewReader(ok))
	if err != nil || len(got) != maxToolResponseBytes {
		t.Fatalf("body at the limit: len=%d err=%v", len(got), err)
	}

	big := bytes.Repeat([]byte("a"), maxToolResponseBytes+1)
	if _, err := readLimited(bytes.NewReader(big)); !errors.Is(err, errResponseTooLarge) {
		t.Errorf("body over the limit: err = %v, want errResponseTooLarge", err)
	}
}

func TestTailBuffer_KeepsNewestBytes(t *testing.T) {
	tb := &tailBuffer{max: 10}
	for _, chunk := range []string{"line-1\n", "line-2\n", "line-3\n"} {
		if _, err := tb.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(tb.buf); got != "-2\nline-3\n" {
		t.Errorf("tail = %q, want the last 10 bytes %q", got, "-2\nline-3\n")
	}
}
