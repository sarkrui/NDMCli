package neat

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellQuoteRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	cookie := "sid=$(echo pwned); name=O'Brien"
	var gotCookie, gotUA, gotEnc, gotExtra string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotUA = r.Header.Get("User-Agent")
		gotEnc = r.Header.Get("Accept-Encoding")
		gotExtra = r.Header.Get("X-Client-Data")
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	req := Request{
		ID: 1, Method: "GET", URL: srv.URL + "/file",
		Header: make(http.Header),
	}
	req.Header.Set("User-Agent", "TestAgent/1")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("X-Client-Data", "abc")
	out := filepath.Join(t.TempDir(), "out.bin")
	argv, err := CurlArgs(req, out, false)
	if err != nil {
		t.Fatal(err)
	}
	line := FormatShell(argv)
	cmd := exec.Command("bash", "-c", line)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if gotCookie != cookie {
		t.Fatalf("cookie = %q", gotCookie)
	}
	if gotUA != "TestAgent/1" || gotEnc != "identity" || gotExtra != "abc" {
		t.Fatalf("ua %q enc %q extra %q", gotUA, gotEnc, gotExtra)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Fatalf("body %q", b)
	}
	if strings.Contains(line, cookie) && !strings.Contains(line, `'\''`) {
		t.Fatal("quote was not escaped")
	}
}

func TestCurlDoesNotForwardCookieToOtherHost(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	var otherCookie string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherCookie = r.Header.Get("Cookie")
		fmt.Fprint(w, "other")
	}))
	defer other.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/x", http.StatusFound)
	}))
	defer first.Close()

	req := Request{ID: 1, Method: "GET", URL: first.URL, Header: make(http.Header)}
	req.Header.Set("Cookie", "sid=abc")
	req.Header.Set("User-Agent", "TestAgent/1")
	argv, err := CurlArgs(req, filepath.Join(t.TempDir(), "out.bin"), false)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if otherCookie != "" {
		t.Fatalf("curl forwarded cookie to %s: %q", other.URL, otherCookie)
	}
}

func TestCurlKeepsCookieOnSameHostRedirect(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "http://"+r.Host+"/file", http.StatusFound)
			return
		}
		got = r.Header.Get("Cookie")
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()
	req := Request{ID: 1, Method: "GET", URL: srv.URL + "/", Header: make(http.Header)}
	req.Header.Set("Cookie", "sid=abc")
	req.Header.Set("User-Agent", "TestAgent/1")
	argv, err := CurlArgs(req, filepath.Join(t.TempDir(), "out.bin"), false)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		t.Fatalf("curl: %v", err)
	}
	if got != "sid=abc" {
		t.Fatalf("same-host cookie %q", got)
	}
}
