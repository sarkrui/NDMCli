package neat

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func newFixture(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "NeatDB.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schema := `
CREATE TABLE headers (id NUMERIC, header TEXT);
CREATE TABLE downloads (
  id INTEGER PRIMARY KEY,
  url TEXT, method TEXT, filename TEXT, ltype TEXT, filesize NUMERIC,
  category TEXT, status TEXT, bandwidthlimit NUMERIC, connections NUMERIC,
  lasttry NUMERIC, firsttry NUMERIC, useragent TEXT, resumable NUMERIC,
  pageurl TEXT, pagetitle TEXT, hittitle TEXT, mimetype TEXT, errortext TEXT,
  urla TEXT, postdata TEXT, folderpath TEXT
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO downloads
		(id, url, method, filename, ltype, filesize, category, status, useragent, resumable, pageurl, mimetype, postdata, folderpath)
		VALUES
		(1, 'https://files.example/a.bin', 'GET', 'a.bin', 'normal', 10, 'Misc', 'Paused ( 0% )', 'TestAgent/1', 1, 'https://example/page', 'application/octet-stream', '', ?),
		(2, 'https://files.example/done.zip', 'GET', 'done.zip', 'normal', 3, 'Compressed', 'Complete', 'TestAgent/1', 0, '', '', '', ''),
		(3, 'https://files.example/need-cookie', 'GET', 'secret.bin', 'normal', 4, 'Misc', 'Error ( 0% )', 'TestAgent/1', 1, '', '', '', ''),
		(4, 'https://files.example/100%_done.bin', 'POST', '100%_done.bin', 'normal', 2, 'Misc', 'Paused ( 1% )', 'TestAgent/1', 1, '', '', 'a=1&b=2', '')`,
		filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO headers (id, header) VALUES
		(1, 'Referer: https://example/page'),
		(3, 'Cookie: sid=abc; name=O''Brien'),
		(3, 'Origin: https://example'),
		(4, 'Content-Type: application/x-www-form-urlencoded'),
		(4, 'Cookie: session=xyz')`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestListAndGet(t *testing.T) {
	st := newFixture(t)
	open, err := st.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 3 {
		t.Fatalf("open jobs = %d, want 3", len(open))
	}
	if open[0].ID != 4 {
		t.Fatalf("newest id = %d", open[0].ID)
	}
	withCookie, err := st.List(Filter{Cookie: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(withCookie) != 2 {
		t.Fatalf("cookie jobs = %d", len(withCookie))
	}
	named, err := st.List(Filter{Status: "all", Name: `%_`})
	if err != nil {
		t.Fatal(err)
	}
	if len(named) != 1 || named[0].ID != 4 {
		t.Fatalf("name filter = %+v", named)
	}
	d, err := st.Get(3)
	if err != nil {
		t.Fatal(err)
	}
	if !d.HasCookie || len(d.Headers) != 2 {
		t.Fatalf("headers: %+v cookie %v", d.Headers, d.HasCookie)
	}
	if d.Headers[0].Name != "Cookie" || d.Headers[0].Value != "sid=abc; name=O'Brien" {
		t.Fatalf("cookie header = %#v", d.Headers[0])
	}
	if _, err := st.Get(99); err == nil {
		t.Fatal("expected missing id to fail")
	}
}
