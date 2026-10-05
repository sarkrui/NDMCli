package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/sarkrui/NDMCli/internal/neat"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "list":
		os.Exit(cmdList(os.Args[2:]))
	case "show":
		os.Exit(cmdShow(os.Args[2:]))
	case "curl":
		os.Exit(cmdCurl(os.Args[2:]))
	case "export":
		os.Exit(cmdExport(os.Args[2:]))
	case "download":
		os.Exit(cmdDownload(os.Args[2:]))
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "ndmcli: unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `ndmcli reads Neat Download Manager's database and turns a saved
download into a command that curl or aria2c can run, or downloads the file itself.

macOS stores that database at
~/Library/Application Support/com.NeatDownloadManager/NeatDB.db.
The URL, method, file name, and user agent are in the downloads table.
Cookies are the Cookie header in the headers table (one header per row).
Partial multi-connection jobs also keep <id>/segments.bin and <id>/seg.xN.

Usage:
  ndmcli list [--status open|all|paused|error|complete] [--cookie] [--name TEXT] [--json]
  ndmcli show <id> [--reveal] [--json]
  ndmcli curl <id> [--resume] [-o FILE]
  ndmcli export [<id>...] [--tool curl|aria2c|both] [--save FILE] [-n N] [-o NAME]
  ndmcli download <id> [-o FILE] [-n N] [--resume] [--adopt] [--force]

--db PATH replaces the database. NDM_DB is used when --db is omitted.

list prints unfinished jobs. It never prints cookie values. show redacts
Cookie, Authorization, and the POST body unless --reveal. curl and export
print those values, so the output is a secret.

export puts the URL, cookies, and headers in the output. File names are
relative, so the download lands in whatever directory you run it from.
With no ids, export uses the same filter as list (unfinished jobs).
--tool curl and --tool aria2c print one command per job for bash or zsh.
--save writes a script (mode 0600) instead. --tool both is that script and
asks for curl or aria2c when you run it. aria2c cannot send a POST body;
those jobs need --tool curl.

download writes a file. --resume continues from the bytes already in that
file, same as curl -C -. --adopt copies Neat's seg.xN pieces into the output
and downloads the gaps. Pause the job in Neat Download Manager before
--adopt. -n splits a range-capable file across up to 8 connections.

hls and media rows point at a playlist, not the media file. curl saves that
URL as-is. download refuses them unless --force.

Examples:
  ndmcli list --status paused --cookie
  ndmcli show <id>
  ndmcli curl <id> | sh
  ndmcli export <id> --tool curl
  ndmcli export <id> --tool aria2c
  ndmcli export <id> --save jobs.sh && sh jobs.sh aria2c
  ndmcli download <id> -o ~/Downloads/file.bin
  ndmcli download <id> --adopt -n 8
`)
}

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "path to NeatDB.db")
	status := fs.String("status", "open", "open, all, paused, error, or complete")
	cookie := fs.Bool("cookie", false, "only jobs that stored a Cookie header")
	name := fs.String("name", "", "file name substring")
	asJSON := fs.Bool("json", false, "print JSON")
	flags, _ := reorderArgs(args, valueFlags())
	if err := fs.Parse(flags); err != nil {
		fmt.Fprintf(os.Stderr, "ndmcli: %v\n", err)
		return 2
	}
	st, err := openDB(*dbPath)
	if err != nil {
		return fail(err)
	}
	defer st.Close()
	rows, err := st.List(neat.Filter{Status: *status, Cookie: *cookie, Name: *name})
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(neat.ListItems(rows)); err != nil {
			return fail(err)
		}
		return 0
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stdout, "(no matching downloads)")
		return 0
	}
	fmt.Fprint(os.Stdout, neat.FormatList(rows))
	return 0
}

func cmdShow(args []string) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "path to NeatDB.db")
	reveal := fs.Bool("reveal", false, "print Cookie and POST body")
	asJSON := fs.Bool("json", false, "print JSON")
	flags, pos := reorderArgs(args, valueFlags())
	if err := fs.Parse(flags); err != nil {
		fmt.Fprintf(os.Stderr, "ndmcli: %v\n", err)
		return 2
	}
	id, err := oneID(pos)
	if err != nil {
		return fail(err)
	}
	st, err := openDB(*dbPath)
	if err != nil {
		return fail(err)
	}
	defer st.Close()
	d, err := st.Get(id)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		b, err := neat.MarshalShow(d, *reveal)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(os.Stdout, "%s\n", b)
		return 0
	}
	fmt.Fprint(os.Stdout, neat.FormatShow(d, *reveal))
	return 0
}

func cmdCurl(args []string) int {
	fs := flag.NewFlagSet("curl", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "path to NeatDB.db")
	output := fs.String("o", "", "output file, passed to curl --output")
	fs.StringVar(output, "output", "", "output file, passed to curl --output")
	resume := fs.Bool("resume", false, "add curl --continue-at -")
	flags, pos := reorderArgs(args, valueFlags())
	if err := fs.Parse(flags); err != nil {
		fmt.Fprintf(os.Stderr, "ndmcli: %v\n", err)
		return 2
	}
	ids, err := ids(pos)
	if err != nil {
		return fail(err)
	}
	if *output != "" && len(ids) > 1 {
		return fail(errors.New("-o with more than one id would write every job to the same file"))
	}
	st, err := openDB(*dbPath)
	if err != nil {
		return fail(err)
	}
	defer st.Close()
	for _, id := range ids {
		d, err := st.Get(id)
		if err != nil {
			return fail(err)
		}
		req, err := neat.BuildRequest(d)
		if err != nil {
			return fail(err)
		}
		if neat.StreamKind(d.Kind) {
			fmt.Fprintf(os.Stderr, "ndmcli: id %d is %s; curl will save that URL, not the media\n", id, d.Kind)
		}
		out := *output
		if out == "" {
			out, err = neat.OutputPath(d.Folder, req.Filename, "", false)
			if err != nil {
				return fail(err)
			}
		}
		argv, err := neat.CurlArgs(req, out, *resume)
		if err != nil {
			return fail(err)
		}
		if req.Header.Get("Cookie") != "" {
			fmt.Fprintf(os.Stderr, "ndmcli: id %d includes a Cookie header\n", id)
		}
		fmt.Fprintln(os.Stdout, neat.FormatShell(argv))
	}
	return 0
}

func cmdExport(args []string) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "path to NeatDB.db")
	status := fs.String("status", "open", "used when no ids are given: open, all, paused, error, or complete")
	cookie := fs.Bool("cookie", false, "only jobs that stored a Cookie header")
	name := fs.String("name", "", "file name substring")
	tool := fs.String("tool", "both", "curl, aria2c, or both")
	save := fs.String("save", "", "write the script to this path (mode 0600) instead of stdout")
	output := fs.String("o", "", "file name to use on the other machine")
	fs.StringVar(output, "output", "", "file name to use on the other machine")
	n := fs.Int("n", 0, "aria2c connections (0 means 8 when the server accepts ranges)")
	flags, pos := reorderArgs(args, valueFlags())
	if err := fs.Parse(flags); err != nil {
		fmt.Fprintf(os.Stderr, "ndmcli: %v\n", err)
		return 2
	}
	which, err := exportTool(*tool)
	if err != nil {
		return fail(err)
	}
	var idList []int64
	if len(pos) == 0 {
		// filled after the database opens
	} else {
		idList, err = ids(pos)
		if err != nil {
			return fail(err)
		}
	}
	if *output != "" && (len(pos) != 1) {
		return fail(errors.New("-o names one file; pass one id, or omit -o and keep each job's own file name"))
	}
	st, err := openDB(*dbPath)
	if err != nil {
		return fail(err)
	}
	defer st.Close()
	var downloads []neat.Download
	if len(pos) == 0 {
		downloads, err = st.List(neat.Filter{Status: *status, Cookie: *cookie, Name: *name})
		if err != nil {
			return fail(err)
		}
		if len(downloads) == 0 {
			return fail(errors.New("no matching downloads to export"))
		}
		full := make([]neat.Download, 0, len(downloads))
		for _, row := range downloads {
			d, err := st.Get(row.ID)
			if err != nil {
				return fail(err)
			}
			full = append(full, d)
		}
		downloads = full
	} else {
		for _, id := range idList {
			d, err := st.Get(id)
			if err != nil {
				return fail(err)
			}
			downloads = append(downloads, d)
		}
	}
	seen := map[string]struct{}{}
	jobs := make([]neat.ExportJob, 0, len(downloads))
	for _, d := range downloads {
		req, err := neat.BuildRequest(d)
		if err != nil {
			return fail(err)
		}
		if neat.StreamKind(d.Kind) {
			fmt.Fprintf(os.Stderr, "ndmcli: id %d is %s; the command saves that URL, not the media segments\n", d.ID, d.Kind)
		}
		fileName := exportName(req, *output, seen)
		curlArgv, err := neat.CurlArgs(req, fileName, true)
		if err != nil {
			return fail(err)
		}
		job := neat.ExportJob{ID: d.ID, Curl: neat.FormatShell(curlArgv)}
		if req.Header.Get("Cookie") != "" {
			fmt.Fprintf(os.Stderr, "ndmcli: id %d includes a Cookie header\n", d.ID)
		}
		if which != "curl" {
			ariaArgv, err := neat.Aria2Args(req, neat.Aria2Options{Output: fileName, Connections: *n})
			if err != nil {
				if which == "aria2c" {
					return fail(err)
				}
				fmt.Fprintf(os.Stderr, "ndmcli: %v\n", err)
			} else {
				job.Aria = neat.FormatShell(ariaArgv)
			}
		}
		jobs = append(jobs, job)
	}
	if *save != "" {
		script, err := neat.FormatExport(which, jobs)
		if err != nil {
			return fail(err)
		}
		if err := os.WriteFile(*save, []byte(script), 0o600); err != nil {
			return fail(err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s (%d jobs, mode 0600)\n", *save, len(jobs))
		if which == "both" {
			fmt.Fprintf(os.Stderr, "on the other machine: sh %s curl   or   sh %s aria2c\n", filepath.Base(*save), filepath.Base(*save))
		} else {
			fmt.Fprintf(os.Stderr, "on the other machine: sh %s\n", filepath.Base(*save))
		}
		return 0
	}
	if which == "both" {
		script, err := neat.FormatExport(which, jobs)
		if err != nil {
			return fail(err)
		}
		fmt.Fprint(os.Stdout, script)
		return 0
	}
	text, err := neat.FormatCommands(which, jobs)
	if err != nil {
		return fail(err)
	}
	fmt.Fprint(os.Stdout, text)
	return 0
}

func exportTool(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "both":
		return "both", nil
	case "curl":
		return "curl", nil
	case "aria2", "aria2c":
		return "aria2c", nil
	default:
		return "", fmt.Errorf("unknown tool %q (use curl, aria2c, or both)", s)
	}
}

func exportName(req neat.Request, explicit string, seen map[string]struct{}) string {
	name := req.Filename
	if explicit != "" {
		name = explicit
	} else {
		name = filepath.Base(name)
	}
	if _, ok := seen[name]; ok {
		name = fmt.Sprintf("%d-%s", req.ID, filepath.Base(name))
	}
	seen[name] = struct{}{}
	return name
}

func cmdDownload(args []string) int {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "path to NeatDB.db")
	output := fs.String("o", "", "output file or directory")
	fs.StringVar(output, "output", "", "output file or directory")
	n := fs.Int("n", 1, "connections (max 8)")
	resume := fs.Bool("resume", false, "continue a partial output file")
	adopt := fs.Bool("adopt", false, "seed the file from Neat's seg.xN pieces, then download the gaps")
	force := fs.Bool("force", false, "download an hls or media URL as a single file")
	flags, pos := reorderArgs(args, valueFlags())
	if err := fs.Parse(flags); err != nil {
		fmt.Fprintf(os.Stderr, "ndmcli: %v\n", err)
		return 2
	}
	// Neat's own cap is 8. Use it for --adopt when -n was left alone.
	if *adopt && !flagPassed(flags, "n") {
		*n = 8
	}
	ids, err := ids(pos)
	if err != nil {
		return fail(err)
	}
	if *output != "" && *output != "-" && len(ids) > 1 {
		st, err := os.Stat(*output)
		if err != nil || !st.IsDir() {
			return fail(errors.New("-o must be a directory when downloading more than one id"))
		}
	}
	st, err := openDB(*dbPath)
	if err != nil {
		return fail(err)
	}
	dbFile := st.Path()
	// Load every job, then close the database before any long transfer.
	type item struct {
		d   neat.Download
		req neat.Request
	}
	items := make([]item, 0, len(ids))
	for _, id := range ids {
		d, err := st.Get(id)
		if err != nil {
			st.Close()
			return fail(err)
		}
		req, err := neat.BuildRequest(d)
		if err != nil {
			st.Close()
			return fail(err)
		}
		items = append(items, item{d: d, req: req})
	}
	st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dl := neat.NewDownloader()

	for _, it := range items {
		if neat.StreamKind(it.d.Kind) && !*force {
			return fail(fmt.Errorf("id %d is %s; the stored URL is a playlist. Pass --force to save that URL, or use a media tool", it.d.ID, it.d.Kind))
		}
		out, err := resolveOutput(it.d, it.req, *output)
		if err != nil {
			return fail(err)
		}
		if err := refuseDataDir(out, dbFile); err != nil {
			return fail(err)
		}
		if dir := filepath.Dir(out); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fail(err)
			}
		}
		if *adopt {
			fmt.Fprintf(os.Stderr, "ndmcli: pause id %d in Neat Download Manager before adopting it\n", it.d.ID)
		}
		job := neat.Job{
			ID:          it.req.ID,
			Method:      it.req.Method,
			URL:         it.req.URL,
			Header:      it.req.Header,
			Body:        it.req.Body,
			Output:      out,
			FileSize:    it.req.FileSize,
			Resumable:   it.req.Resumable,
			ForceRange:  *resume || *adopt,
			Connections: *n,
			Resume:      *resume,
		}
		if *adopt {
			job.AdoptDir = neat.ItemDir(dbFile, it.d.ID)
		}
		res, err := dl.Run(ctx, job)
		if err != nil {
			return fail(err)
		}
		if res.Existing {
			fmt.Fprintf(os.Stderr, "already complete\n")
		}
		fmt.Fprintln(os.Stdout, res.Path)
	}
	return 0
}

func resolveOutput(d neat.Download, req neat.Request, explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	isDir := false
	if explicit != "" && explicit != "-" {
		if st, err := os.Stat(explicit); err == nil && st.IsDir() {
			isDir = true
		}
	}
	return neat.OutputPath(d.Folder, req.Filename, explicit, isDir)
}

func refuseDataDir(out, dbFile string) error {
	data := filepath.Dir(dbFile)
	out = filepath.Clean(out)
	data = filepath.Clean(data)
	rel, err := filepath.Rel(data, out)
	if err != nil {
		return nil
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	return fmt.Errorf("refusing to write %s inside the Neat Download Manager data directory", out)
}

func openDB(flagPath string) (*neat.Store, error) {
	path := flagPath
	if path == "" {
		var err error
		path, err = neat.DefaultDBPath()
		if err != nil {
			return nil, err
		}
	}
	return neat.Open(path)
}

func oneID(pos []string) (int64, error) {
	ids, err := ids(pos)
	if err != nil {
		return 0, err
	}
	if len(ids) != 1 {
		return 0, errors.New("pass one download id")
	}
	return ids[0], nil
}

func ids(pos []string) ([]int64, error) {
	if len(pos) == 0 {
		return nil, errors.New("pass a download id")
	}
	out := make([]int64, 0, len(pos))
	for _, s := range pos {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id < 0 {
			return nil, fmt.Errorf("bad download id %q", s)
		}
		out = append(out, id)
	}
	return out, nil
}

func valueFlags() map[string]bool {
	return map[string]bool{
		"db": true, "status": true, "name": true,
		"o": true, "output": true, "n": true,
		"tool": true, "save": true,
	}
}

func flagPassed(flags []string, name string) bool {
	for _, a := range flags {
		if a == "-"+name || a == "--"+name || strings.HasPrefix(a, "-"+name+"=") || strings.HasPrefix(a, "--"+name+"=") {
			return true
		}
	}
	return false
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "ndmcli: %v\n", err)
	return 1
}
