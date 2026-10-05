package neat

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func serveBytes(t *testing.T, body []byte, wantCookie string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != wantCookie {
			t.Errorf("cookie %q", r.Header.Get("Cookie"))
		}
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Errorf("encoding %q", r.Header.Get("Accept-Encoding"))
		}
		rh := r.Header.Get("Range")
		if rh == "" {
			w.Write(body)
			return
		}
		start, end, err := parseRange(rh, len(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
		w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(body[start : end+1])
	}))
}

func parseRange(h string, n int) (int, int, error) {
	rest, ok := strings.CutPrefix(h, "bytes=")
	if !ok {
		return 0, 0, fmt.Errorf("range %q", h)
	}
	a, b, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, 0, fmt.Errorf("range %q", h)
	}
	start, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, err
	}
	end := n - 1
	if b != "" {
		end, err = strconv.Atoi(b)
		if err != nil {
			return 0, 0, err
		}
	}
	if start < 0 || end >= n || start > end {
		return 0, 0, fmt.Errorf("bad range %d-%d", start, end)
	}
	return start, end, nil
}

func testDL(t *testing.T) *Downloader {
	t.Helper()
	d := NewDownloader()
	d.Stderr = io.Discard
	return d
}

func TestDownloadSingleAndResume(t *testing.T) {
	body := []byte("0123456789")
	srv := serveBytes(t, body, "sid=abc")
	defer srv.Close()
	dir := t.TempDir()
	out := filepath.Join(dir, "a.bin")
	job := Job{
		ID: 1, Method: "GET", URL: srv.URL, Output: out,
		FileSize: int64(len(body)), Resumable: true, Connections: 1,
		Header: map[string][]string{"Cookie": {"sid=abc"}, "Accept-Encoding": {"identity"}, "User-Agent": {"TestAgent/1"}},
	}
	// Stop after 4 bytes by using a server that... we instead pre-seed and resume.
	if err := os.WriteFile(out, body[:4], 0o644); err != nil {
		t.Fatal(err)
	}
	job.Resume = true
	job.ForceRange = true
	res, err := testDL(t).Run(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("got %q", got)
	}
}

func TestDownloadParallel(t *testing.T) {
	body := bytesRepeat(64)
	srv := serveBytes(t, body, "sid=abc")
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "p.bin")
	job := Job{
		ID: 2, Method: "GET", URL: srv.URL, Output: out,
		FileSize: int64(len(body)), Resumable: true, Connections: 4, MinChunk: 8,
		Header: map[string][]string{"Cookie": {"sid=abc"}, "Accept-Encoding": {"identity"}},
	}
	if _, err := testDL(t).Run(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("parallel mismatch %q", got)
	}
	if _, err := os.Stat(SidecarPath(out)); !os.IsNotExist(err) {
		t.Fatal("sidecar should be removed after success")
	}
}

func TestAdoptSegments(t *testing.T) {
	body := []byte("abcdefghijklmnopqrstuvwxyz012345")
	srv := serveBytes(t, body, "sid=abc")
	defer srv.Close()
	item := t.TempDir()
	segs := append(putSegment(0, 1, 0, 15), putSegment(1, 2, 16, 31)...)
	if err := os.WriteFile(filepath.Join(item, "segments.bin"), segs, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(item, "seg.x0"), []byte("abcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(item, "seg.x1"), []byte("qr"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "adopted.bin")
	job := Job{
		ID: 7, Method: "GET", URL: srv.URL, Output: out,
		FileSize: int64(len(body)), Resumable: true, ForceRange: true,
		Connections: 2, MinChunk: 1, AdoptDir: item,
		Header: map[string][]string{"Cookie": {"sid=abc"}, "Accept-Encoding": {"identity"}},
	}
	if _, err := testDL(t).Run(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("adopted %q", got)
	}
}

func TestRedirectDropsCookieOnOtherHost(t *testing.T) {
	var otherCookie, otherReferer string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherCookie = r.Header.Get("Cookie")
		otherReferer = r.Header.Get("Referer")
		w.Write([]byte("ok"))
	}))
	defer other.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "" {
			t.Error("first hop missing cookie")
		}
		// Different hostname, not just a different port. Go treats two ports
		// on 127.0.0.1 as the same host and will keep the cookie.
		target := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
		http.Redirect(w, r, target, http.StatusFound)
	}))
	defer first.Close()
	out := filepath.Join(t.TempDir(), "r.bin")
	h := map[string][]string{
		"Cookie":          {"sid=abc"},
		"Referer":         {"https://example/page"},
		"Accept-Encoding": {"identity"},
	}
	job := Job{ID: 1, Method: "GET", URL: first.URL, Output: out, Header: h}
	if _, err := testDL(t).Run(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if otherCookie != "" {
		t.Fatalf("cookie leaked: %q", otherCookie)
	}
	if otherReferer != "https://example/page" {
		t.Fatalf("referer %q", otherReferer)
	}
}

func TestSameHostRedirectKeepsCookie(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "http://"+r.Host+"/file", http.StatusFound)
			return
		}
		got = r.Header.Get("Cookie")
		w.Write([]byte("z"))
	}))
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "z.bin")
	job := Job{
		ID: 1, Method: "GET", URL: srv.URL, Output: out,
		Header: map[string][]string{"Cookie": {"sid=abc"}, "Accept-Encoding": {"identity"}},
	}
	if _, err := testDL(t).Run(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if got != "sid=abc" {
		t.Fatalf("same-host cookie %q", got)
	}
}

func TestPostBody(t *testing.T) {
	var method, body, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		ctype = r.Header.Get("Content-Type")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "post.bin")
	job := Job{
		ID: 4, Method: "POST", URL: srv.URL, Output: out, Body: []byte("a=1&b=2"),
		Header: map[string][]string{
			"Content-Type":    {"application/x-www-form-urlencoded"},
			"Accept-Encoding": {"identity"},
		},
	}
	if _, err := testDL(t).Run(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if method != "POST" || body != "a=1&b=2" || ctype != "application/x-www-form-urlencoded" {
		t.Fatalf("method %s body %q type %q", method, body, ctype)
	}
}

func bytesRepeat(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}
