package neat

import (
	"strings"
	"testing"
)

func TestShowRedactsCookie(t *testing.T) {
	d := Download{
		ID: 1, Status: "Paused ( 0% )", Filename: "a.bin", Kind: "normal",
		Method: "GET", FileSize: 10, URL: "https://files.example/a.bin",
		PostData: "token=secret",
		Headers: []Header{
			{Name: "Referer", Value: "https://example/page"},
			{Name: "Cookie", Value: "sid=abc; session=very-secret"},
		},
	}
	text := FormatShow(d, false)
	if strings.Contains(text, "very-secret") || strings.Contains(text, "token=secret") {
		t.Fatalf("redaction failed:\n%s", text)
	}
	if !strings.Contains(text, "Cookie: [") || !strings.Contains(text, "https://example/page") {
		t.Fatalf("missing public fields:\n%s", text)
	}
	if !strings.Contains(FormatShow(d, true), "sid=abc") {
		t.Fatal("reveal should print the cookie")
	}
	b, err := MarshalShow(d, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "very-secret") {
		t.Fatal(string(b))
	}
}
