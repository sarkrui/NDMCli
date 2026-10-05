package neat

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sampleReq(url, cookie string) Request {
	h := make(http.Header)
	h.Set("User-Agent", "TestAgent/1")
	h.Set("Cookie", cookie)
	h.Set("Referer", "https://example/page")
	h.Set("Accept-Encoding", "identity")
	h.Set("Origin", "https://example")
	return Request{
		ID: 9, Method: "GET", URL: url, Header: h,
		Resumable: true, Filename: "report.bin",
	}
}

func TestAria2AndCurlStayPortable(t *testing.T) {
	req := sampleReq("https://files.example/report.bin?sig=1", "sid=abc")
	curlArgs, err := CurlArgs(req, "report.bin", true)
	if err != nil {
		t.Fatal(err)
	}
	ariaArgs, err := Aria2Args(req, Aria2Options{Output: "report.bin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{curlArgs, ariaArgs} {
		joined := strings.Join(args, "\n")
		if strings.Contains(joined, "/Users/") || strings.Contains(joined, "Application Support") {
			t.Fatalf("command embeds this machine's path:\n%s", joined)
		}
	}
	if !containsArg(ariaArgs, "--out=report.bin") {
		t.Fatalf("aria out: %v", ariaArgs)
	}
	if containsPrefix(ariaArgs, "--dir=") {
		t.Fatalf("bare file name should not set --dir: %v", ariaArgs)
	}
	if !containsArg(ariaArgs, "--max-connection-per-server=8") || !containsArg(ariaArgs, "--split=8") {
		t.Fatalf("connections: %v", ariaArgs)
	}
	if !containsArg(ariaArgs, "--header=Cookie: sid=abc") {
		t.Fatalf("cookie header missing: %v", ariaArgs)
	}
	if !containsArg(ariaArgs, "--user-agent=TestAgent/1") || !containsArg(ariaArgs, "--referer=https://example/page") {
		t.Fatalf("ua/referer: %v", ariaArgs)
	}

	one, err := Aria2Args(Request{
		ID: 1, Method: "GET", URL: "https://files.example/a",
		Header: make(http.Header), Filename: "a",
	}, Aria2Options{Output: "a", Connections: 8})
	if err != nil {
		t.Fatal(err)
	}
	if !containsArg(one, "--split=1") {
		t.Fatalf("non-resumable should stay one connection: %v", one)
	}
	_, err = Aria2Args(Request{
		ID: 4, Method: "POST", URL: "https://files.example/a",
		Header: make(http.Header),
	}, Aria2Options{Output: "a"})
	if err == nil {
		t.Fatal("POST should not be exported for aria2c")
	}
}

func TestFormatCommandsIsAShellLine(t *testing.T) {
	cookie := "sid=$(echo pwned); name=O'Brien"
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Cookie")
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	req := sampleReq(srv.URL+"/file", cookie)
	curlArgs, err := CurlArgs(req, "out.bin", true)
	if err != nil {
		t.Fatal(err)
	}
	text, err := FormatCommands("curl", []ExportJob{{
		ID: 9, Curl: FormatShell(curlArgs),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "#!/bin/sh") || strings.Contains(text, "set -eu") || strings.Contains(text, "case ") {
		t.Fatalf("command is wrapped in a script:\n%s", text)
	}
	line := strings.TrimRight(text, "\n")
	if strings.Contains(line, "\n") {
		t.Fatalf("expected one line, got %q", text)
	}
	if !strings.HasPrefix(line, "'curl' ") {
		t.Fatalf("line %s", line)
	}
	dir := t.TempDir()
	ran := 0
	for _, shell := range []string{"bash", "zsh"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		ran++
		got = ""
		outPath := filepath.Join(dir, "out.bin")
		os.Remove(outPath)
		cmd := exec.Command(shell, "-c", line)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", shell, err, out)
		}
		if got != cookie {
			t.Fatalf("%s cookie %q", shell, got)
		}
		body, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "hello" {
			t.Fatalf("%s body %q", shell, body)
		}
	}
	if ran == 0 {
		t.Fatal("neither bash nor zsh is installed")
	}
}

func TestExportScriptRunsOnCurl(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	cookie := "sid=$(echo pwned); name=O'Brien"
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Cookie")
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	req := sampleReq(srv.URL+"/file", cookie)
	curlArgs, err := CurlArgs(req, "out.bin", true)
	if err != nil {
		t.Fatal(err)
	}
	ariaArgs, err := Aria2Args(req, Aria2Options{Output: "out.bin", Connections: 1})
	if err != nil {
		t.Fatal(err)
	}
	script, err := FormatExport("both", []ExportJob{{
		ID: 9, Curl: FormatShell(curlArgs), Aria: FormatShell(ariaArgs),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "/Users/") {
		t.Fatal("script contains a home path")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.sh")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("sh", "-n", path).Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("sh", path).Run(); err == nil {
		t.Fatal("script without curl|aria2c should fail")
	}
	cmd := exec.Command("sh", path, "curl")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("curl export: %v\n%s", err, out)
	}
	if got != cookie {
		t.Fatalf("cookie %q", got)
	}
	body, err := os.ReadFile(filepath.Join(dir, "out.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Fatalf("body %q", body)
	}
}

func TestExportScriptRunsOnAria2(t *testing.T) {
	if _, err := exec.LookPath("aria2c"); err != nil {
		t.Skip("aria2c not installed")
	}
	cookie := "sid=abc"
	var gotCookie, gotUA, gotRef string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotUA = r.Header.Get("User-Agent")
		gotRef = r.Header.Get("Referer")
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	req := sampleReq(srv.URL+"/file", cookie)
	args, err := Aria2Args(req, Aria2Options{Output: "out.bin", Connections: 8})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("aria2c: %v\n%s", err, out)
	}
	if gotCookie != cookie || gotUA != "TestAgent/1" || gotRef != "https://example/page" {
		t.Fatalf("cookie %q ua %q referer %q", gotCookie, gotUA, gotRef)
	}
	body, err := os.ReadFile(filepath.Join(dir, "out.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Fatalf("body %q", body)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsPrefix(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}
