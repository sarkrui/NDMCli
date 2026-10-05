package neat

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// defaultUA is used only when a job stored no user agent. Sending
// Go-http-client gets blocked by a lot of file hosts.
const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// Request is the HTTP request Neat Download Manager would send, without
// a Range header. Callers add Range when they continue a partial file.
type Request struct {
	ID       int64
	Method   string
	URL      string
	Header   http.Header
	Body     []byte
	Kind     string
	FileSize int64
	// Resumable is true only when the database recorded a 1. Neat uses
	// 0 for "server did not accept ranges" and -1 for "not checked".
	Resumable bool
	Filename  string
	Folder    string
}

// BuildRequest assembles the headers stored for d. Accept-Encoding is
// forced to identity so the body is the file itself. Neat's own engine
// sends the same value. Hop-by-hop headers and Content-Length are left
// to the HTTP client.
func BuildRequest(d Download) (Request, error) {
	if strings.TrimSpace(d.URL) == "" {
		return Request{}, fmt.Errorf("download %d has an empty url", d.ID)
	}
	method := strings.ToUpper(strings.TrimSpace(d.Method))
	if method == "" {
		method = http.MethodGet
	}
	h := make(http.Header)
	h.Set("Accept", "*/*")
	h.Set("Accept-Encoding", "identity")
	h.Set("Accept-Language", "en-US,en;q=0.9")
	h.Set("Accept-Charset", "*")
	if ua := strings.TrimSpace(d.UserAgent); ua != "" {
		h.Set("User-Agent", ua)
	} else {
		h.Set("User-Agent", defaultUA)
	}
	for _, hdr := range d.Headers {
		if skipHeader(hdr.Name) {
			continue
		}
		h.Set(hdr.Name, hdr.Value)
	}
	if h.Get("Referer") == "" && d.PageURL != "" {
		h.Set("Referer", d.PageURL)
	}
	var body []byte
	if method != http.MethodGet && method != http.MethodHead && d.PostData != "" {
		body = []byte(d.PostData)
	}
	return Request{
		ID:        d.ID,
		Method:    method,
		URL:       d.URL,
		Header:    h,
		Body:      body,
		Kind:      d.Kind,
		FileSize:  d.FileSize,
		Resumable: d.Resumable == 1,
		Filename:  FileName(d),
		Folder:    d.Folder,
	}, nil
}

func skipHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Host", "Content-Length", "Range", "Connection", "Transfer-Encoding",
		"Keep-Alive", "Proxy-Connection", "Upgrade", "Te", "Trailer", "Accept-Encoding":
		return true
	default:
		return false
	}
}

// FileName picks a single path element for the saved file.
func FileName(d Download) string {
	name := strings.TrimSpace(d.Filename)
	if name == "" || name == "." || name == ".." {
		if u, err := url.Parse(d.URL); err == nil {
			name = path.Base(u.Path)
		}
	}
	name = filepath.Base(name)
	if name == "" || name == "." || name == string(filepath.Separator) || name == ".." {
		return fmt.Sprintf("ndm-%d", d.ID)
	}
	return name
}

// OutputPath joins folder and file name. An explicit path from the user
// is returned as-is unless it names a directory.
func OutputPath(folder, fileName, explicit string, explicitIsDir bool) (string, error) {
	if explicit != "" && explicit != "-" {
		if explicitIsDir {
			return safeJoin(explicit, fileName)
		}
		return explicit, nil
	}
	if explicit == "-" {
		return "-", nil
	}
	if folder == "" {
		folder = DefaultSaveDir()
	}
	if folder == "" {
		return "", fmt.Errorf("no output directory; pass -o")
	}
	return safeJoin(folder, fileName)
}

func safeJoin(dir, name string) (string, error) {
	name = filepath.Base(name)
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("missing file name")
	}
	return filepath.Join(dir, name), nil
}

// HasCookie reports whether the prepared request will send a cookie.
func HasCookie(h http.Header) bool {
	return h.Get("Cookie") != ""
}

// StreamKind is hls or media, where the stored URL is a playlist or a
// player manifest rather than the file itself.
func StreamKind(kind string) bool {
	switch strings.ToLower(kind) {
	case "hls", "media":
		return true
	default:
		return false
	}
}
