package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// rawGet fetches path without Go's transparent gzip handling, so the test sees
// exactly what a browser gets.
func rawGet(t *testing.T, h *harness, path string, header map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := h.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, body
}

func TestStaticCachingAndGzip(t *testing.T) {
	big := []byte(strings.Repeat("export const line = 'estate';\n", 200))
	assets := testAssets()
	assets["web/shared/big.js"] = &fstest.MapFile{Data: big}
	assets["web/sw.js"] = &fstest.MapFile{Data: []byte("// worker")}
	assets["web/phone/app.webmanifest"] = &fstest.MapFile{Data: []byte(`{"name":"Estate"}`)}
	h := newHarness(t, func(c *Config) { c.Assets = assets })

	plain, body := rawGet(t, h, "/shared/big.js", nil)
	etag := plain.Header.Get("ETag")
	if plain.StatusCode != 200 || !bytes.Equal(body, big) || plain.Header.Get("Content-Encoding") != "" {
		t.Fatalf("plain: status %d encoding %q, %d bytes", plain.StatusCode, plain.Header.Get("Content-Encoding"), len(body))
	}
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) || strings.HasSuffix(etag, `-gz"`) {
		t.Fatalf("plain ETag %q", etag)
	}
	if cc := plain.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("static Cache-Control %q, want no-cache (keep a copy, always revalidate)", cc)
	}
	if plain.Header.Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary %q", plain.Header.Get("Vary"))
	}

	zipped, zbody := rawGet(t, h, "/shared/big.js", map[string]string{"Accept-Encoding": "br, gzip"})
	if zipped.StatusCode != 200 || zipped.Header.Get("Content-Encoding") != "gzip" || len(zbody) >= len(big)/2 {
		t.Fatalf("gzip: status %d encoding %q, %d of %d bytes", zipped.StatusCode, zipped.Header.Get("Content-Encoding"), len(zbody), len(big))
	}
	if got := zipped.Header.Get("ETag"); got != strings.TrimSuffix(etag, `"`)+`-gz"` {
		t.Errorf("gzip ETag %q for plain %q", got, etag)
	}
	zr, err := gzip.NewReader(bytes.NewReader(zbody))
	if err != nil {
		t.Fatal(err)
	}
	unzipped, _ := io.ReadAll(zr)
	if !bytes.Equal(unzipped, big) {
		t.Fatal("gzip body does not unzip to the file")
	}

	for _, tag := range []string{etag, zipped.Header.Get("ETag"), "W/" + etag, `"other", ` + etag} {
		res, body := rawGet(t, h, "/shared/big.js", map[string]string{"If-None-Match": tag, "Accept-Encoding": "gzip"})
		if res.StatusCode != http.StatusNotModified || len(body) != 0 || res.Header.Get("Content-Encoding") != "" {
			t.Errorf("If-None-Match %s: status %d, %d bytes, encoding %q", tag, res.StatusCode, len(body), res.Header.Get("Content-Encoding"))
		}
	}
	if res, _ := rawGet(t, h, "/shared/big.js", map[string]string{"If-None-Match": `"stale"`}); res.StatusCode != 200 {
		t.Errorf("stale ETag: status %d", res.StatusCode)
	}

	// Small files are not worth zipping; they still get a validator.
	small, _ := rawGet(t, h, "/shared/client.js", map[string]string{"Accept-Encoding": "gzip"})
	if small.Header.Get("Content-Encoding") != "" || small.Header.Get("ETag") == "" || small.Header.Get("Vary") != "" {
		t.Errorf("small file: encoding %q ETag %q Vary %q", small.Header.Get("Content-Encoding"), small.Header.Get("ETag"), small.Header.Get("Vary"))
	}

	// The installed app: the worker sits at the root, the manifest has its own type.
	sw, swBody := rawGet(t, h, "/sw.js", nil)
	if sw.StatusCode != 200 || string(swBody) != "// worker" || sw.Header.Get("Content-Type") != "text/javascript; charset=utf-8" || sw.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("/sw.js: status %d type %q cache %q body %q", sw.StatusCode, sw.Header.Get("Content-Type"), sw.Header.Get("Cache-Control"), swBody)
	}
	manifest, _ := rawGet(t, h, "/phone/app.webmanifest", nil)
	if manifest.StatusCode != 200 || manifest.Header.Get("Content-Type") != "application/manifest+json" {
		t.Errorf("manifest: status %d type %q", manifest.StatusCode, manifest.Header.Get("Content-Type"))
	}

	// Unknown paths and the API are never cached.
	if missing, _ := rawGet(t, h, "/nothing", nil); missing.StatusCode != 404 || missing.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("404: status %d cache %q", missing.StatusCode, missing.Header.Get("Cache-Control"))
	}
	if api, _ := rawGet(t, h, "/api/server", nil); api.Header.Get("Cache-Control") != "no-store" || api.Header.Get("ETag") != "" {
		t.Errorf("api: cache %q ETag %q", api.Header.Get("Cache-Control"), api.Header.Get("ETag"))
	}
}

func TestAcceptsGzip(t *testing.T) {
	for header, want := range map[string]bool{
		"":                      false,
		"gzip":                  true,
		"GZIP":                  true,
		"gzip, deflate, br":     true,
		"br;q=1.0, gzip;q=0.8":  true,
		"deflate, gzip;q=0":     false,
		"gzip; q=0.000":         false,
		"identity":              false,
		"x-gzip":                false,
		"deflate,   gzip ; q=1": true,
	} {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}
