package neat

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// CurlArgs is the argv for a curl invocation, without the curl binary
// repeated as a shell string. The Cookie header is sent as stored.
// --location follows redirects. curl, like Go's client, does not forward
// an explicit Cookie header to an unrelated host.
func CurlArgs(req Request, output string, resume bool) ([]string, error) {
	if err := validateExportText(req, output); err != nil {
		return nil, err
	}
	args := []string{
		"curl",
		"--fail",
		"--location",
		"--globoff",
		"--retry", "5",
		"--retry-delay", "2",
	}
	if ua := req.Header.Get("User-Agent"); ua != "" {
		args = append(args, "--user-agent", ua)
	}
	if resume {
		args = append(args, "--continue-at", "-")
	}
	if output != "" {
		args = append(args, "--output", output)
	}
	if req.Method != "" && req.Method != http.MethodGet {
		args = append(args, "--request", req.Method)
	}
	if len(req.Body) > 0 {
		args = append(args, "--data-binary", string(req.Body))
	}
	keys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		if http.CanonicalHeaderKey(k) == "User-Agent" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range req.Header.Values(k) {
			args = append(args, "--header", k+": "+v)
		}
	}
	args = append(args, req.URL)
	return args, nil
}

func validateExportText(req Request, output string) error {
	if strings.ContainsRune(output, 0) || bytesHasNUL(req.Body) || strings.ContainsRune(req.URL, 0) {
		return fmt.Errorf("download %d has a NUL byte; use ndmcli download on this machine", req.ID)
	}
	if strings.ContainsAny(output, "\r\n") || strings.ContainsAny(req.URL, "\r\n") {
		return fmt.Errorf("download %d has a newline in the url or file name", req.ID)
	}
	for k, vs := range req.Header {
		for _, v := range vs {
			if strings.ContainsRune(v, 0) || strings.ContainsAny(v, "\r\n") {
				return fmt.Errorf("header %s contains a newline; use ndmcli download on this machine", k)
			}
		}
	}
	return nil
}

func bytesHasNUL(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

// ShellQuote quotes one argv element for bash/zsh.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// FormatShell joins argv into one line that bash and zsh can run.
func FormatShell(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = ShellQuote(a)
	}
	return strings.Join(quoted, " ")
}
