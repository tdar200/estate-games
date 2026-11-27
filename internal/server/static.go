package server

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

var mimeTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".json":        "application/json; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".webp":        "image/webp",
	".gif":         "image/gif",
	".ico":         "image/x-icon",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".mp3":         "audio/mpeg",
	".ogg":         "audio/ogg",
	".wav":         "audio/wav",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
}

func contentType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := mimeTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

// staticFiles serves the embedded apps. The classic client (public/) uses
// root-absolute URLs (/styles.css, /icons.svg#…, history "/"); those are
// rewritten to /classic/… once at startup so it runs unchanged under that
// prefix while its /api/ calls stay as they are.
//
// Files are sent with an ETag and "Cache-Control: no-cache": a browser (or the
// installed app) keeps its copy but asks every time, and an unchanged file costs
// a 304 instead of the whole file, so a deploy is picked up on the next load.
// Text files are gzipped once, the first time they are asked for.
type staticFiles struct {
	assets  fs.FS
	classic map[string][]byte // rewritten text files of public/

	mu      sync.Mutex
	entries map[string]*staticEntry // by asset name, filled on first request
}

// staticEntry is one embedded file ready to send. The assets are embedded in
// the binary, so an entry never goes stale.
type staticEntry struct {
	data []byte
	gz   []byte // gzipped data, nil when not worth it
	etag string // quoted strong validator of data
}

// compressible lists the types worth gzipping (fonts and images are already compressed).
var compressible = map[string]bool{
	".html": true, ".css": true, ".js": true, ".mjs": true, ".json": true, ".svg": true,
	".webmanifest": true, ".txt": true, ".ico": true,
}

func newStaticFiles(assets fs.FS) (*staticFiles, error) {
	st := &staticFiles{assets: assets, classic: map[string][]byte{}, entries: map[string]*staticEntry{}}
	entries, err := fs.ReadDir(assets, "public")
	if err != nil {
		return st, nil // no classic client embedded
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	for _, name := range names {
		ext := path.Ext(name)
		if ext != ".html" && ext != ".js" && ext != ".css" {
			continue
		}
		data, err := fs.ReadFile(assets, "public/"+name)
		if err != nil {
			return nil, err
		}
		st.classic[name] = rewriteClassic(data, names)
	}
	return st, nil
}

func rewriteClassic(data []byte, names []string) []byte {
	for _, quote := range []string{`"`, `'`, "`", "("} {
		for _, name := range names {
			data = bytes.ReplaceAll(data, []byte(quote+"/"+name), []byte(quote+"/classic/"+name))
		}
	}
	// href="/" (home link) and history.replaceState(null, "", "/").
	data = bytes.ReplaceAll(data, []byte(`"/"`), []byte(`"/classic/"`))
	return data
}

func (st *staticFiles) exists(name string) bool {
	info, err := fs.Stat(st.assets, name)
	return err == nil && !info.IsDir()
}

// entry returns the ready-to-send form of an embedded file, or nil.
func (st *staticFiles) entry(name string) *staticEntry {
	if !fs.ValidPath(name) {
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if e := st.entries[name]; e != nil {
		return e
	}
	var data []byte
	if strings.HasPrefix(name, "public/") && st.classic[strings.TrimPrefix(name, "public/")] != nil {
		data = st.classic[strings.TrimPrefix(name, "public/")]
	} else {
		info, err := fs.Stat(st.assets, name)
		if err != nil || info.IsDir() {
			return nil
		}
		data, err = fs.ReadFile(st.assets, name)
		if err != nil {
			return nil
		}
	}
	sum := sha256.Sum256(data)
	e := &staticEntry{data: data, etag: `"` + hex.EncodeToString(sum[:12]) + `"`}
	if compressible[strings.ToLower(path.Ext(name))] && len(data) >= 1024 {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if _, err := zw.Write(data); err == nil && zw.Close() == nil && buf.Len() < len(data)*9/10 {
			e.gz = buf.Bytes()
		}
	}
	if st.entries == nil {
		st.entries = map[string]*staticEntry{}
	}
	st.entries[name] = e
	return e
}

// serveFile writes an embedded file (path relative to the assets root).
func (st *staticFiles) serveFile(w http.ResponseWriter, r *http.Request, name string) bool {
	e := st.entry(name)
	if e == nil {
		return false
	}
	h := w.Header()
	h.Set("Content-Type", contentType(name))
	h.Set("Cache-Control", "no-cache")
	body, etag := e.data, e.etag
	if e.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r.Header.Get("Accept-Encoding")) {
			// A different body needs a different strong validator.
			body, etag = e.gz, strings.TrimSuffix(e.etag, `"`)+`-gz"`
			h.Set("Content-Encoding", "gzip")
		}
	}
	h.Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), e.etag) {
		h.Del("Content-Encoding")
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
	return true
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		q := strings.ReplaceAll(strings.ToLower(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}

// etagMatches reports whether If-None-Match names this file, in its plain or
// gzipped form (weak comparison, as RFC 9110 asks for If-None-Match).
func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	gz := strings.TrimSuffix(etag, `"`) + `-gz"`
	for _, part := range strings.Split(header, ",") {
		tag := strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if tag == "*" || tag == etag || tag == gz {
			return true
		}
	}
	return false
}

// resolve maps a URL path to an embedded file. ok is false for unknown
// paths; classic reports whether the classic CSP applies.
func (st *staticFiles) resolve(urlPath string) (name string, classic, ok bool) {
	clean := path.Clean("/" + urlPath)
	switch {
	case clean == "/":
		return "web/table/index.html", false, true
	case clean == "/play":
		return "web/phone/index.html", false, true
	case clean == "/sw.js":
		// The installed app's service worker; it must sit at the root to cover
		// /play, /j/CODE and the table at /.
		return "web/sw.js", false, true
	case strings.HasPrefix(clean, "/j/") && !strings.Contains(clean[3:], "/"):
		return "web/phone/index.html", false, true
	case clean == "/classic":
		return "public/index.html", true, true
	case strings.HasPrefix(clean, "/classic/"):
		rel := strings.TrimPrefix(clean, "/classic/")
		if rel == "" || !st.exists("public/"+rel) {
			// server.js serves index.html for unknown paths.
			return "public/index.html", true, true
		}
		return "public/" + rel, true, true
	}
	for _, prefix := range []string{"table", "phone", "shared", "fixtures"} {
		if clean == "/"+prefix || strings.HasPrefix(clean, "/"+prefix+"/") {
			rel := strings.TrimPrefix(clean, "/")
			if clean == "/"+prefix || strings.HasSuffix(urlPath, "/") {
				if st.exists("web/" + rel + "/index.html") {
					return "web/" + rel + "/index.html", false, true
				}
			}
			return "web/" + rel, false, true
		}
	}
	return "", false, false
}
