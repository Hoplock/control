// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package console

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hoplock/control/internal/config"
)

// The build, as ui/ writes it. "all:" so that nothing in it is skipped for
// its name.
//
//go:embed all:dist
var dist embed.FS

// Prefix is where the console is served: north.ConsolePrefix, which this
// package cannot import (north's own tests mount this console), and which a
// test holds equal.
const Prefix = "/console/"

// The parts of the build the console's patterns name.
const (
	indexFile     = "index.html"
	assetsDir     = "assets/"
	cataloguePath = "locales/en.json"
	licensesDir   = "licenses/"
)

// stripePlaceholder is the attribute index.html is built with; New replaces
// it with the configured stripe, so the first paint already carries it.
const stripePlaceholder = `data-stripe="accent"`

// contentSecurityPolicy keeps the console to its own origin: it loads no
// script, style, font or image from anywhere else and talks to nothing but
// this listener, so an air-gapped deployment renders as any other and the
// console reports its operators' activity to no third party (M20). Inline
// script and style are refused as well: the build has none, and the string
// lint refuses an inline style in a component.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; " +
	"img-src 'self' data:; connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'self'; " +
	"frame-ancestors 'none'"

// Options configure the console.
type Options struct {
	// Stripe is one of config.ConsoleStripes; empty is the default.
	Stripe string
}

// Console is the built console, ready to serve.
type Console struct {
	files     map[string]*file
	index     *file
	catalogue []string
}

type file struct {
	name    string
	body    []byte
	gzipped []byte
	etag    string
	ctype   string
	cache   string
}

// New reads the embedded build. It refuses a build that is not one — no
// shell, no catalogue — because a binary that starts and serves a blank page
// is a defect found by the first operator rather than by CI.
func New(o Options) (*Console, error) {
	return newFrom(dist, o)
}

func newFrom(build embed.FS, o Options) (*Console, error) {
	stripe := o.Stripe
	if stripe == "" {
		stripe = config.DefaultConsoleStripe
	}
	if !slices.Contains(config.ConsoleStripes, stripe) {
		return nil, fmt.Errorf("console: stripe %q is not one of %s", stripe, strings.Join(config.ConsoleStripes, ", "))
	}
	root, err := fs.Sub(build, "dist")
	if err != nil {
		return nil, err
	}
	c := &Console{files: map[string]*file{}}
	err = fs.WalkDir(root, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		c.files[name] = newFile(name, body)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("console: reading the build: %w", err)
	}
	index, ok := c.files[indexFile]
	if !ok {
		return nil, fmt.Errorf("console: the build has no %s; run `npm run build` in ui/", indexFile)
	}
	if !bytes.Contains(index.body, []byte(stripePlaceholder)) {
		return nil, fmt.Errorf("console: %s does not carry %s for the stripe", indexFile, stripePlaceholder)
	}
	c.index = newFile(indexFile, bytes.Replace(index.body, []byte(stripePlaceholder), []byte(`data-stripe="`+stripe+`"`), 1))
	cat, ok := c.files[cataloguePath]
	if !ok {
		return nil, fmt.Errorf("console: the build has no %s", cataloguePath)
	}
	var messages map[string]string
	if err := json.Unmarshal(cat.body, &messages); err != nil {
		return nil, fmt.Errorf("console: %s: %w", cataloguePath, err)
	}
	for k := range messages {
		c.catalogue = append(c.catalogue, k)
	}
	slices.Sort(c.catalogue)
	return c, nil
}

func newFile(name string, body []byte) *file {
	sum := sha256.Sum256(body)
	f := &file{
		name:  name,
		body:  body,
		etag:  `"` + hex.EncodeToString(sum[:16]) + `"`,
		ctype: mime.TypeByExtension(path.Ext(name)),
		cache: "no-cache",
	}
	switch {
	case path.Ext(name) == ".woff2":
		f.ctype = "font/woff2"
	case f.ctype == "":
		f.ctype = "application/octet-stream"
	}
	if strings.HasPrefix(name, assetsDir) {
		// Every asset's name carries its content hash, so it never changes.
		f.cache = "public, max-age=31536000, immutable"
	}
	if compressible(f.ctype) {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(body)
		_ = zw.Close()
		if buf.Len() < len(body) {
			f.gzipped = buf.Bytes()
		}
	}
	return f
}

func compressible(ctype string) bool {
	for _, p := range []string{"text/", "application/javascript", "text/javascript", "application/json", "image/svg+xml"} {
		if strings.HasPrefix(ctype, p) {
			return true
		}
	}
	return false
}

// Patterns are the console's mounts, as north.Console takes them: the shell,
// its assets, its catalogue, the font licences, and the history fallback,
// which answers any other path inside the prefix with the shell.
func (c *Console) Patterns() []string {
	return []string{
		Prefix + "{$}",
		Prefix + assetsDir,
		Prefix + cataloguePath,
		Prefix + licensesDir,
		Prefix + "{path...}",
	}
}

// Catalogue is every key of the console's catalogue (M21), from the build's
// own en.json, so that a host's catalogue is refused against the real keys.
func (c *Console) Catalogue() []string { return slices.Clone(c.catalogue) }

// ServeHTTP serves the console. A path that names a file of the build is
// that file; a path under assets/ or licenses/ that names none is a 404,
// because a missing script answered with the shell fails as a puzzle rather
// than as a 404; and every other path inside the prefix is the shell, which
// routes it.
func (c *Console) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name, inside := strings.CutPrefix(r.URL.Path, Prefix)
	if !inside {
		http.NotFound(w, r)
		return
	}
	f := c.files[name]
	switch {
	case name == indexFile || name == "":
		f = c.index
	case f != nil:
	case strings.HasPrefix(name, assetsDir), strings.HasPrefix(name, licensesDir), name == cataloguePath:
		http.NotFound(w, r)
		return
	default:
		f = c.index
	}
	h := w.Header()
	h.Set("Content-Type", f.ctype)
	h.Set("Cache-Control", f.cache)
	h.Set("ETag", f.etag)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	if f == c.index {
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY")
	}
	body := f.body
	if f.gzipped != nil {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			h.Set("Content-Encoding", "gzip")
			body = f.gzipped
		}
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}

// acceptsGzip reports whether the request accepts gzip at a quality above 0.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		q, found := strings.CutPrefix(strings.TrimSpace(params), "q=")
		if !found {
			return true
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(q), 64)
		return err == nil && v > 0
	}
	return false
}
