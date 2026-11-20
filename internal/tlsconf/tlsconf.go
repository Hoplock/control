// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package tlsconf

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"slices"

	"github.com/hoplock/control/internal/config"
)

// MinVersion is the oldest TLS version a listener accepts.
//
// TLS 1.2 buys nothing here: a south-bound caller is a Hoplock Proxy built
// with a current Go, and a north-bound one is a modern browser or an
// operator's tooling. 1.3 is also the only version the hybrid post-quantum
// groups exist in, so a 1.2 handshake could never meet RequireHybridKex.
const MinVersion = tls.VersionTLS13

// hybridGroups are the key exchanges this package counts as hybrid
// post-quantum: a classical exchange and ML-KEM together, so the session key
// holds if either does. A slice rather than a map, so the exhaustive linter
// reads it as the list it is — every other CurveID is classical by omission,
// not by an entry somebody forgot to add.
//
// A group the standard library adds later is classical here until it is added
// to this list, which errs towards refusing under RequireHybridKex rather than
// towards accepting something nobody has looked at.
var hybridGroups = []tls.CurveID{
	tls.X25519MLKEM768,
	tls.SecP256r1MLKEM768,
	tls.SecP384r1MLKEM1024,
}

// HybridGroups returns the key exchanges RequireHybridKex accepts.
func HybridGroups() []tls.CurveID { return slices.Clone(hybridGroups) }

// IsHybrid reports whether id is a hybrid post-quantum key exchange.
func IsHybrid(id tls.CurveID) bool { return slices.Contains(hybridGroups, id) }

// KexError is the handshake refusal RequireHybridKex produces: the group the
// handshake negotiated, and the listener that would not accept it.
type KexError struct {
	Section    string
	Negotiated tls.CurveID
}

func (e *KexError) Error() string {
	return fmt.Sprintf("tlsconf: the handshake negotiated %s, which is not a hybrid post-quantum key exchange, and %s.tls.require_hybrid_kex is set",
		e.Negotiated, e.Section)
}

// Build returns the configuration the listener of section (`south` or `north`)
// serves TLS with, or nil when c names no certificate: plaintext, exactly as
// before the block existed.
//
// Every file is read here, once, so that a path that is wrong is a start-up
// error rather than a handshake error on the first connection. A certificate
// rotation is a restart.
func Build(section string, c config.TLSConfig) (*tls.Config, error) {
	// The loader refuses an incomplete block too; Build refuses it again
	// because it is the last thing between a wrong setting and a listener,
	// and a caller may hand it a block that never went through the loader.
	if err := c.Validate(section); err != nil {
		return nil, err
	}
	if !c.Enabled() {
		return nil, nil
	}
	prefix := section + ".tls."
	cert, err := tls.LoadX509KeyPair(c.CertificateFile, c.KeyFile)
	if err != nil {
		return nil, &config.FieldError{Field: prefix + "certificate_file",
			Msg: fmt.Sprintf("cannot be served with %s: %v", prefix+"key_file", err)}
	}

	cfg := &tls.Config{
		MinVersion:   MinVersion,
		Certificates: []tls.Certificate{cert},
		// CurvePreferences IS LEFT NIL ON PURPOSE. The standard library's
		// own set applies, and from Go 1.24 it puts X25519MLKEM768 first
		// (from 1.26, the NIST-curve hybrids too). A list typed here would
		// freeze the posture at whatever was current when somebody typed
		// it; nil means a Go upgrade improves it, with nothing to change.
		CurvePreferences: nil,
	}

	if c.ClientCAFile != "" {
		pem, err := os.ReadFile(c.ClientCAFile)
		if err != nil {
			return nil, &config.FieldError{Field: prefix + "client_ca_file", Msg: err.Error()}
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, &config.FieldError{Field: prefix + "client_ca_file", Msg: "holds no PEM certificate"}
		}
		// Naming client CAs REQUIRES a client certificate: a listener
		// that verified one only if given would be a listener anyone can
		// reach without one. It authenticates the transport and nothing
		// else — the caller's credential is still the bearer token, and
		// no identity is read from the certificate (M2, M22).
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	if c.RequireHybridKex {
		// VerifyConnection runs on every handshake, resumptions included,
		// after the group is chosen. A resumption without a fresh key
		// exchange negotiates no group at all, and is refused with the
		// rest: it is not a hybrid exchange either.
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if !IsHybrid(cs.CurveID) {
				return &KexError{Section: section, Negotiated: cs.CurveID}
			}
			return nil
		}
	}
	return cfg, nil
}

// Posture is what one listener states about its wire at start-up: the answer
// to "is this deployment post-quantum on the wire?" for that listener.
type Posture struct {
	// TLS is whether this server terminates TLS on the listener.
	TLS bool
	// RequireHybridKex is whether a classical-only handshake is refused.
	RequireHybridKex bool
	// ClientCertificate is whether a client certificate is required.
	ClientCertificate bool
}

// PostureOf states the posture c configures. No certificate is plaintext.
func PostureOf(c config.TLSConfig) Posture {
	if !c.Enabled() {
		return Posture{}
	}
	return Posture{TLS: true, RequireHybridKex: c.RequireHybridKex, ClientCertificate: c.ClientCAFile != ""}
}

// The values of the `post_quantum_kex` attribute.
const (
	// KexRequired: a handshake that is not a hybrid group is refused.
	KexRequired = "required"
	// KexPreferred: a hybrid group is offered first, and a classical-only
	// client still connects.
	KexPreferred = "preferred"
	// KexNotStated: the listener is plaintext, so whatever terminates TLS
	// in front of it decides, and this server cannot say what it decided.
	KexNotStated = "not-stated-plaintext-listener"
)

// PostQuantumKex is the posture's key-exchange answer, one of the Kex values.
func (p Posture) PostQuantumKex() string {
	switch {
	case !p.TLS:
		return KexNotStated
	case p.RequireHybridKex:
		return KexRequired
	default:
		return KexPreferred
	}
}

// LogAttrs are the posture as the start-up log line's attributes.
func (p Posture) LogAttrs() []any {
	if !p.TLS {
		return []any{slog.String("tls", "off"), slog.String("post_quantum_kex", p.PostQuantumKex())}
	}
	return []any{
		slog.String("tls", "on"),
		slog.String("min_tls_version", "1.3"),
		slog.String("post_quantum_kex", p.PostQuantumKex()),
		slog.Bool("client_certificate_required", p.ClientCertificate),
	}
}
