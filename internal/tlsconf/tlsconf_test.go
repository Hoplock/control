// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package tlsconf_test

import (
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/tlsconf"
	"github.com/hoplock/control/internal/tlsconf/tlstest"
)

// The two listeners this package builds for. Every handshake assertion runs
// once per section, because M2 makes them two listeners with nothing shared
// but this constructor.
var sections = []string{"south", "north"}

func TestNoBlockIsPlaintext(t *testing.T) {
	cfg, err := tlsconf.Build("south", config.TLSConfig{})
	if err != nil || cfg != nil {
		t.Fatalf("Build(no certificate) = %v, %v; want nil, nil: no certificate serves plaintext, exactly as before", cfg, err)
	}
	if p := tlsconf.PostureOf(config.TLSConfig{}); p.TLS || p.PostQuantumKex() != tlsconf.KexNotStated {
		t.Fatalf("PostureOf(no certificate) = %+v (%s)", p, p.PostQuantumKex())
	}
}

// A BLOCK THAT CANNOT BE HONOURED IS REFUSED, never served as plaintext.
func TestAnIncompleteBlockIsRefused(t *testing.T) {
	m := tlstest.New(t)
	missing := filepath.Join(t.TempDir(), "absent.pem")
	garbage := filepath.Join(t.TempDir(), "garbage.pem")
	if err := os.WriteFile(garbage, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		c     config.TLSConfig
		field string
	}{
		{"a certificate and no key", config.TLSConfig{CertificateFile: m.CertFile}, "south.tls.key_file"},
		{"a key and no certificate", config.TLSConfig{KeyFile: m.KeyFile}, "south.tls.certificate_file"},
		{"require_hybrid_kex on a plaintext listener", config.TLSConfig{RequireHybridKex: true}, "south.tls.require_hybrid_kex"},
		{"a client CA on a plaintext listener", config.TLSConfig{ClientCAFile: m.CAFile}, "south.tls.client_ca_file"},
		{"a certificate file that does not exist", config.TLSConfig{CertificateFile: missing, KeyFile: m.KeyFile}, "south.tls.certificate_file"},
		{"a key file that does not exist", config.TLSConfig{CertificateFile: m.CertFile, KeyFile: missing}, "south.tls.certificate_file"},
		{"a client CA file that does not exist", config.TLSConfig{CertificateFile: m.CertFile, KeyFile: m.KeyFile, ClientCAFile: missing}, "south.tls.client_ca_file"},
		{"a client CA file with no certificate", config.TLSConfig{CertificateFile: m.CertFile, KeyFile: m.KeyFile, ClientCAFile: garbage}, "south.tls.client_ca_file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := tlsconf.Build("south", tc.c)
			if cfg != nil {
				t.Fatalf("Build built a config: %+v", cfg)
			}
			var fe *config.FieldError
			if !errors.As(err, &fe) || fe.Field != tc.field {
				t.Fatalf("Build = %v; want a field error on %s", err, tc.field)
			}
		})
	}
}

// THE DEFAULTS THIS REPOSITORY COMMITS TO: TLS 1.3, and the standard library's
// own key-exchange set rather than a list somebody typed.
func TestTheDefaults(t *testing.T) {
	m := tlstest.New(t)
	cfg, err := tlsconf.Build("north", config.TLSConfig{CertificateFile: m.CertFile, KeyFile: m.KeyFile})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = %#x, want TLS 1.3", cfg.MinVersion)
	}
	if cfg.CurvePreferences != nil {
		t.Errorf("CurvePreferences = %v, want nil: a typed list freezes the posture at the day it was typed", cfg.CurvePreferences)
	}
	if cfg.VerifyConnection != nil {
		t.Error("VerifyConnection is set without require_hybrid_kex")
	}
	if cfg.ClientAuth != tls.NoClientCert || cfg.ClientCAs != nil {
		t.Errorf("client authentication without client_ca_file: %v", cfg.ClientAuth)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("Certificates: %d", len(cfg.Certificates))
	}
}

func TestHybridGroups(t *testing.T) {
	for _, id := range []tls.CurveID{tls.X25519MLKEM768, tls.SecP256r1MLKEM768, tls.SecP384r1MLKEM1024} {
		if !tlsconf.IsHybrid(id) {
			t.Errorf("%s is not counted as hybrid", id)
		}
	}
	for _, id := range []tls.CurveID{0, tls.X25519, tls.CurveP256, tls.CurveP384, tls.CurveP521} {
		if tlsconf.IsHybrid(id) {
			t.Errorf("%s is counted as hybrid", id)
		}
	}
}

// serve starts an HTTPS server with the built config and returns it.
func serve(t *testing.T, cfg *tls.Config) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	srv.TLS = cfg
	// The server's own handshake errors are the test's to read, not noise
	// on stderr.
	srv.Config.ErrorLog = discardLog()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// get makes one request with a client restricted to curves (nil: Go's
// default set), and returns the negotiated group.
func get(m *tlstest.Material, srv *httptest.Server, curves []tls.CurveID, cert *tls.Certificate) (tls.CurveID, error) {
	clientCfg := &tls.Config{RootCAs: m.Pool, ServerName: "localhost", CurvePreferences: curves}
	if cert != nil {
		clientCfg.Certificates = []tls.Certificate{*cert}
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientCfg}}
	defer client.CloseIdleConnections()
	resp, err := client.Get(srv.URL)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if string(body) != "ok" {
		return 0, errors.New("unexpected body " + string(body))
	}
	return resp.TLS.CurveID, nil
}

// THE CLAIM MADE REAL: a Go client against each listener negotiates
// X25519MLKEM768, read from the connection state rather than inferred from
// the absence of an error.
func TestEachListenerNegotiatesTheHybrid(t *testing.T) {
	m := tlstest.New(t)
	for _, section := range sections {
		t.Run(section, func(t *testing.T) {
			cfg, err := tlsconf.Build(section, config.TLSConfig{CertificateFile: m.CertFile, KeyFile: m.KeyFile})
			if err != nil {
				t.Fatal(err)
			}
			srv := serve(t, cfg)
			got, err := get(m, srv, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got != tls.X25519MLKEM768 {
				t.Fatalf("negotiated %s, want X25519MLKEM768", got)
			}
		})
	}
}

// BOTH DIRECTIONS of require_hybrid_kex, because a knob that cannot be
// observed to do anything is a knob nobody should trust.
func TestRequireHybridKex(t *testing.T) {
	m := tlstest.New(t)
	classical := []tls.CurveID{tls.X25519}
	for _, section := range sections {
		t.Run(section, func(t *testing.T) {
			lenient, err := tlsconf.Build(section, config.TLSConfig{CertificateFile: m.CertFile, KeyFile: m.KeyFile})
			if err != nil {
				t.Fatal(err)
			}
			got, err := get(m, serve(t, lenient), classical, nil)
			if err != nil {
				t.Fatalf("require_hybrid_kex false refused a classical-only client: %v", err)
			}
			if got != tls.X25519 {
				t.Fatalf("a classical-only client negotiated %s", got)
			}

			strict, err := tlsconf.Build(section, config.TLSConfig{CertificateFile: m.CertFile, KeyFile: m.KeyFile, RequireHybridKex: true})
			if err != nil {
				t.Fatal(err)
			}
			srv := serve(t, strict)
			if got, err := get(m, srv, classical, nil); err == nil {
				t.Fatalf("require_hybrid_kex true accepted a classical-only client (negotiated %s)", got)
			}
			// The hybrid still gets through the strict listener.
			got, err = get(m, srv, nil, nil)
			if err != nil || got != tls.X25519MLKEM768 {
				t.Fatalf("require_hybrid_kex true and a default client: %s, %v", got, err)
			}
		})
	}
}

// The refusal is the listener's, and it says why: the group negotiated and the
// setting that refused it.
func TestTheRefusalNamesTheGroupAndTheSetting(t *testing.T) {
	m := tlstest.New(t)
	cfg, err := tlsconf.Build("north", config.TLSConfig{CertificateFile: m.CertFile, KeyFile: m.KeyFile, RequireHybridKex: true})
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.VerifyConnection(tls.ConnectionState{CurveID: tls.X25519})
	var kex *tlsconf.KexError
	if !errors.As(err, &kex) || kex.Negotiated != tls.X25519 || kex.Section != "north" {
		t.Fatalf("VerifyConnection(X25519) = %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "X25519") || !strings.Contains(msg, "north.tls.require_hybrid_kex") {
		t.Fatalf("the refusal does not say why: %s", msg)
	}
	// A handshake with no key exchange at all is not a hybrid one either.
	if err := cfg.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Fatal("a handshake that negotiated no group was accepted")
	}
	if err := cfg.VerifyConnection(tls.ConnectionState{CurveID: tls.X25519MLKEM768}); err != nil {
		t.Fatalf("VerifyConnection(X25519MLKEM768) = %v", err)
	}
}

// client_ca_file requires a client certificate chaining to it, and still
// negotiates the hybrid.
func TestClientCAFileRequiresACertificate(t *testing.T) {
	m := tlstest.New(t)
	cfg, err := tlsconf.Build("south", config.TLSConfig{CertificateFile: m.CertFile, KeyFile: m.KeyFile, ClientCAFile: m.CAFile})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth = %v", cfg.ClientAuth)
	}
	srv := serve(t, cfg)
	if _, err := get(m, srv, nil, nil); err == nil {
		t.Fatal("a client with no certificate was accepted")
	}
	client := m.ClientCertificate(t)
	got, err := get(m, srv, nil, &client)
	if err != nil || got != tls.X25519MLKEM768 {
		t.Fatalf("a client with a certificate: %s, %v", got, err)
	}
}

func TestPostureStatesTheWire(t *testing.T) {
	cases := []struct {
		c    config.TLSConfig
		want string
		tls  string
	}{
		{config.TLSConfig{}, tlsconf.KexNotStated, "off"},
		{config.TLSConfig{CertificateFile: "c", KeyFile: "k"}, tlsconf.KexPreferred, "on"},
		{config.TLSConfig{CertificateFile: "c", KeyFile: "k", RequireHybridKex: true}, tlsconf.KexRequired, "on"},
	}
	for _, tc := range cases {
		p := tlsconf.PostureOf(tc.c)
		if p.PostQuantumKex() != tc.want {
			t.Errorf("%+v: post_quantum_kex %q, want %q", tc.c, p.PostQuantumKex(), tc.want)
		}
		attrs := p.LogAttrs()
		if !strings.Contains(fmtAttrs(attrs), "tls="+tc.tls) || !strings.Contains(fmtAttrs(attrs), "post_quantum_kex="+tc.want) {
			t.Errorf("%+v: log attrs %s", tc.c, fmtAttrs(attrs))
		}
	}
}
