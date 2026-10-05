package neat

import (
	"bytes"
	"os/exec"
	"strings"
)

// readPlistString asks plutil for one key. Failure returns fallback.
// Only the requested key is read, so unrelated preference values stay put.
func readPlistString(path, key, fallback string) string {
	out, err := exec.Command("plutil", "-extract", key, "raw", path).Output()
	if err != nil {
		return fallback
	}
	s := strings.TrimSpace(string(bytes.TrimSpace(out)))
	if s == "" {
		return fallback
	}
	return s
}
