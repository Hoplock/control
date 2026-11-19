// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/accessctx"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/authoring"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/console"
	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/instance"
	"github.com/hoplock/control/internal/report"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/standing"
	"github.com/hoplock/control/internal/store"
)

// The north-bound listener's bring-up (0011).
//
// It is a file of its own beside serve.go because the two listeners share
// nothing (M2), and the seam is worth being able to read on its own.

// northDeps is what serve built that the north-bound surface stands on.
type northDeps struct {
	st          *store.Store
	ca          *credential.CA
	emitter     *audit.Emitter
	grants      *access.Service
	pushes      *accessctx.Service
	fleet       *fleet.Registry
	revocations *revoke.Operator
	standing    *standing.Service
	extensions  *ext.Extensions
	// archive is the feed to a registered archive (0025), nil when none is
	// registered — a nil interface, never a typed nil.
	archive north.ArchiveFeed
	// sinks is delivery to the registered sinks (0026), nil when none is
	// registered, likewise.
	sinks north.SinkDelivery
	// actions is the registered action handler's lifecycle (0039), nil when
	// none is registered, likewise.
	actions north.ActionHandlerState
	// sync is the registered identity sync's lifecycle (0040), nil when none
	// is registered, likewise.
	sync north.IdentitySyncState
	// recalc brings directory subjects onto a newly written claim mapping
	// (0043). It is always built.
	recalc north.MappingRecalculation
	// instance is the deployment's identity and health (0046). It is
	// always built.
	instance *instance.Service
}

// buildNorth assembles the federation service, the policy lifecycle and the
// north-bound handler tree, over the services serve built.
func buildNorth(ctx context.Context, cfg *config.Config, d northDeps, h Host, log *slog.Logger) (*http.Server, *north.Server, error) {
	st := d.st
	federation, err := buildFederation(cfg, st, d.emitter, log)
	if err != nil {
		return nil, nil, err
	}

	// The policy lifecycle (0019): Control's own publish-time checks, then
	// every registered validator, over the fleet and the audit evidence.
	var validators []ext.Bound[ext.PolicyValidator]
	if d.extensions != nil {
		validators = d.extensions.PolicyValidators()
	}
	lifecycle, err := authoring.New(authoring.Options{
		Store: st, Fleet: d.fleet, Emitter: d.emitter, Reader: audit.NewReader(st),
		Validators: validators, Interpreters: cfg.Policy.Interpreters,
	})
	if err != nil {
		return nil, nil, err
	}

	// The registered report packs (0044), served under the node's own
	// ceiling: a provider runs in this process, beside the decision path.
	var providers []ext.Bound[ext.ReportProvider]
	if d.extensions != nil {
		providers = d.extensions.ReportProviders()
	}
	reports, err := report.New(providers, report.Options{MaxConcurrent: cfg.North.MaxConcurrentReports})
	if err != nil {
		return nil, nil, err
	}

	// The operator console (0049), mounted inside its own namespace (M27):
	// files and a history fallback under /console/, and nothing else.
	shell, err := console.New(console.Options{Stripe: cfg.Console.Stripe})
	if err != nil {
		return nil, nil, err
	}

	handler, err := north.New(north.Options{
		Federation:           federation,
		Instance:             d.instance,
		CA:                   d.ca,
		Grants:               d.grants,
		AccessContext:        d.pushes,
		Store:                st,
		Fleet:                d.fleet,
		Authoring:            lifecycle,
		Revocations:          d.revocations,
		Standing:             d.standing,
		Audit:                d.emitter,
		Extensions:           d.extensions,
		Archive:              d.archive,
		Sinks:                d.sinks,
		Actions:              d.actions,
		IdentitySync:         d.sync,
		MappingRecalculation: d.recalc,
		Reports:              reports,
		MaxPushBytes:         cfg.AccessContext.MaxPushBytes,
		Logger:               log,
		MaxBodyBytes:         cfg.North.MaxBodyBytes,
		RequestTimeout:       cfg.North.RequestTimeout,
		ReportTimeout:        cfg.North.ReportTimeout,
		InsecureCookies:      cfg.North.InsecureCookies,
		DefaultTenant:        store.Tenant(cfg.Tenant),
		// A host's routes enter the same table, behind the same chain (M15).
		HostProvider:   h.Provider,
		HostRoutes:     h.Routes,
		HostErrorCodes: h.ErrorCodes,
		Consoles: []north.Console{{
			Patterns:  shell.Patterns(),
			Catalogue: shell.Catalogue(),
			Handler:   shell,
		}},
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

// buildCA wires the certificate authority over the deployment's key store, or
// returns nil when it has none.
//
// THE KEY STORE IS AN ARGUMENT, NOT SOMETHING BUILT HERE. It is the one
// buildKeyStore resolved — a registered ext.KeyStore, else Control's software
// custodian over the key-encryption key — and the same value the retention keys
// are signed with, so the CA and retention can never disagree about where this
// deployment's keys live. The CA generates its keys there and asks it for every
// signer; a key it does not hold is refused (credential.CustodyError), and no
// other custodian is asked.
//
// NIL IS A LEGITIMATE ANSWER AND AN ERROR IS NOT. A deployment with neither a
// key-encryption key nor a registered key store has no CA — but it still has a
// console, a policy surface and an audit store, so refusing to boot over it
// would make the product unreachable to fix. The north-bound CA routes then
// answer `ca_not_configured` naming both ways to configure one.
func buildCA(cfg *config.Config, st *store.Store, keys keyCustody, log *slog.Logger) (*credential.CA, error) {
	if keys.store == nil {
		log.Warn("this deployment has no certificate authority",
			"event", "ssh_ca_not_configured",
			"note", noKeyStoreNote,
		)
		return nil, nil
	}
	return credential.New(st, keys.store,
		credential.WithKeyStoreName(keys.name),
		credential.WithValidity(cfg.Credential.CertificateValidity),
		credential.WithMaxValidity(cfg.Credential.MaxCertificateValidity),
		credential.WithRotationOverlap(cfg.Credential.RotationOverlap),
		credential.WithLogger(log),
	)
}

// noKeyStoreNote is what a deployment with no key store is told to set: either
// of the two ways to have one.
const noKeyStoreNote = "set credential.key_encryption_key_env to a variable holding 32 base64 bytes, or register an ext.KeyStore"

// keyEncryptionKey reads the software custodian's key-encryption key: nil when
// the deployment names none, an error when it names one that cannot be used.
func keyEncryptionKey(cfg *config.Config) ([]byte, error) {
	variable := strings.TrimSpace(cfg.Credential.KeyEncryptionKeyEnv)
	if variable == "" {
		return nil, nil
	}
	encoded, ok := os.LookupEnv(variable)
	if !ok || encoded == "" {
		return nil, fmt.Errorf(
			"credential.key_encryption_key_env names %s, which is unset: the key store cannot start", variable)
	}
	kek, err := decodeKEK(encoded)
	if err != nil {
		// The ERROR NAMES THE VARIABLE AND NEVER ITS VALUE, which is the
		// whole point of reading a secret through a name.
		return nil, fmt.Errorf("the key in %s could not be read: %w", variable, err)
	}
	return kek, nil
}

// keyCustody is this deployment's one key store — where the CA's keys and the
// retention keys live — and the name an operator knows it by. The zero value is
// none.
type keyCustody struct {
	store ext.KeyStore
	name  string
}

// softwareKeyStoreName is what Control's software custodian is called in a
// message naming the key store that was asked.
const softwareKeyStoreName = "Control's software custodian"

// buildKeyStore is where this deployment's keys live (`ext.PointKeyStore`): a
// registered ext.KeyStore, or Control's software custodian over the
// key-encryption key, or — with neither — none. It is the ONE resolution: the
// CA (0062) and the retention keys (0024) are both handed what it returns, and
// nothing else in this package builds a key store. The daemon, `audit-verify`,
// `audit-retention` and the `ca` subcommands all build it here, after the
// host's registry is sealed, so a command signs and verifies with exactly the
// key store the server does.
func buildKeyStore(cfg *config.Config, st *store.Store, extensions *ext.Extensions) (keyCustody, error) {
	if extensions != nil {
		if ks, ok := extensions.KeyStore(); ok {
			return keyCustody{store: ks, name: registeredKeyStoreName(extensions)}, nil
		}
	}
	kek, err := keyEncryptionKey(cfg)
	if err != nil || kek == nil {
		return keyCustody{}, err
	}
	software, err := extdefault.NewSoftwareKeyStore(st, kek)
	if err != nil {
		return keyCustody{}, err
	}
	return keyCustody{store: software, name: softwareKeyStoreName}, nil
}

// registeredKeyStoreName names the registered key store by its registration,
// which is what the start-up log and `GET …/extensions` call it too.
func registeredKeyStoreName(x *ext.Extensions) string {
	for _, s := range x.Status() {
		if s.Info.Point == ext.PointKeyStore && s.Registered() {
			return "registered ext.KeyStore " + s.Registrations[0].String()
		}
	}
	return "registered ext.KeyStore"
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
