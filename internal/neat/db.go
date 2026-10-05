package neat

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

// Download is one row from the downloads table, plus its request headers
// when they were loaded.
type Download struct {
	ID        int64
	URL       string
	Method    string
	Filename  string
	Kind      string
	FileSize  int64
	Category  string
	Status    string
	UserAgent string
	Resumable int64
	PageURL   string
	Mime      string
	PostData  string
	Folder    string
	HasCookie bool
	Headers   []Header
}

// Header is one stored request header. Name keeps the original spelling.
type Header struct {
	Name  string
	Value string
}

// Filter selects rows for List. Status is open, all, paused, error, or
// complete. Empty status means open (anything not Complete).
type Filter struct {
	Status string
	Cookie bool
	Name   string
}

// Store is a read-only view of NeatDB.db. Close it before a long download
// so Neat Download Manager can keep writing the file.
type Store struct {
	path string
	db   *sql.DB
}

// DefaultDBPath is the macOS database, or NDM_DB when that is set.
func DefaultDBPath() (string, error) {
	if p := os.Getenv("NDM_DB"); p != "" {
		return p, nil
	}
	if runtime.GOOS != "darwin" {
		return "", errors.New("set --db or NDM_DB to NeatDB.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "com.NeatDownloadManager", "NeatDB.db"), nil
}

// DefaultSaveDir reads DownloadDirectory from the macOS preferences
// plist. It falls back to ~/Downloads.
func DefaultSaveDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	fallback := filepath.Join(home, "Downloads")
	if runtime.GOOS != "darwin" {
		return fallback
	}
	plist := filepath.Join(home, "Library", "Preferences", "com.NeatDownloadManager.plist")
	return readPlistString(plist, "DownloadDirectory", fallback)
}

// ItemDir is the per-download folder that holds segments.bin and seg.xN.
func ItemDir(dbPath string, id int64) string {
	return filepath.Join(filepath.Dir(dbPath), strconv.FormatInt(id, 10))
}

// Open opens NeatDB.db read-only.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("empty database path")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("Neat Download Manager database %s: %w", path, err)
	}
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: "mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)",
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &Store{path: path, db: db}, nil
}

// Path is the database file this store reads.
func (s *Store) Path() string { return s.path }

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// List returns downloads matching f, newest id first. Headers are not loaded.
func (s *Store) List(f Filter) ([]Download, error) {
	where, args, err := f.clause()
	if err != nil {
		return nil, err
	}
	q := `
SELECT id, url, method, filename, ltype, filesize, category, status,
       useragent, resumable, pageurl, mimetype, postdata, folderpath,
       EXISTS(
         SELECT 1 FROM headers h
         WHERE h.id = downloads.id AND h.header LIKE 'Cookie:%'
       )
FROM downloads
WHERE ` + where + `
ORDER BY id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Download
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Get loads one download and its request headers.
func (s *Store) Get(id int64) (Download, error) {
	row := s.db.QueryRow(`
SELECT id, url, method, filename, ltype, filesize, category, status,
       useragent, resumable, pageurl, mimetype, postdata, folderpath,
       EXISTS(
         SELECT 1 FROM headers h
         WHERE h.id = downloads.id AND h.header LIKE 'Cookie:%'
       )
FROM downloads WHERE id = ?`, id)
	d, err := scanDownload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Download{}, fmt.Errorf("no download with id %d", id)
	}
	if err != nil {
		return Download{}, err
	}
	hrows, err := s.db.Query(`SELECT header FROM headers WHERE id = ? ORDER BY rowid`, id)
	if err != nil {
		return Download{}, err
	}
	defer hrows.Close()
	for hrows.Next() {
		var raw string
		if err := hrows.Scan(&raw); err != nil {
			return Download{}, err
		}
		h, err := ParseHeader(raw)
		if err != nil {
			return Download{}, fmt.Errorf("download %d: %w", id, err)
		}
		d.Headers = append(d.Headers, h)
		if strings.EqualFold(h.Name, "Cookie") && h.Value != "" {
			d.HasCookie = true
		}
	}
	return d, hrows.Err()
}

func (f Filter) clause() (string, []any, error) {
	status := f.Status
	if status == "" {
		status = "open"
	}
	var where string
	switch status {
	case "open":
		where = "status NOT LIKE 'Complete%'"
	case "all":
		where = "1=1"
	case "paused":
		where = "status LIKE 'Paused%'"
	case "error":
		where = "status LIKE 'Error%'"
	case "complete":
		where = "status LIKE 'Complete%'"
	default:
		return "", nil, fmt.Errorf("unknown status %q (use open, all, paused, error, complete)", status)
	}
	var args []any
	if f.Cookie {
		where += " AND EXISTS(SELECT 1 FROM headers h WHERE h.id = downloads.id AND h.header LIKE 'Cookie:%')"
	}
	if f.Name != "" {
		where += " AND filename LIKE ? ESCAPE '\\'"
		args = append(args, "%"+escapeLike(f.Name)+"%")
	}
	return where, args, nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDownload(sc scanner) (Download, error) {
	var d Download
	var url, method, filename, kind, category, status sql.NullString
	var userAgent, pageURL, mime, post, folder sql.NullString
	var size, resumable sql.NullInt64
	var has int
	err := sc.Scan(
		&d.ID, &url, &method, &filename, &kind, &size, &category, &status,
		&userAgent, &resumable, &pageURL, &mime, &post, &folder, &has,
	)
	if err != nil {
		return Download{}, err
	}
	d.URL = url.String
	d.Method = method.String
	d.Filename = filename.String
	d.Kind = kind.String
	d.FileSize = size.Int64
	d.Category = category.String
	d.Status = status.String
	d.UserAgent = userAgent.String
	d.Resumable = resumable.Int64
	d.PageURL = pageURL.String
	d.Mime = mime.String
	d.PostData = post.String
	d.Folder = folder.String
	d.HasCookie = has == 1
	if d.Method == "" {
		d.Method = "GET"
	}
	return d, nil
}

// ParseHeader splits a stored "Name: value" row.
func ParseHeader(raw string) (Header, error) {
	raw = strings.TrimRight(raw, "\r\n")
	name, value, ok := strings.Cut(raw, ":")
	if !ok || strings.TrimSpace(name) == "" {
		return Header{}, fmt.Errorf("malformed header row")
	}
	return Header{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value)}, nil
}
