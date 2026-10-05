package neat

import (
	"strings"
	"testing"
)

func TestBuildRequestUsesStoredHeaders(t *testing.T) {
	req, err := BuildRequest(Download{
		ID: 3, URL: "https://files.example/need-cookie", Method: "get",
		Filename: "secret.bin", Kind: "normal", FileSize: 4, Resumable: 1,
		UserAgent: "TestAgent/1",
		Headers: []Header{
			{Name: "Cookie", Value: "sid=abc"},
			{Name: "Origin", Value: "https://example"},
			{Name: "Content-Length", Value: "999"},
			{Name: "Accept-Encoding", Value: "gzip"},
		},
		PageURL: "https://example/page",
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "GET" || !req.Resumable {
		t.Fatalf("method %s resumable %v", req.Method, req.Resumable)
	}
	if req.Header.Get("Cookie") != "sid=abc" {
		t.Fatalf("cookie %q", req.Header.Get("Cookie"))
	}
	if req.Header.Get("Accept-Encoding") != "identity" {
		t.Fatalf("encoding %q", req.Header.Get("Accept-Encoding"))
	}
	if req.Header.Get("Content-Length") != "" {
		t.Fatal("stored content-length should not be copied")
	}
	if req.Header.Get("Referer") != "https://example/page" {
		t.Fatalf("referer %q", req.Header.Get("Referer"))
	}
	if req.Header.Get("User-Agent") != "TestAgent/1" {
		t.Fatalf("ua %q", req.Header.Get("User-Agent"))
	}
}

func TestFileNameFromURL(t *testing.T) {
	name := FileName(Download{ID: 9, URL: "https://cdn.example/dir/Report%20Q1.bin?sig=1"})
	if name != "Report%20Q1.bin" && name != "Report Q1.bin" {
		// path.Base does not decode percent escapes.
		if name != "Report%20Q1.bin" {
			t.Fatal(name)
		}
	}
	if got := FileName(Download{ID: 9, URL: "https://cdn.example/"}); got != "ndm-9" {
		t.Fatal(got)
	}
	got, err := OutputPath("/tmp/out", "a.bin", "", false)
	if err != nil || !strings.HasSuffix(got, "/a.bin") {
		t.Fatal(got, err)
	}
}
