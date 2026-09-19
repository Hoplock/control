// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// `hoplock-control seed` writes a development and CI fixture set.
//
// IT EXISTS BECAUSE THE NORTH-BOUND API DOES NOT YET (0014). The conformance
// suite grades "a server configured to serve these identities" (M1), and until
// there is an operator surface there is no way to configure one — so the
// alternative to this command is a conformance leg that cannot run against
// this server at all, which is the acceptance criterion phase 0007 is graded
// on.
//
// Three properties keep it from becoming a back door:
//
//   - it writes and never reads, so it cannot be used to extract anything;
//   - it takes a file, so what it wrote is reviewable in version control;
//   - the credentials it writes are hashed exactly as the running server
//     writes them — there is no seed-only path into the credential tables.
//
// When 0014 lands, this becomes a thin client of that API or it goes away.
func runSeed(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("hoplock-control seed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to the YAML configuration file")
	seedPath := fs.String("file", "", "path to the seed document (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *seedPath == "" {
		return fmt.Errorf("seed: --file is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	doc, err := loadSeed(*seedPath)
	if err != nil {
		return err
	}

	ctx := context.Background()
	st, err := store.Open(ctx, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	tenant := store.Tenant(cfg.Tenant)
	if doc.Tenant != "" {
		tenant = store.Tenant(doc.Tenant)
	}
	return doc.apply(ctx, st, tenant, stdout)
}

// seedDocument is the fixture set.
//
// It is decoded STRICTLY: a key it does not define is a typo that would
// otherwise silently seed nothing, and a fixture that silently seeds nothing
// makes a conformance case fail for a reason nobody can see in the file.
type seedDocument struct {
	// Tenant overrides the configured tenant, so one database can hold a
	// fixture set beside real data.
	Tenant string `yaml:"tenant"`
	// Subjects are the identities the auth endpoints resolve.
	Subjects []seedSubject `yaml:"subjects"`
	// Proxies are the fleet members. Their keys are what makes a chain leg
	// recognisable (proxy D11).
	Proxies []seedProxy `yaml:"proxies"`
	// HostKeys are target host keys this server already trusts, so a
	// conformance run has a `known: true` case to grade.
	HostKeys []seedHostKey `yaml:"host_keys"`
	// Tokens are south-bound channel credentials (M2).
	Tokens []seedToken `yaml:"tokens"`
}

type seedSubject struct {
	ID          string            `yaml:"id"`
	DisplayName string            `yaml:"display_name"`
	Source      string            `yaml:"source"`
	Principals  []string          `yaml:"principals"`
	Groups      []string          `yaml:"groups"`
	Claims      map[string]string `yaml:"claims"`
	// Password is the plaintext to hash. It is read from the file, hashed,
	// and dropped; nothing writes it anywhere.
	Password string `yaml:"password"`
	// Keys are the public keys this subject may offer.
	Keys []seedKey `yaml:"keys"`
	// MFA, when present, enrolls the subject with the scripted provider.
	MFA *identity.ScriptedMFAConfig `yaml:"mfa"`
}

type seedKey struct {
	// Blob is base64 of the SSH wire encoding, exactly as the contract
	// carries it. The fingerprint is DERIVED from it rather than taken from
	// the file, so a fixture cannot claim a fingerprint its key does not
	// have — which is the failure that would make a chain-leg test pass
	// against the wrong thing.
	Blob string `yaml:"blob"`
	// Fingerprint may be given INSTEAD of a blob, for a fixture that has no
	// real key bytes to offer.
	Fingerprint   string `yaml:"fingerprint"`
	Type          string `yaml:"type"`
	IsCertificate bool   `yaml:"is_certificate"`
	// ValidFrom and ValidTo are the certificate validity window, RFC 3339.
	ValidFrom string `yaml:"valid_from"`
	ValidTo   string `yaml:"valid_to"`
	// RevokedAt marks a key that has been withdrawn.
	RevokedAt string `yaml:"revoked_at"`
}

type seedProxy struct {
	ID   string `yaml:"id"`
	Zone string `yaml:"zone"`
	// Key is the proxy's own key. Its fingerprint is what `/v1/auth/cert`
	// recognises as a chain leg, and it is derived from the blob by the
	// same expression the `proxies.key_fingerprint` column uses.
	Key seedKey `yaml:"key"`
	// Heartbeat, when true, marks the proxy as having just reported, so it
	// is a routing option.
	Heartbeat bool `yaml:"heartbeat"`
	// State defaults to "enrolled". Seed "revoked" to prove a withdrawn
	// proxy cannot authenticate a chain leg.
	State string `yaml:"state"`
}

type seedHostKey struct {
	Target      string `yaml:"target"`
	Port        int32  `yaml:"port"`
	Fingerprint string `yaml:"fingerprint"`
	Type        string `yaml:"type"`
}

type seedToken struct {
	// Secret is the token's secret half. The presented credential is
	// `<tenant>.<secret>`; only its SHA-256 is stored.
	Secret string `yaml:"secret"`
	// ProxyID binds the token to one proxy. EMPTY mints an UNBOUND token,
	// which authenticates "a proxy of this tenant" and nothing narrower —
	// spelled out here rather than arrived at by leaving a field blank.
	ProxyID string `yaml:"proxy_id"`
	Label   string `yaml:"label"`
}

func loadSeed(path string) (*seedDocument, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}
	defer func() { _ = f.Close() }()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)

	var doc seedDocument
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("seed: parse %s: %w", path, err)
	}
	return &doc, nil
}

func (d *seedDocument) apply(ctx context.Context, st *store.Store, tenant store.Tenant, stdout io.Writer) error {
	say := func(format string, args ...any) {
		_, _ = fmt.Fprintf(stdout, format+"\n", args...)
	}

	for _, s := range d.Subjects {
		if s.ID == "" {
			return fmt.Errorf("seed: a subject has no id")
		}
		source := s.Source
		if source == "" {
			source = identity.SourceLocal
		}
		if err := st.Subjects().Upsert(ctx, tenant, store.Subject{
			ID:          s.ID,
			Source:      source,
			DisplayName: s.DisplayName,
			Principals:  s.Principals,
			Groups:      s.Groups,
			Claims:      s.Claims,
		}); err != nil {
			return err
		}

		for _, k := range s.Keys {
			fingerprint, err := k.resolveFingerprint()
			if err != nil {
				return fmt.Errorf("seed: subject %q: %w", s.ID, err)
			}
			from, err := seedTime(k.ValidFrom)
			if err != nil {
				return fmt.Errorf("seed: subject %q key: %w", s.ID, err)
			}
			to, err := seedTime(k.ValidTo)
			if err != nil {
				return fmt.Errorf("seed: subject %q key: %w", s.ID, err)
			}
			revoked, err := seedTime(k.RevokedAt)
			if err != nil {
				return fmt.Errorf("seed: subject %q key: %w", s.ID, err)
			}
			if err := st.SubjectKeys().Put(ctx, tenant, store.SubjectKey{
				Fingerprint:   fingerprint,
				SubjectID:     s.ID,
				KeyType:       k.Type,
				IsCertificate: k.IsCertificate,
				ValidFrom:     from,
				ValidTo:       to,
				RevokedAt:     revoked,
			}); err != nil {
				return err
			}
		}

		if s.Password != "" {
			// Hashed by the same function the running server verifies
			// against. There is no seed-only path into this table.
			digest, err := identity.HashPassword(s.ID, s.Password)
			if err != nil {
				return err
			}
			if err := st.SubjectPasswords().Put(ctx, tenant, digest); err != nil {
				return err
			}
		}

		if s.MFA != nil {
			cfg, err := json.Marshal(s.MFA)
			if err != nil {
				return err
			}
			if err := st.MFA().PutEnrollment(ctx, tenant, store.MFAEnrollment{
				SubjectID: s.ID,
				Provider:  identity.ScriptedMFAName,
				Config:    cfg,
			}); err != nil {
				return err
			}
		}
		say("subject %s (%d key(s))", s.ID, len(s.Keys))
	}

	for _, p := range d.Proxies {
		if p.ID == "" {
			return fmt.Errorf("seed: a proxy has no id")
		}
		blob, err := p.Key.blobBytes()
		if err != nil {
			return fmt.Errorf("seed: proxy %q: %w", p.ID, err)
		}
		if len(blob) == 0 {
			// The generated fingerprint column derives from the KEY, so a
			// proxy fixture needs real bytes. A fingerprint on its own
			// would seed a proxy no chain leg could ever match, and the
			// test that noticed would be the one that stopped testing.
			return fmt.Errorf("seed: proxy %q needs key.blob: the chain-leg lookup derives "+
				"the fingerprint from the key material, so a bare fingerprint would match nothing", p.ID)
		}
		state := store.EnrollmentState(p.State)
		if state == "" {
			state = store.EnrollmentEnrolled
		}
		proxy := store.Proxy{
			ID:        p.ID,
			Zone:      p.Zone,
			PublicKey: blob,
			State:     state,
		}
		if p.Heartbeat {
			proxy.LastHeartbeatAt = time.Now().UTC()
		}
		if err := st.Proxies().Upsert(ctx, tenant, proxy); err != nil {
			return err
		}
		say("proxy %s (%s) key %s", p.ID, state, identity.KeyFingerprint(blob))
	}

	for _, k := range d.HostKeys {
		if k.Target == "" || k.Fingerprint == "" {
			return fmt.Errorf("seed: a host key entry needs a target and a fingerprint")
		}
		now := time.Now().UTC()
		if _, _, err := st.TargetHostKeys().Record(ctx, tenant, store.TargetHostKey{
			Hostname:        k.Target,
			Port:            k.Port,
			Fingerprint:     k.Fingerprint,
			KeyType:         k.Type,
			Decision:        store.HostKeyAccepted,
			LastSeenAt:      now,
			FirstReportedBy: "seed",
			LastReportedBy:  "seed",
		}); err != nil {
			return err
		}
		say("host key %s %s", k.Target, k.Fingerprint)
	}

	for _, t := range d.Tokens {
		if t.Secret == "" {
			return fmt.Errorf("seed: a token has no secret")
		}
		token := fleet.ProxyToken{Tenant: tenant, Secret: t.Secret}
		tokenID := "seed-" + base64.RawURLEncoding.EncodeToString([]byte(t.Label+t.ProxyID))
		if err := st.ProxyTokens().Insert(ctx, tenant, store.ProxyAPIToken{
			TokenID:   tokenID,
			ProxyID:   t.ProxyID,
			TokenHash: token.Hash(),
			Label:     t.Label,
			IssuedAt:  time.Now().UTC(),
		}); err != nil && !store.IsConflict(err) {
			return err
		}
		// The secret is NOT printed. It came from a file the operator
		// already has, and a credential echoed to a CI log is a credential
		// in that log forever.
		bound := t.ProxyID
		if bound == "" {
			bound = "(unbound)"
		}
		say("token %s for %s", tokenID, bound)
	}
	return nil
}

// resolveFingerprint derives a key's fingerprint from its blob where one was
// given, and otherwise takes the stated one.
//
// Deriving wins over stating, always. A fixture that claimed a fingerprint its
// key does not have would make a chain-leg test pass against the wrong thing.
func (k seedKey) resolveFingerprint() (string, error) {
	blob, err := k.blobBytes()
	if err != nil {
		return "", err
	}
	if len(blob) > 0 {
		return identity.KeyFingerprint(blob), nil
	}
	if k.Fingerprint == "" {
		return "", fmt.Errorf("a key needs either blob or fingerprint")
	}
	return k.Fingerprint, nil
}

func (k seedKey) blobBytes() ([]byte, error) {
	if k.Blob == "" {
		return nil, nil
	}
	blob, err := base64.StdEncoding.DecodeString(k.Blob)
	if err != nil {
		return nil, fmt.Errorf("key blob is not base64: %w", err)
	}
	return blob, nil
}

func seedTime(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not RFC 3339: %w", v, err)
	}
	return t, nil
}
