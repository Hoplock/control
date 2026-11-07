// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package console_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/console"
	"github.com/hoplock/control/internal/httpapi/north"
)

func serve(t *testing.T, c *console.Console, method, path string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	return rec
}

func mustNew(t *testing.T, o console.Options) *console.Console {
	t.Helper()
	c, err := console.New(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The console's prefix is the namespace M27 reserved for it.
func TestTheConsoleIsServedUnderTheNamespaceReservedForIt(t *testing.T) {
	if console.Prefix != north.ConsolePrefix {
		t.Fatalf("console.Prefix is %q, north.ConsolePrefix %q", console.Prefix, north.ConsolePrefix)
	}
	for _, p := range mustNew(t, console.Options{}).Patterns() {
		if !strings.HasPrefix(p, north.ConsolePrefix) {
			t.Errorf("pattern %q is outside %s", p, north.ConsolePrefix)
		}
	}
}

// The catalogue the console mounts is the source's en.json, key for key, so
// that a host's keys are judged against what the console really renders.
func TestTheCatalogueIsTheSourcesEnJSON(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "ui", "src", "locales", "en.json"))
	if err != nil {
		t.Fatal(err)
	}
	var messages map[string]string
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	var want []string
	for k := range messages {
		want = append(want, k)
	}
	slices.Sort(want)
	if got := mustNew(t, console.Options{}).Catalogue(); !slices.Equal(got, want) {
		t.Fatalf("the embedded catalogue has %d keys and the source %d: rebuild ui/ (npm run build)", len(got), len(want))
	}
}

// Every deep link inside the prefix is the shell, which routes it; a missing
// asset is a 404, never the shell; and nothing but GET and HEAD is served.
func TestTheShellAnswersEveryDeepLinkAndNoMissingAsset(t *testing.T) {
	c := mustNew(t, console.Options{})
	for _, path := range []string{"/console/", "/console/deployment", "/console/audit/records?subject=alice", "/console/sign-in/callback/corp?code=x&state=y"} {
		rec := serve(t, c, "GET", path, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<div id="root">`) {
			t.Errorf("%s answered %d, want the shell", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s is %q", path, ct)
		}
	}
	for _, path := range []string{"/console/assets/missing.js", "/console/licenses/missing.txt"} {
		if rec := serve(t, c, "GET", path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d, want 404", path, rec.Code)
		}
	}
	if rec := serve(t, c, "POST", "/console/", nil); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST answered %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
	if rec := serve(t, c, "GET", "/console/locales/en.json", nil); rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("the catalogue answered %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

// The shell loads only from its own origin, under its own prefix, and says
// so to the browser: nothing is fetched from the internet (M20).
func TestTheShellLoadsNothingFromAnotherOrigin(t *testing.T) {
	c := mustNew(t, console.Options{})
	rec := serve(t, c, "GET", "/console/", nil)
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "font-src 'self'", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("the shell's policy %q has no %q", csp, want)
		}
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("the shell is cached as %q; it names the assets, so it must revalidate", rec.Header().Get("Cache-Control"))
	}
	index := rec.Body.String()
	refs := regexp.MustCompile(`(?:src|href)="([^"]*)"`).FindAllStringSubmatch(index, -1)
	if len(refs) < 2 {
		t.Fatalf("the shell references %d files; the test read nothing", len(refs))
	}
	for _, ref := range refs {
		if ref[1] != "data:," && !strings.HasPrefix(ref[1], "/console/assets/") {
			t.Errorf("the shell loads %q, outside /console/assets/", ref[1])
		}
	}
	// Every stylesheet's url() is a bundled file, and none imports another.
	for _, m := range regexp.MustCompile(`/console/(assets/[^"]+\.css)`).FindAllStringSubmatch(index, -1) {
		css := serve(t, c, "GET", "/console/"+m[1], nil).Body.String()
		if strings.Contains(css, "@import") {
			t.Errorf("%s imports a stylesheet", m[1])
		}
		urls := regexp.MustCompile(`url\(([^)]*)\)`).FindAllStringSubmatch(css, -1)
		fonts := 0
		for _, u := range urls {
			v := strings.Trim(u[1], `"'`)
			if !strings.HasPrefix(v, "/console/assets/") {
				t.Errorf("%s loads %q", m[1], v)
			}
			if strings.HasSuffix(v, ".woff2") {
				fonts++
				if rec := serve(t, c, "GET", v, nil); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "font/woff2" {
					t.Errorf("%s answered %d %q: the font is not in the binary", v, rec.Code, rec.Header().Get("Content-Type"))
				}
			}
		}
		if fonts != 2 {
			t.Errorf("%s loads %d bundled fonts, want the UI face and the mono face", m[1], fonts)
		}
	}
}

// An asset is immutable, compressed when the browser accepts it, and
// revalidated by its ETag.
func TestAnAssetIsCachedForeverAndCompressed(t *testing.T) {
	c := mustNew(t, console.Options{})
	index := serve(t, c, "GET", "/console/", nil).Body.String()
	m := regexp.MustCompile(`src="(/console/assets/[^"]+\.js)"`).FindStringSubmatch(index)
	if m == nil {
		t.Fatal("the shell loads no script")
	}
	plain := serve(t, c, "GET", m[1], nil)
	if plain.Code != http.StatusOK || plain.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("%s answered %d, cached as %q", m[1], plain.Code, plain.Header().Get("Cache-Control"))
	}
	zipped := serve(t, c, "GET", m[1], http.Header{"Accept-Encoding": {"br, gzip"}})
	if zipped.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("a browser accepting gzip got %q", zipped.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(bytes.NewReader(zipped.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if !bytes.Equal(body, plain.Body.Bytes()) {
		t.Fatal("the compressed script is not the script")
	}
	if rec := serve(t, c, "GET", m[1], http.Header{"Accept-Encoding": {"gzip;q=0"}}); rec.Header().Get("Content-Encoding") != "" {
		t.Error("gzip;q=0 was answered with gzip")
	}
	if rec := serve(t, c, "GET", m[1], http.Header{"If-None-Match": {plain.Header().Get("ETag")}}); rec.Code != http.StatusNotModified {
		t.Errorf("a revalidation answered %d", rec.Code)
	}
}

// The configured stripe is on the first paint, and each has its tokens in
// the build; a stripe that is not one is refused at start-up.
func TestTheStripeIsTheOperatorsAndEveryOneHasItsTokens(t *testing.T) {
	css := ""
	c := mustNew(t, console.Options{})
	for _, m := range regexp.MustCompile(`href="/console/(assets/[^"]+\.css)"`).FindAllStringSubmatch(serve(t, c, "GET", "/console/", nil).Body.String(), -1) {
		css += serve(t, c, "GET", "/console/"+m[1], nil).Body.String()
	}
	for _, stripe := range config.ConsoleStripes {
		index := serve(t, mustNew(t, console.Options{Stripe: stripe}), "GET", "/console/deployment", nil).Body.String()
		if !strings.Contains(index, `data-stripe="`+stripe+`"`) {
			t.Errorf("stripe %s is not on the shell", stripe)
		}
		if stripe == config.DefaultConsoleStripe {
			continue
		}
		if !strings.Contains(css, `[data-stripe=`+stripe+`]`) && !strings.Contains(css, `[data-stripe="`+stripe+`"]`) {
			t.Errorf("stripe %s has no tokens in the built stylesheet", stripe)
		}
	}
	if !strings.Contains(serve(t, c, "GET", "/console/", nil).Body.String(), `data-stripe="accent"`) {
		t.Error("a deployment that sets no stripe does not get the accent")
	}
	if _, err := console.New(console.Options{Stripe: "red"}); err == nil {
		t.Error("a stripe that is not one was accepted")
	}
}
