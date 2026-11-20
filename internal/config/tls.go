// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config

// TLSConfig is one listener's own TLS (0063, M29): `south.tls` or `north.tls`.
//
// It is a block inside each listener's section rather than a shared one,
// because M2 gives the two listeners nothing in common but a process: each
// states its own certificate, its own client CAs and its own key-exchange
// requirement, and a deployment may terminate one here and the other in front.
//
// NO CERTIFICATE MEANS PLAINTEXT, exactly as before this block existed: the
// listener serves HTTP and whatever is in front of it terminates TLS, if
// anything does. A CERTIFICATE MEANS TLS OR NOTHING: a block that names a
// certificate and no key, or a file that cannot be read, refuses to start the
// server — and so does a key-exchange requirement or a client CA on a
// listener with no certificate, which would otherwise be a setting that reads
// as enforced and enforces nothing. A listener that quietly served plaintext
// because a path was wrong is the failure this block exists to prevent.
type TLSConfig struct {
	// CertificateFile is the listener's certificate chain, PEM, leaf first.
	CertificateFile string `yaml:"certificate_file"`
	// KeyFile is the certificate's private key, PEM.
	KeyFile string `yaml:"key_file"`
	// ClientCAFile, when set, requires every client to present a
	// certificate chaining to one of the CAs in it (PEM). It authenticates
	// the TRANSPORT only: a caller still presents its credential, and
	// nothing about who a caller is comes from its certificate (M2, M22).
	ClientCAFile string `yaml:"client_ca_file"`
	// RequireHybridKex refuses a handshake whose key exchange is not a
	// hybrid post-quantum group. Off, the listener still PREFERS one — the
	// standard library offers X25519MLKEM768 first — and a classical-only
	// client still connects.
	RequireHybridKex bool `yaml:"require_hybrid_kex"`
}

// Enabled reports whether this server terminates TLS on the listener: the
// block names a certificate or a key. Validate refuses one without the other.
func (t TLSConfig) Enabled() bool { return t.CertificateFile != "" || t.KeyFile != "" }

// validate reports the first setting in a `tls:` block that cannot be acted
// on. section is the listener's section, `south` or `north`. That the files
// exist and parse is internal/tlsconf's to check, at start-up.
func (t TLSConfig) validate(section string) error {
	prefix := section + ".tls."
	switch {
	case !t.Enabled() && t.RequireHybridKex:
		return &FieldError{Field: prefix + "require_hybrid_kex",
			Msg: "needs " + prefix + "certificate_file: without one this listener serves plaintext, and nothing here could refuse a handshake"}
	case !t.Enabled() && t.ClientCAFile != "":
		return &FieldError{Field: prefix + "client_ca_file",
			Msg: "needs " + prefix + "certificate_file: without one this listener serves plaintext, and no client certificate is ever asked for"}
	case !t.Enabled():
		return nil
	case t.CertificateFile == "":
		return &FieldError{Field: prefix + "certificate_file", Msg: "is required when " + prefix + "key_file is set"}
	case t.KeyFile == "":
		return &FieldError{Field: prefix + "key_file", Msg: "is required when " + prefix + "certificate_file is set"}
	}
	return nil
}

// Validate is validate for a caller outside the loader, internal/tlsconf.
func (t TLSConfig) Validate(section string) error { return t.validate(section) }
