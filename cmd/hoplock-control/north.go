// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The north-bound listener's bring-up (0011).
//
// It is a file of its own beside serve.go because the two listeners share
// nothing (M2), and the seam is worth being able to read on its own.

// buildNorth assembles the federation service, the certificate authority and
// the north-bound handler tree.
func buildNorth(
	ctx context.Context,
	cfg *config.Config,
	st *store.Store,
	emitter identity.AuditSink,
	log *slog.Logger,
) (*http.Server, *north.Server, error) {
	federation, err := buildFederation(cfg, st, emitter, log)
	if err != nil {
		return nil, nil, err
	}

	ca, err := buildCA(cfg, st, log)
	if err != nil {
		return nil, nil, err
	}

	handler, err := north.New(north.Options{
		Federation:      federation,
		CA:              ca,
		Logger:          log,
		MaxBodyBytes:    cfg.North.MaxBodyBytes,
		RequestTimeout:  cfg.North.RequestTimeout,
		InsecureCookies: cfg.North.InsecureCookies,
		DefaultTenant:   store.Tenant(cfg.Tenant),
	})
	if err != nil {
		return nil, nil, err
	}

	// The configured tenant's certificate authority is created on boot rather
	// than on the first session, so a key generation is not on a user's
	// handshake. A tenant nothing has configured gets its CA on first use.
	if err := handler.Ensure(ctx, store.Tenant(cfg.Tenant)); err != nil {
		return nil, nil, err
	}

	return &http.Server{
		Addr:    cfg.Listeners.North,
		Handler: handler,
		// ReadHeaderTimeout bounds a caller that opens a connection and
		// sends nothing.
		ReadHeaderTimeout: cfg.North.RequestTimeout,
	}, handler, nil
}

// buildFederation wires logins, sessions and API tokens.
func buildFederation(
	cfg *config.Config,
	st *store.Store,
	emitter identity.AuditSink,
	log *slog.Logger,
) (*identity.Federation, error) {
	return identity.NewFederation(st,
		identity.WithSessionTTL(cfg.Identity.SessionTTL),
		identity.WithFlowTTL(cfg.Identity.FlowTTL),
		identity.WithAuditSink(emitter),
		identity.WithFederationLogger(log),
	)
}

// buildCA wires the certificate authority, or returns nil when this deployment
// has no key-encryption key.
//
// NIL IS A LEGITIMATE ANSWER AND AN ERROR IS NOT. The software custodian refuses
// to hold a CA private key without a key to encrypt it with, and a deployment
// that has not set one has no CA — but it still has a console, a policy surface
// and an audit store, so refusing to boot over it would make the product
// unreachable to fix. The north-bound CA routes then answer
// `ca_not_configured` naming the key to set.
//
// A MISCONFIGURED key, on the other hand, IS an error: a variable that is named
// and unset, or set to something that is not 32 base64 bytes, is an operator who
// believes they have a CA.
func buildCA(cfg *config.Config, st *store.Store, log *slog.Logger) (*credential.CA, error) {
	variable := strings.TrimSpace(cfg.Credential.KeyEncryptionKeyEnv)
	if variable == "" {
		log.Warn("this deployment has no certificate authority",
			"event", "ssh_ca_not_configured",
			"note", "set credential.key_encryption_key_env to a variable holding 32 base64 bytes",
		)
		return nil, nil
	}

	encoded, ok := os.LookupEnv(variable)
	if !ok || encoded == "" {
		return nil, fmt.Errorf(
			"credential.key_encryption_key_env names %s, which is unset: the certificate authority cannot start", variable)
	}
	kek, err := decodeKEK(encoded)
	if err != nil {
		// The ERROR NAMES THE VARIABLE AND NEVER ITS VALUE, which is the
		// whole point of reading a secret through a name.
		return nil, fmt.Errorf("the key in %s could not be read: %w", variable, err)
	}

	keys, err := extdefault.NewSoftwareKeyStore(st, kek)
	if err != nil {
		return nil, err
	}
	return credential.New(st, keys,
		credential.WithValidity(cfg.Credential.CertificateValidity),
		credential.WithMaxValidity(cfg.Credential.MaxCertificateValidity),
		credential.WithRotationOverlap(cfg.Credential.RotationOverlap),
		credential.WithLogger(log),
	)
}

// decodeKEK reads a base64 key, accepting both alphabets and both paddings —
// because an operator pasting the output of `openssl rand -base64 32` and one
// pasting `head -c 32 /dev/urandom | basenc --base64url` should both work.
func decodeKEK(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if raw, err := enc.DecodeString(encoded); err == nil {
			if len(raw) != extdefault.KeyEncryptionKeySize {
				return nil, fmt.Errorf("it decodes to %d bytes; %d are required",
					len(raw), extdefault.KeyEncryptionKeySize)
			}
			return raw, nil
		}
	}
	return nil, fmt.Errorf("it is not base64")
}
