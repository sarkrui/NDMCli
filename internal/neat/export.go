package neat

import (
	"fmt"
	"strings"
)

// ExportJob is one portable download. Curl and Aria are already shell-quoted
// command lines. Aria is empty when aria2c cannot represent the request.
type ExportJob struct {
	ID   int64
	Curl string
	Aria string
}

// FormatCommands prints one shell command per job and nothing else.
// tool is curl or aria2c. The line is already quoted for bash and zsh,
// so it can be pasted into either shell. The commands contain cookies.
func FormatCommands(tool string, jobs []ExportJob) (string, error) {
	if len(jobs) == 0 {
		return "", fmt.Errorf("nothing to export")
	}
	curl := false
	switch tool {
	case "curl":
		curl = true
	case "aria2c":
		curl = false
	default:
		return "", fmt.Errorf("command output needs --tool curl or --tool aria2c")
	}
	lines := make([]string, 0, len(jobs))
	for _, job := range jobs {
		line := job.Aria
		kind := "aria2c"
		if curl {
			line = job.Curl
			kind = "curl"
		}
		if line == "" {
			return "", fmt.Errorf("id %d cannot be exported for %s", job.ID, kind)
		}
		if strings.ContainsAny(line, "\r\n") {
			return "", fmt.Errorf("id %d command is not a single line", job.ID)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// FormatExport writes a /bin/sh script that saves files in the current
// directory of whatever machine runs it. tool is curl, aria2c, or both.
// The script contains cookies. Use FormatCommands when the line should be
// pasted straight into bash or zsh.
func FormatExport(tool string, jobs []ExportJob) (string, error) {
	if len(jobs) == 0 {
		return "", fmt.Errorf("nothing to export")
	}
	switch tool {
	case "curl", "aria2c", "both":
	default:
		return "", fmt.Errorf("unknown tool %q", tool)
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Copy this file to the machine that should download, then run it there.\n")
	b.WriteString("# Files land in the current directory. This file contains cookies.\n")
	if tool == "both" {
		b.WriteString("#   sh export.sh curl\n")
		b.WriteString("#   sh export.sh aria2c\n")
	}
	b.WriteString("set -eu\n")
	switch tool {
	case "curl":
		if err := writeCommands(&b, jobs, true, true); err != nil {
			return "", err
		}
	case "aria2c":
		if err := writeCommands(&b, jobs, false, true); err != nil {
			return "", err
		}
	case "both":
		b.WriteString("if [ $# -lt 1 ]; then\n")
		b.WriteString("  echo \"usage: sh $0 curl|aria2c\" >&2\n")
		b.WriteString("  exit 2\n")
		b.WriteString("fi\n")
		b.WriteString("case \"$1\" in\n")
		b.WriteString("curl)\n")
		if err := writeCommands(&b, jobs, true, true); err != nil {
			return "", err
		}
		b.WriteString("  ;;\n")
		b.WriteString("aria2|aria2c)\n")
		if err := writeCommands(&b, jobs, false, false); err != nil {
			b.WriteString("  echo \"aria2c cannot download these jobs; run: sh $0 curl\" >&2\n")
			b.WriteString("  exit 1\n")
		}
		b.WriteString("  ;;\n")
		b.WriteString("*)\n")
		b.WriteString("  echo \"usage: sh $0 curl|aria2c\" >&2\n")
		b.WriteString("  exit 2\n")
		b.WriteString("  ;;\n")
		b.WriteString("esac\n")
	}
	return b.String(), nil
}

// strict reports an error when a job has no command for that tool.
// The both-tool aria2c branch is not strict: a POST job is skipped there
// and still exported for curl.
func writeCommands(b *strings.Builder, jobs []ExportJob, curl, strict bool) error {
	wrote := 0
	for _, job := range jobs {
		line := job.Aria
		kind := "aria2c"
		if curl {
			line = job.Curl
			kind = "curl"
		}
		if line == "" {
			if strict {
				return fmt.Errorf("id %d cannot be exported for %s", job.ID, kind)
			}
			fmt.Fprintf(b, "# id %d skipped: aria2c cannot send this request body\n", job.ID)
			continue
		}
		fmt.Fprintf(b, "# id %d\n%s\n", job.ID, line)
		wrote++
	}
	if wrote == 0 {
		return fmt.Errorf("no %s commands to export", map[bool]string{true: "curl", false: "aria2c"}[curl])
	}
	return nil
}
