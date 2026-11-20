// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
	"github.com/hoplock/control/internal/tlsconf/tlstest"
)

// freeAddr is a loopback address nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func writeDaemonConfig(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A TLS BLOCK THAT CANNOT BE HONOURED REFUSES THE START (0063, M29): before the
// store is opened and before either listener is bound, so nothing ever serves
// plaintext in its place. No database is needed to prove it — the DSN names
// nothing that answers.
func TestATLSBlockThatCannotBeHonouredRefusesTheStart(t *testing.T) {
	m := tlstest.New(t)
	missing := filepath.Join(t.TempDir(), "absent.pem")
	cases := []struct {
		name, section, block, field string
	}{
		{"a certificate file that does not exist", "south",
			"  tls: {certificate_file: " + missing + ", key_file: " + m.KeyFile + "}\n", "south.tls.certificate_file"},
		{"a key file that does not exist", "north",
			"  tls: {certificate_file: " + m.CertFile + ", key_file: " + missing + "}\n", "north.tls.certificate_file"},
		{"a certificate and no key", "north",
			"  tls: {certificate_file: " + m.CertFile + "}\n", "north.tls.key_file"},
		{"a client CA file that does not exist", "south",
			"  tls: {certificate_file: " + m.CertFile + ", key_file: " + m.KeyFile + ", client_ca_file: " + missing + "}\n",
			"south.tls.client_ca_file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			south, north := freeAddr(t), freeAddr(t)
			path := writeDaemonConfig(t, "listeners: {south: \""+south+"\", north: \""+north+"\"}\n"+
				"database: {dsn: \"postgres://hoplock@127.0.0.1:1/nothing?sslmode=disable&connect_timeout=1\"}\n"+
				tc.section+":\n"+tc.block)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			var out, errOut syncBuffer
			err := Main(ctx, []string{"--config", path}, &out, &errOut, Host{})
			if err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("Main = %v; want a refusal naming %s", err, tc.field)
			}
			for _, addr := range []string{south, north} {
				if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
					_ = c.Close()
					t.Fatalf("%s was bound by a start that was refused", addr)
				}
			}
			if strings.Contains(errOut.String(), "listener starting") {
				t.Fatalf("a listener started:\n%s", errOut.String())
			}
		})
	}
}

// running is one daemon started by Main over a migrated schema.
type running struct {
	south, north string
	log          *syncBuffer
}

func startDaemon(t *testing.T, dsn, sections string) running {
	t.Helper()
	r := running{south: freeAddr(t), north: freeAddr(t), log: &syncBuffer{}}
	path := writeDaemonConfig(t, "tenant: wire\nlisteners: {south: \""+r.south+"\", north: \""+r.north+"\"}\n"+
		"database: {dsn: \""+dsn+"\"}\n"+sections)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		var out syncBuffer
		done <- Main(ctx, []string{"--config", path}, &out, r.log, Host{})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Main: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("the daemon did not stop")
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for _, addr := range []string{r.south, r.north} {
		for {
			c, err := net.DialTimeout("tcp", addr, time.Second)
			if err == nil {
				_ = c.Close()
				break
			}
			select {
			case err := <-done:
				t.Fatalf("the daemon stopped before %s listened: %v\n%s", addr, err, r.log.String())
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never listened:\n%s", addr, r.log.String())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return r
}

// request makes one GET over TLS (pool non-nil) or plaintext, with the client
// restricted to curves when they are given, and returns the connection state.
func request(addr string, pool *x509.CertPool, curves []tls.CurveID) (*tls.ConnectionState, error) {
	scheme := "http://"
	transport := &http.Transport{}
	if pool != nil {
		scheme = "https://"
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, CurvePreferences: curves}
	}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Get(scheme + addr + "/")
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 500 {
		return nil, errors.New("server error " + resp.Status)
	}
	return resp.TLS, nil
}

// EACH LISTENER SERVES ITS OWN WIRE (0063, M29), through the daemon's real
// bring-up: TLS where its section names a certificate and plaintext where it
// does not, the hybrid negotiated and read from the connection state, and
// require_hybrid_kex observed refusing a classical-only client on the one
// listener that sets it and not on the other. The start-up log states each
// listener's posture on its own line.
func TestEachListenerServesItsOwnWire(t *testing.T) {
	dsn := storetest.DSN(t)
	dsn = storetest.DSNForSchema(dsn, storetest.NewSchema(t, dsn))
	st, err := store.Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	m := tlstest.New(t)
	tlsBlock := func(require bool) string {
		b := "  tls:\n    certificate_file: " + m.CertFile + "\n    key_file: " + m.KeyFile + "\n"
		if require {
			b += "    require_hybrid_kex: true\n"
		}
		return b
	}
	classical := []tls.CurveID{tls.X25519}

	t.Run("south TLS requiring the hybrid, north plaintext", func(t *testing.T) {
		d := startDaemon(t, dsn, "south:\n"+tlsBlock(true))
		cs, err := request(d.south, m.Pool, nil)
		if err != nil || cs == nil || cs.CurveID != tls.X25519MLKEM768 {
			t.Fatalf("south: %v, %v; want X25519MLKEM768", cs, err)
		}
		if _, err := request(d.south, m.Pool, classical); err == nil {
			t.Fatal("south: require_hybrid_kex accepted a classical-only client")
		}
		if cs, err := request(d.north, nil, nil); err != nil || cs != nil {
			t.Fatalf("north: %v, %v; want plaintext", cs, err)
		}
		if _, err := request(d.north, m.Pool, nil); err == nil {
			t.Fatal("north answered a TLS handshake with no tls block")
		}
		wantPosture(t, d.log.String(), "south-bound listener starting", `"tls":"on"`, `"post_quantum_kex":"required"`)
		wantPosture(t, d.log.String(), "north-bound listener starting", `"tls":"off"`, `"post_quantum_kex":"not-stated-plaintext-listener"`)
	})

	t.Run("north TLS preferring the hybrid, south plaintext", func(t *testing.T) {
		d := startDaemon(t, dsn, "north:\n"+tlsBlock(false))
		cs, err := request(d.north, m.Pool, nil)
		if err != nil || cs == nil || cs.CurveID != tls.X25519MLKEM768 {
			t.Fatalf("north: %v, %v; want X25519MLKEM768", cs, err)
		}
		cs, err = request(d.north, m.Pool, classical)
		if err != nil || cs == nil || cs.CurveID != tls.X25519 {
			t.Fatalf("north: a classical-only client without require_hybrid_kex: %v, %v", cs, err)
		}
		if cs, err := request(d.south, nil, nil); err != nil || cs != nil {
			t.Fatalf("south: %v, %v; want plaintext", cs, err)
		}
		wantPosture(t, d.log.String(), "north-bound listener starting", `"tls":"on"`, `"post_quantum_kex":"preferred"`)
		wantPosture(t, d.log.String(), "south-bound listener starting", `"tls":"off"`)
	})
}

// wantPosture finds the log line msg and wants every attribute on it.
func wantPosture(t *testing.T, log, msg string, attrs ...string) {
	t.Helper()
	for _, line := range strings.Split(log, "\n") {
		if !strings.Contains(line, `"msg":"`+msg+`"`) {
			continue
		}
		for _, a := range attrs {
			if !strings.Contains(line, a) {
				t.Fatalf("%q does not state %s:\n%s", msg, a, line)
			}
		}
		return
	}
	t.Fatalf("no %q line in the log:\n%s", msg, log)
}
