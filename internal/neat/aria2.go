package neat

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
)

// Aria2Options controls the aria2c command written for another machine.
// Output is a file name, or a path the other machine understands.
// Connections is capped at 16, which is aria2's usual per-server maximum.
// Zero means 8 when the server accepts ranges, and 1 otherwise.
type Aria2Options struct {
	Output      string
	Connections int
}

// Aria2Args builds one aria2c invocation. The cookie is sent with
// --header so the command is self-contained: the other machine does not
// need Neat Download Manager's database or a cookie file beside the command.
func Aria2Args(req Request, opt Aria2Options) ([]string, error) {
	if err := validateExportText(req, opt.Output); err != nil {
		return nil, err
	}
	if opt.Output == "" {
		return nil, fmt.Errorf("download %d needs a file name", req.ID)
	}
	if req.Method != http.MethodGet && req.Method != "" {
		return nil, fmt.Errorf("download %d is %s; aria2c has no request body, export it with --tool curl", req.ID, req.Method)
	}
	n := ariaConnections(req, opt.Connections)
	args := []string{
		"aria2c",
		"--continue=true",
		"--max-tries=5",
		"--retry-wait=2",
		"--auto-file-renaming=false",
		"--allow-overwrite=false",
		"--file-allocation=none",
		"--summary-interval=1",
		"--max-connection-per-server=" + fmt.Sprint(n),
		"--split=" + fmt.Sprint(n),
		"--min-split-size=1M",
	}
	if ua := req.Header.Get("User-Agent"); ua != "" {
		args = append(args, "--user-agent="+ua)
	}
	if ref := req.Header.Get("Referer"); ref != "" {
		args = append(args, "--referer="+ref)
	}
	dir, name := splitExportPath(opt.Output)
	if dir != "" {
		args = append(args, "--dir="+dir)
	}
	args = append(args, "--out="+name)
	keys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		switch http.CanonicalHeaderKey(k) {
		case "User-Agent", "Referer":
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range req.Header.Values(k) {
			args = append(args, "--header="+k+": "+v)
		}
	}
	args = append(args, req.URL)
	return args, nil
}

func ariaConnections(req Request, n int) int {
	if !req.Resumable {
		return 1
	}
	if n <= 0 {
		return 8
	}
	if n > 16 {
		return 16
	}
	return n
}

func splitExportPath(output string) (dir, name string) {
	dir = filepath.Dir(output)
	name = filepath.Base(output)
	if dir == "." || dir == "" {
		dir = ""
	}
	return dir, name
}
