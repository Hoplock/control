// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"errors"
	"testing"

	"github.com/hoplock/control/internal/config"
)

// NO TLS BLOCK IS PLAINTEXT, exactly as before the block existed (0063): an
// existing configuration gains no requirement and no mandatory key.
func TestNoTLSBlockIsPlaintext(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.South.TLS.Enabled() || cfg.North.TLS.Enabled() {
		t.Fatalf("a file with no tls block enabled TLS: south %+v, north %+v", cfg.South.TLS, cfg.North.TLS)
	}
	if cfg.South.TLS != (config.TLSConfig{}) || cfg.North.TLS != (config.TLSConfig{}) {
		t.Fatalf("a file with no tls block defaulted something: south %+v, north %+v", cfg.South.TLS, cfg.North.TLS)
	}
}

// Each listener's block is its own: one may terminate TLS here while the other
// is terminated in front (M2).
func TestEachListenerHasItsOwnTLSBlock(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validYAML+`
south:
  tls:
    certificate_file: /etc/hoplock/south.pem
    key_file: /etc/hoplock/south-key.pem
    client_ca_file: /etc/hoplock/proxies.pem
    require_hybrid_kex: true
`))
	if err != nil {
		t.Fatal(err)
	}
	want := config.TLSConfig{CertificateFile: "/etc/hoplock/south.pem", KeyFile: "/etc/hoplock/south-key.pem",
		ClientCAFile: "/etc/hoplock/proxies.pem", RequireHybridKex: true}
	if cfg.South.TLS != want {
		t.Fatalf("south.tls = %+v", cfg.South.TLS)
	}
	if cfg.North.TLS.Enabled() {
		t.Fatalf("north.tls was enabled by south's block: %+v", cfg.North.TLS)
	}
}

// A BLOCK THAT CANNOT BE HONOURED IS REFUSED AT LOAD, never served as
// plaintext. That its files exist is internal/tlsconf's to check, at start-up.
func TestATLSBlockThatCannotBeHonouredIsRefused(t *testing.T) {
	cases := []struct {
		name, block, field string
	}{
		{"a certificate and no key", "south:\n  tls:\n    certificate_file: c.pem\n", "south.tls.key_file"},
		{"a key and no certificate", "north:\n  tls:\n    key_file: k.pem\n", "north.tls.certificate_file"},
		{"a hybrid requirement with no certificate", "north:\n  tls:\n    require_hybrid_kex: true\n", "north.tls.require_hybrid_kex"},
		{"a client CA with no certificate", "south:\n  tls:\n    client_ca_file: ca.pem\n", "south.tls.client_ca_file"},
		{"an unknown key", "south:\n  tls:\n    curve_preferences: [x25519]\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, validYAML+tc.block))
			if err == nil {
				t.Fatal("Load accepted it")
			}
			if tc.field == "" {
				return
			}
			var fe *config.FieldError
			if !errors.As(err, &fe) || fe.Field != tc.field {
				t.Fatalf("Load = %v; want a field error on %s", err, tc.field)
			}
		})
	}
}
