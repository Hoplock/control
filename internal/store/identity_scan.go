// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"encoding/json"
	"time"
)

// The scan helpers for 0011's rows. Each serves both a QueryRow lookup and a
// loop over a result set, through rowScanner.

// rowIterator is the loop half of pgx.Rows, so a collector can take either a
// real result set or a fake one.
type rowIterator interface {
	rowScanner
	Next() bool
	Err() error
}

func scanGroup(op string, row rowScanner) (Group, error) {
	var g Group
	err := row.Scan(&g.ID, &g.DisplayName, &g.Description, &g.Source, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return Group{}, wrap(op, err)
	}
	return g, nil
}

func scanConnector(op string, row rowScanner) (Connector, error) {
	var (
		c    Connector
		kind string
	)
	err := row.Scan(&c.Name, &kind, &c.DisplayName, &c.Enabled, &c.Config, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Connector{}, wrap(op, err)
	}
	c.Kind = ConnectorKind(kind)
	c.Config = nonNilJSON(c.Config)
	return c, nil
}

func scanClaimMapping(op string, row rowScanner) (ClaimMapping, error) {
	var m ClaimMapping
	err := row.Scan(&m.Version, &m.Document, &m.Digest, &m.Active, &m.CreatedAt, &m.CreatedBy)
	if err != nil {
		return ClaimMapping{}, wrap(op, err)
	}
	return m, nil
}

func scanFederatedIdentity(op string, row rowScanner) (FederatedIdentity, error) {
	var f FederatedIdentity
	err := row.Scan(&f.Connector, &f.ExternalSubject, &f.SubjectID, &f.FirstSeenAt, &f.LastSeenAt)
	if err != nil {
		return FederatedIdentity{}, wrap(op, err)
	}
	return f, nil
}

func scanFlowState(op string, row rowScanner) (FlowState, error) {
	var (
		f        FlowState
		kind     string
		consumed *time.Time
	)
	err := row.Scan(&f.State, &f.Connector, &kind, &f.Nonce, &f.PKCEVerifier,
		&f.RequestID, &f.RedirectURI, &f.CreatedAt, &f.ExpiresAt, &consumed)
	if err != nil {
		return FlowState{}, wrap(op, err)
	}
	f.Kind = ConnectorKind(kind)
	f.ConsumedAt = timeOrZero(consumed)
	return f, nil
}

func scanNorthPrincipal(op string, row rowScanner) (NorthPrincipal, error) {
	var (
		p                          NorthPrincipal
		kind                       string
		scopes                     []byte
		lastUsed, expires, revoked *time.Time
	)
	err := row.Scan(&p.ID, &kind, &p.SubjectID, &p.DisplayName, &p.Source,
		&p.BreakGlass, &p.MappingVersion, &p.TokenDigest, &p.SessionDigest,
		&scopes, &p.CreatedAt, &lastUsed, &expires, &revoked)
	if err != nil {
		return NorthPrincipal{}, wrap(op, err)
	}
	p.Kind = PrincipalKind(kind)
	p.LastUsedAt, p.ExpiresAt, p.RevokedAt = timeOrZero(lastUsed), timeOrZero(expires), timeOrZero(revoked)
	p.Scopes = map[string][]string{}
	if len(scopes) > 0 {
		if err := json.Unmarshal(scopes, &p.Scopes); err != nil {
			// A scope document that does not decode is NOT an empty
			// scope set: that would silently turn a broken row into a
			// principal with access to nothing, which reads as a
			// permissions bug during an incident. It is an outage.
			return NorthPrincipal{}, wrap(op, err)
		}
	}
	return p, nil
}

func scanCAKey(op string, row rowScanner) (CAKey, error) {
	var (
		k                     CAKey
		trustedUntil, retired *time.Time
	)
	err := row.Scan(&k.KeyID, &k.Algorithm, &k.PublicKey, &k.PrivateRef, &k.PrivateKey,
		&k.Active, &trustedUntil, &k.Comment, &k.CreatedAt, &retired)
	if err != nil {
		return CAKey{}, wrap(op, err)
	}
	k.TrustedUntil, k.RetiredAt = timeOrZero(trustedUntil), timeOrZero(retired)
	return k, nil
}

func scanSSHCertificate(op string, row rowScanner) (SSHCertificate, error) {
	var (
		c       SSHCertificate
		revoked *time.Time
	)
	err := row.Scan(&c.Serial, &c.KeyID, &c.CAKeyID, &c.SubjectID, &c.SessionID,
		&c.Principals, &c.Target, &c.TargetPort, &c.SourceAddress,
		&c.ValidAfter, &c.ValidBefore, &c.IssuedAt, &revoked, &c.RevokedReason,
		&c.Certificate)
	if err != nil {
		return SSHCertificate{}, wrap(op, err)
	}
	c.RevokedAt = timeOrZero(revoked)
	c.Principals = nonNilStrings(c.Principals)
	return c, nil
}

// nonNilScopes normalises a nil scope map to an empty one, so the column's
// NOT NULL default is never fought with a JSON `null`.
func nonNilScopes(v map[string][]string) map[string][]string {
	if v == nil {
		return map[string][]string{}
	}
	return v
}
