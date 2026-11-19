// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package tlstest writes throwaway TLS material for tests: a CA, a server
// certificate for localhost and 127.0.0.1 signed by it, and client
// certificates on request. Nothing here is a fixture anyone may reuse — every
// call generates fresh keys into the test's own temporary directory.
package tlstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Material is one test's CA and server certificate, on disk and in memory.
type Material struct {
	// CAFile is the CA certificate, PEM; Pool holds the same certificate.
	CAFile string
	Pool   *x509.CertPool
	// CertFile and KeyFile are the server's certificate and key, PEM.
	CertFile, KeyFile string

	ca    *x509.Certificate
	caKey *ecdsa.PrivateKey
}

// New writes a fresh CA and a server certificate for localhost into a
// temporary directory of t's.
func New(t testing.TB) *Material {
	t.Helper()
	dir := t.TempDir()
	caKey := newKey(t)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "tlstest CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	m := &Material{ca: ca, caKey: caKey, Pool: x509.NewCertPool()}
	m.Pool.AddCert(ca)
	m.CAFile = write(t, dir, "ca.pem", "CERTIFICATE", caDER)

	key := newKey(t)
	der := m.sign(t, &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, key)
	m.CertFile = write(t, dir, "server.pem", "CERTIFICATE", der)
	m.KeyFile = write(t, dir, "server-key.pem", "PRIVATE KEY", marshalKey(t, key))
	return m
}

// ClientCertificate is a fresh client certificate signed by the CA.
func (m *Material) ClientCertificate(t testing.TB) tls.Certificate {
	t.Helper()
	key := newKey(t)
	der := m.sign(t, &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "tlstest client"},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}, key)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func (m *Material) sign(t testing.TB, tmpl *x509.Certificate, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(time.Hour)
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	der, err := x509.CreateCertificate(rand.Reader, tmpl, m.ca, &key.PublicKey, m.caKey)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func newKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func marshalKey(t testing.TB, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func write(t testing.TB, dir, name, kind string, der []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
