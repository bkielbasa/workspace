package main

import (
	"bufio"
	"bytes"
	"testing"
)

func TestFetchLiteralSequencing(t *testing.T) {
	body := "Header: value\r\n\r\n"
	var out bytes.Buffer
	if err := writeFetchLiteral(bufio.NewWriter(&out), "* 1 FETCH (BODY[HEADER] {"+itoa(len(body))+"}", body); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	want := "* 1 FETCH (BODY[HEADER] {17}\r\nHeader: value\r\n\r\n)\r\n"
	if got != want {
		t.Fatalf("invalid IMAP literal framing:\n got %q\nwant %q", got, want)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
