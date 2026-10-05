package neat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/tabwriter"
)

// FormatBytes renders a file size in binary units. Negative means unknown.
func FormatBytes(n int64) string {
	if n < 0 {
		return "?"
	}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	v := float64(n)
	i := -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// FormatList renders a table. Cookie values are not included.
func FormatList(rows []Download) string {
	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tSIZE\tCOOKIE\tKIND\tFILE")
	for _, d := range rows {
		ck := "no"
		if d.HasCookie {
			ck = "yes"
		}
		kind := d.Kind
		if kind == "" {
			kind = "-"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
			d.ID, d.Status, FormatBytes(d.FileSize), ck, kind, FileName(d))
	}
	w.Flush()
	return b.String()
}

// ListItem is the JSON form of a list row. It never contains cookies.
type ListItem struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	Kind      string `json:"kind"`
	Method    string `json:"method"`
	Resumable int64  `json:"resumable"`
	HasCookie bool   `json:"has_cookie"`
	URL       string `json:"url"`
	Mime      string `json:"mime,omitempty"`
	Folder    string `json:"folder,omitempty"`
}

// ListItems converts rows for JSON output.
func ListItems(rows []Download) []ListItem {
	out := make([]ListItem, len(rows))
	for i, d := range rows {
		out[i] = ListItem{
			ID: d.ID, Status: d.Status, Filename: FileName(d), Size: d.FileSize,
			Kind: d.Kind, Method: d.Method, Resumable: d.Resumable,
			HasCookie: d.HasCookie, URL: d.URL, Mime: d.Mime, Folder: d.Folder,
		}
	}
	return out
}

// FormatShow prints one job. Cookie and the POST body are redacted unless
// reveal is set. curl is the path that always prints them.
func FormatShow(d Download, reveal bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "id:        %d\n", d.ID)
	fmt.Fprintf(&b, "status:    %s\n", d.Status)
	fmt.Fprintf(&b, "file:      %s\n", FileName(d))
	if d.Folder != "" {
		fmt.Fprintf(&b, "folder:    %s\n", d.Folder)
	}
	fmt.Fprintf(&b, "size:      %s\n", FormatBytes(d.FileSize))
	fmt.Fprintf(&b, "kind:      %s\n", d.Kind)
	fmt.Fprintf(&b, "method:    %s\n", d.Method)
	fmt.Fprintf(&b, "resumable: %d\n", d.Resumable)
	if d.Mime != "" {
		fmt.Fprintf(&b, "mime:      %s\n", d.Mime)
	}
	fmt.Fprintf(&b, "url:       %s\n", d.URL)
	if d.PageURL != "" {
		fmt.Fprintf(&b, "page:      %s\n", d.PageURL)
	}
	if d.UserAgent != "" {
		fmt.Fprintf(&b, "agent:     %s\n", d.UserAgent)
	}
	if d.PostData != "" {
		if reveal {
			fmt.Fprintf(&b, "post:      %s\n", d.PostData)
		} else {
			fmt.Fprintf(&b, "post:      [%d bytes]\n", len(d.PostData))
		}
	}
	if len(d.Headers) == 0 {
		fmt.Fprintf(&b, "headers:   (none)\n")
		return b.String()
	}
	fmt.Fprintf(&b, "headers:\n")
	for _, h := range d.Headers {
		if !reveal && redactHeader(h.Name) {
			fmt.Fprintf(&b, "  %s: [%d bytes]\n", h.Name, len(h.Value))
			continue
		}
		fmt.Fprintf(&b, "  %s: %s\n", h.Name, h.Value)
	}
	return b.String()
}

func redactHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Cookie", "Authorization", "Proxy-Authorization":
		return true
	default:
		return false
	}
}

// ShowJSON is the JSON form of one job. Secrets are redacted like FormatShow.
type ShowJSON struct {
	ID        int64    `json:"id"`
	Status    string   `json:"status"`
	Filename  string   `json:"filename"`
	Folder    string   `json:"folder,omitempty"`
	Size      int64    `json:"size"`
	Kind      string   `json:"kind"`
	Method    string   `json:"method"`
	Resumable int64    `json:"resumable"`
	Mime      string   `json:"mime,omitempty"`
	URL       string   `json:"url"`
	PageURL   string   `json:"page_url,omitempty"`
	UserAgent string   `json:"user_agent,omitempty"`
	Post      string   `json:"post,omitempty"`
	Headers   []Header `json:"headers"`
}

// MarshalShow encodes one download as JSON.
func MarshalShow(d Download, reveal bool) ([]byte, error) {
	post := d.PostData
	if post != "" && !reveal {
		post = fmt.Sprintf("[%d bytes]", len(post))
	}
	headers := make([]Header, len(d.Headers))
	for i, h := range d.Headers {
		headers[i] = h
		if !reveal && redactHeader(h.Name) {
			headers[i].Value = fmt.Sprintf("[%d bytes]", len(h.Value))
		}
	}
	return json.MarshalIndent(ShowJSON{
		ID: d.ID, Status: d.Status, Filename: FileName(d), Folder: d.Folder,
		Size: d.FileSize, Kind: d.Kind, Method: d.Method, Resumable: d.Resumable,
		Mime: d.Mime, URL: d.URL, PageURL: d.PageURL, UserAgent: d.UserAgent,
		Post: post, Headers: headers,
	}, "", "  ")
}
