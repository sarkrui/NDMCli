package neat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxConnections = 8
	defaultChunk   = 1 << 20
)

// Job is one prepared download.
type Job struct {
	ID       int64
	Method   string
	URL      string
	Header   http.Header
	Body     []byte
	Output   string
	FileSize int64
	// Resumable mirrors the database flag. ForceRange lets --resume and
	// --adopt send Range even when the flag was not 1.
	Resumable   bool
	ForceRange  bool
	Connections int
	Resume      bool
	AdoptDir    string
	MinChunk    int64
}

// Result is a finished (or already finished) file.
type Result struct {
	Path     string
	Bytes    int64
	Existing bool
}

// Downloader performs the HTTP transfer.
type Downloader struct {
	Client *http.Client
	Stderr io.Writer
}

// NewDownloader returns a client that does not transparently gzip the body.
// Accept-Encoding: identity has to reach the server, and a gzip layer would
// change the file on disk.
func NewDownloader() *Downloader {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		DisableCompression:    true,
		ForceAttemptHTTP2:     true,
	}
	return &Downloader{
		Client: &http.Client{Transport: tr},
		Stderr: os.Stderr,
	}
}

// Run downloads job.Output. It follows redirects with net/http's default
// policy: the Cookie header is kept for the original host and its
// subdomains, and dropped for any other host.
func (d *Downloader) Run(ctx context.Context, job Job) (Result, error) {
	if job.Output == "" || job.Output == "-" {
		return Result{}, errors.New("download needs a file path; use ndmcli curl to write to stdout")
	}
	if job.URL == "" {
		return Result{}, errors.New("empty url")
	}
	if job.Method == "" {
		job.Method = http.MethodGet
	}
	if job.Connections < 1 {
		job.Connections = 1
	}
	if job.Connections > maxConnections {
		job.Connections = maxConnections
	}
	if job.MinChunk <= 0 {
		job.MinChunk = defaultChunk
	}
	if d.Client == nil {
		d.Client = NewDownloader().Client
	}
	if d.Stderr == nil {
		d.Stderr = io.Discard
	}
	if !job.Resumable && !job.ForceRange {
		job.Connections = 1
	}
	if job.Method != http.MethodGet && job.Method != http.MethodHead {
		job.Connections = 1
	}
	fmt.Fprintf(d.Stderr, "id %d  %s  %s\n", job.ID, filepath.Base(job.Output), hostOf(job.URL))

	sidePath := SidecarPath(job.Output)
	st, err := loadRangeState(sidePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	if st != nil {
		if st.id != job.ID || (job.FileSize > 0 && st.size != job.FileSize) {
			return Result{}, fmt.Errorf("%s belongs to a different download; remove it or pick another -o", sidePath)
		}
		if _, err := os.Stat(job.Output); err != nil {
			return Result{}, fmt.Errorf("found %s but %s is missing; remove the state file to start over", sidePath, job.Output)
		}
		return d.runRanges(ctx, job, st)
	}

	if job.AdoptDir != "" {
		return d.adopt(ctx, job)
	}

	fi, statErr := os.Stat(job.Output)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Result{}, statErr
	}
	if exists && job.FileSize > 0 && fi.Size() == job.FileSize && job.Resume {
		return Result{Path: job.Output, Bytes: fi.Size(), Existing: true}, nil
	}
	if exists && job.FileSize > 0 && fi.Size() > job.FileSize {
		return Result{}, fmt.Errorf("%s is larger than the download (%d > %d)", job.Output, fi.Size(), job.FileSize)
	}
	if exists && !job.Resume {
		return Result{}, fmt.Errorf("%s already exists; pass --resume or remove it", job.Output)
	}
	if exists && job.Resume {
		have := fi.Size()
		if job.FileSize > 0 && have > job.FileSize {
			return Result{}, fmt.Errorf("%s is larger than the download", job.Output)
		}
		end := int64(-1)
		if job.FileSize > 0 {
			end = job.FileSize - 1
		}
		if !job.Resumable && !job.ForceRange {
			return Result{}, fmt.Errorf("this server was not marked resumable; remove %s or pass nothing and a new -o", job.Output)
		}
		parts := []Part{{Start: 0, End: end, Have: have}}
		// Prefix resume writes straight into the existing file. No sidecar:
		// the file length is the cursor.
		return d.runSingle(ctx, job, parts[0], true)
	}

	if job.FileSize > 0 && job.Connections > 1 && (job.Resumable || job.ForceRange) {
		parts, err := Split(job.FileSize, job.Connections, job.MinChunk)
		if err != nil {
			return Result{}, err
		}
		if len(parts) > 1 {
			st = newRangeState(sidePath, job.ID, job.FileSize, parts)
			if err := st.Flush(); err != nil {
				return Result{}, err
			}
			if err := createSparse(job.Output, job.FileSize); err != nil {
				return Result{}, err
			}
			return d.runRanges(ctx, job, st)
		}
	}

	end := int64(-1)
	if job.FileSize > 0 {
		end = job.FileSize - 1
	}
	return d.runSingle(ctx, job, Part{Start: 0, End: end}, false)
}

func (d *Downloader) adopt(ctx context.Context, job Job) (Result, error) {
	if job.FileSize <= 0 {
		return Result{}, errors.New("cannot adopt a download with an unknown size")
	}
	if _, err := os.Stat(job.Output); err == nil {
		return Result{}, fmt.Errorf("%s already exists; remove it before --adopt", job.Output)
	}
	segs, err := LoadSegments(job.AdoptDir)
	if err != nil {
		return Result{}, err
	}
	if err := ValidateCoverage(segs, job.FileSize); err != nil {
		return Result{}, err
	}
	parts, err := PartsFromSegmentFiles(job.AdoptDir, segs)
	if err != nil {
		return Result{}, err
	}
	// Sidecar before the sparse file. A truncated file reports the full
	// size even when it is still empty, so the state file has to exist
	// first or a later --resume would treat that hole as finished.
	seeded := append([]Part(nil), parts...)
	blank := make([]Part, len(parts))
	for i, p := range parts {
		blank[i] = Part{Start: p.Start, End: p.End}
	}
	st := newRangeState(SidecarPath(job.Output), job.ID, job.FileSize, blank)
	if err := st.Flush(); err != nil {
		return Result{}, err
	}
	if err := createSparse(job.Output, job.FileSize); err != nil {
		return Result{}, err
	}
	f, err := os.OpenFile(job.Output, os.O_RDWR, 0)
	if err != nil {
		return Result{}, err
	}
	for _, seg := range segs {
		var have int64
		for _, p := range seeded {
			if p.Start == seg.Start {
				have = p.Have
				break
			}
		}
		if err := CopySegmentPrefix(f, job.AdoptDir, seg, have); err != nil {
			f.Close()
			return Result{}, err
		}
	}
	if err := f.Close(); err != nil {
		return Result{}, err
	}
	st = newRangeState(SidecarPath(job.Output), job.ID, job.FileSize, seeded)
	if err := st.Flush(); err != nil {
		return Result{}, err
	}
	fmt.Fprintf(d.Stderr, "adopted %s of partial data from Neat Download Manager\n", FormatBytes(st.Done()))
	return d.runRanges(ctx, job, st)
}

func createSparse(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Truncate(size)
}

func (d *Downloader) runSingle(ctx context.Context, job Job, part Part, existing bool) (Result, error) {
	flags := os.O_CREATE | os.O_RDWR
	if !existing {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(job.Output, flags, 0o644)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()

	var have atomic.Int64
	have.Store(part.Have)
	start := part.Start + part.Have
	sendRange := start > 0
	var total int64
	if part.End >= 0 {
		total = part.End - part.Start + 1
	}
	stop, stopped := d.startProgress(func() int64 { return have.Load() }, total)
	n, err := d.transfer(ctx, job, f, start, part.End, sendRange, func(wrote int64) error {
		have.Add(wrote)
		return nil
	})
	d.stopProgress(stop, stopped)
	if err != nil {
		return Result{}, err
	}
	if err := f.Sync(); err != nil {
		return Result{}, err
	}
	return Result{Path: job.Output, Bytes: have.Load(), Existing: existing && n == 0}, nil
}

func (d *Downloader) runRanges(ctx context.Context, job Job, st *rangeState) (Result, error) {
	f, err := os.OpenFile(job.Output, os.O_RDWR, 0)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()

	parts := st.Parts()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stop, stopped := d.startProgress(st.Done, job.FileSize)

	sem := make(chan struct{}, job.Connections)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var first error
	setErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if first == nil {
			first = err
			cancel()
		}
		errMu.Unlock()
	}

	for i := range parts {
		p := parts[i]
		span := p.Span()
		if span >= 0 && p.Have >= span {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, p Part) {
			defer wg.Done()
			defer func() { <-sem }()
			start := p.Start + p.Have
			_, err := d.transfer(ctx, job, f, start, p.End, true, func(n int64) error {
				return st.Add(i, n)
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				setErr(err)
			}
		}(i, p)
	}
	wg.Wait()
	d.stopProgress(stop, stopped)
	if first == nil {
		first = ctx.Err()
	}
	if first != nil {
		_ = st.Flush()
		if errors.Is(first, context.Canceled) {
			return Result{}, fmt.Errorf("interrupted; partial file kept at %s", job.Output)
		}
		return Result{}, first
	}
	if err := f.Sync(); err != nil {
		return Result{}, err
	}
	if err := st.Remove(); err != nil {
		return Result{}, err
	}
	return Result{Path: job.Output, Bytes: job.FileSize}, nil
}

// transfer fetches bytes [start, end] inclusive and writes them at start.
// end < 0 means "through EOF". sendRange adds a Range header. onWrite is
// called with each successful chunk.
func (d *Downloader) transfer(ctx context.Context, job Job, dst io.WriterAt, start, end int64, sendRange bool, onWrite func(int64) error) (int64, error) {
	if end >= 0 && start > end {
		return 0, nil
	}
	want := int64(-1)
	if end >= 0 {
		want = end - start + 1
	}

	resp, err := d.do(ctx, job, start, end, sendRange)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && want == 0 {
		return 0, nil
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && end >= 0 && start > end {
		return 0, nil
	}
	host := hostOf(job.URL)
	switch resp.StatusCode {
	case http.StatusOK:
		if sendRange && start > 0 {
			return 0, fmt.Errorf("%s ignored the Range header and sent the whole file", host)
		}
		if sendRange && end >= 0 && !(start == 0 && end == job.FileSize-1) {
			return 0, fmt.Errorf("%s ignored the Range header", host)
		}
	case http.StatusPartialContent:
		if err := checkContentRange(resp.Header.Get("Content-Range"), start); err != nil {
			return 0, fmt.Errorf("%s: %w", host, err)
		}
	default:
		return 0, fmt.Errorf("%s returned HTTP %d", host, resp.StatusCode)
	}
	if cl := resp.ContentLength; cl >= 0 && want >= 0 && cl != want && resp.StatusCode == http.StatusPartialContent {
		return 0, fmt.Errorf("%s: content-length %d, want %d", host, cl, want)
	}

	buf := make([]byte, 256*1024)
	var wrote int64
	off := start
	for {
		if want >= 0 && wrote >= want {
			break
		}
		toRead := len(buf)
		if want >= 0 && int64(toRead) > want-wrote {
			toRead = int(want - wrote)
		}
		nr, rerr := resp.Body.Read(buf[:toRead])
		if nr > 0 {
			nw, werr := dst.WriteAt(buf[:nr], off)
			off += int64(nw)
			wrote += int64(nw)
			if werr != nil {
				return wrote, werr
			}
			if nw != nr {
				return wrote, io.ErrShortWrite
			}
			if onWrite != nil {
				if err := onWrite(int64(nw)); err != nil {
					return wrote, err
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if errors.Is(rerr, context.Canceled) {
				return wrote, rerr
			}
			return wrote, rerr
		}
	}
	if want >= 0 && wrote != want {
		return wrote, fmt.Errorf("%s: got %d bytes, want %d", host, wrote, want)
	}
	return wrote, nil
}

func (d *Downloader) do(ctx context.Context, job Job, start, end int64, sendRange bool) (*http.Response, error) {
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		var body io.Reader
		if len(job.Body) > 0 {
			body = strings.NewReader(string(job.Body))
		}
		req, err := http.NewRequestWithContext(ctx, job.Method, job.URL, body)
		if err != nil {
			return nil, err
		}
		req.Header = job.Header.Clone()
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		if sendRange {
			if end >= 0 {
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
			} else {
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))
			}
		}
		resp, err := d.Client.Do(req)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, err
			}
			last = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			resp.Body.Close()
			last = fmt.Errorf("%s returned HTTP %d", hostOf(job.URL), resp.StatusCode)
			continue
		}
		return resp, nil
	}
	if last == nil {
		last = errors.New("request failed")
	}
	return nil, last
}

func checkContentRange(v string, start int64) error {
	if v == "" {
		return errors.New("206 response without Content-Range")
	}
	// bytes START-END/TOTAL or bytes START-END/*
	rest, ok := strings.CutPrefix(v, "bytes ")
	if !ok {
		return fmt.Errorf("unexpected Content-Range %q", v)
	}
	left, _, _ := strings.Cut(rest, "/")
	a, _, ok := strings.Cut(left, "-")
	if !ok {
		return fmt.Errorf("unexpected Content-Range %q", v)
	}
	got, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return fmt.Errorf("unexpected Content-Range %q", v)
	}
	if got != start {
		return fmt.Errorf("Content-Range starts at %d, requested %d", got, start)
	}
	return nil
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "server"
	}
	return u.Host
}

func (d *Downloader) startProgress(done func() int64, total int64) (chan struct{}, chan struct{}) {
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		d.progress(stop, done, total)
	}()
	return stop, stopped
}

func (d *Downloader) stopProgress(stop, stopped chan struct{}) {
	close(stop)
	<-stopped
}

func (d *Downloader) progress(stop <-chan struct{}, done func() int64, total int64) {
	tty := isTTY(d.Stderr)
	interval := 200 * time.Millisecond
	if !tty {
		interval = 5 * time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	start := time.Now()
	initial := done()
	var last int64 = -1
	line := func() string {
		n := done()
		return progressLine(n, total, n-initial, time.Since(start))
	}
	for {
		select {
		case <-stop:
			text := line()
			if tty {
				fmt.Fprint(d.Stderr, "\r"+text+"\n")
			} else {
				fmt.Fprintln(d.Stderr, text)
			}
			return
		case <-tick.C:
			n := done()
			if n == last && !tty {
				continue
			}
			last = n
			text := progressLine(n, total, n-initial, time.Since(start))
			if tty {
				fmt.Fprint(d.Stderr, "\r"+text+"   ")
			} else {
				fmt.Fprintln(d.Stderr, text)
			}
		}
	}
}

func progressLine(done, total, session int64, elapsed time.Duration) string {
	sec := elapsed.Seconds()
	if sec < 0.001 {
		sec = 0.001
	}
	if session < 0 {
		session = 0
	}
	rate := int64(float64(session) / sec)
	if total > 0 {
		pct := float64(done) / float64(total) * 100
		if pct > 100 {
			pct = 100
		}
		return fmt.Sprintf("%s / %s  %5.1f%%  %s/s", FormatBytes(done), FormatBytes(total), pct, FormatBytes(rate))
	}
	return fmt.Sprintf("%s  %s/s", FormatBytes(done), FormatBytes(rate))
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
