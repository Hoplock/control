// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

// The repositories behind the south-bound API (0007). Every method names its
// tenant, like every other one in this package (M18), and absence is
// ErrNotFound rather than a zero value — a credential lookup that could not
// tell "no such key" from "the database did not answer" is M11 waiting to
// happen one layer up.

// ---------------------------------------------------------------------------
// subject keys
// ---------------------------------------------------------------------------

type subjectKeyRepo struct{ s *Store }

const subjectKeyColumns = `fingerprint, subject_id, key_type, is_certificate,
	valid_from, valid_to, revoked_at, created_at`

func (r subjectKeyRepo) GetByFingerprint(ctx context.Context, tenant Tenant, fingerprint string) (SubjectKey, error) {
	const op = "store.SubjectKeys.GetByFingerprint"
	if err := checkTenant(op, tenant); err != nil {
		return SubjectKey{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+subjectKeyColumns+`
		FROM subject_keys
		WHERE tenant = $1 AND fingerprint = $2`, tenant, fingerprint)
	return scanSubjectKey(op, row)
}

func (r subjectKeyRepo) ListBySubject(ctx context.Context, tenant Tenant, subjectID string) ([]SubjectKey, error) {
	const op = "store.SubjectKeys.ListBySubject"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+subjectKeyColumns+`
		FROM subject_keys
		WHERE tenant = $1 AND subject_id = $2
		ORDER BY fingerprint`, tenant, subjectID)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []SubjectKey
	for rows.Next() {
		k, err := scanSubjectKey(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, wrap(op, rows.Err())
}

func (r subjectKeyRepo) Put(ctx context.Context, tenant Tenant, k SubjectKey) error {
	const op = "store.SubjectKeys.Put"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if k.Fingerprint == "" || k.SubjectID == "" {
		return invalid(op, "fingerprint and subject id are required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO subject_keys
			(tenant, fingerprint, subject_id, key_type, is_certificate, valid_from, valid_to, revoked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant, fingerprint) DO UPDATE SET
			subject_id     = EXCLUDED.subject_id,
			key_type       = EXCLUDED.key_type,
			is_certificate = EXCLUDED.is_certificate,
			valid_from     = EXCLUDED.valid_from,
			valid_to       = EXCLUDED.valid_to,
			revoked_at     = EXCLUDED.revoked_at`,
		tenant, k.Fingerprint, k.SubjectID, k.KeyType, k.IsCertificate,
		nullableTime(k.ValidFrom), nullableTime(k.ValidTo), nullableTime(k.RevokedAt))
	return wrap(op, err)
}

func (r subjectKeyRepo) Revoke(ctx context.Context, tenant Tenant, fingerprint string, at time.Time) error {
	const op = "store.SubjectKeys.Revoke"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// COALESCE, so a second revocation keeps the first instant: the question
	// an auditor asks is when the key stopped working, and the later answer
	// is not more true than the first (the same rule GrantRepository.Revoke
	// follows).
	tag, err := r.s.db.Exec(ctx, `
		UPDATE subject_keys
		SET revoked_at = COALESCE(revoked_at, $3)
		WHERE tenant = $1 AND fingerprint = $2`, tenant, fingerprint, at)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func scanSubjectKey(op string, row rowScanner) (SubjectKey, error) {
	var (
		k                           SubjectKey
		validFrom, validTo, revoked *time.Time
	)
	err := row.Scan(&k.Fingerprint, &k.SubjectID, &k.KeyType, &k.IsCertificate,
		&validFrom, &validTo, &revoked, &k.CreatedAt)
	if err != nil {
		return SubjectKey{}, wrap(op, err)
	}
	k.ValidFrom, k.ValidTo, k.RevokedAt = timeOrZero(validFrom), timeOrZero(validTo), timeOrZero(revoked)
	return k, nil
}

// ---------------------------------------------------------------------------
// passwords
// ---------------------------------------------------------------------------

type passwordRepo struct{ s *Store }

func (r passwordRepo) Get(ctx context.Context, tenant Tenant, subjectID string) (PasswordDigest, error) {
	const op = "store.SubjectPasswords.Get"
	if err := checkTenant(op, tenant); err != nil {
		return PasswordDigest{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var d PasswordDigest
	err := r.s.db.QueryRow(ctx, `
		SELECT subject_id, algorithm, iterations, salt, digest, updated_at
		FROM subject_passwords
		WHERE tenant = $1 AND subject_id = $2`, tenant, subjectID).
		Scan(&d.SubjectID, &d.Algorithm, &d.Iterations, &d.Salt, &d.Digest, &d.UpdatedAt)
	if err != nil {
		return PasswordDigest{}, wrap(op, err)
	}
	return d, nil
}

func (r passwordRepo) Put(ctx context.Context, tenant Tenant, d PasswordDigest) error {
	const op = "store.SubjectPasswords.Put"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if d.SubjectID == "" || d.Algorithm == "" || len(d.Digest) == 0 {
		return invalid(op, "subject id, algorithm and digest are required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO subject_passwords (tenant, subject_id, algorithm, iterations, salt, digest)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant, subject_id) DO UPDATE SET
			algorithm  = EXCLUDED.algorithm,
			iterations = EXCLUDED.iterations,
			salt       = EXCLUDED.salt,
			digest     = EXCLUDED.digest,
			updated_at = now()`,
		tenant, d.SubjectID, d.Algorithm, d.Iterations, nonNilBytes(d.Salt), d.Digest)
	return wrap(op, err)
}

func (r passwordRepo) Delete(ctx context.Context, tenant Tenant, subjectID string) error {
	const op = "store.SubjectPasswords.Delete"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		DELETE FROM subject_passwords WHERE tenant = $1 AND subject_id = $2`, tenant, subjectID)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

// ---------------------------------------------------------------------------
// MFA enrollments and challenges
// ---------------------------------------------------------------------------

type mfaRepo struct{ s *Store }

func (r mfaRepo) GetEnrollment(ctx context.Context, tenant Tenant, subjectID string) (MFAEnrollment, error) {
	const op = "store.MFA.GetEnrollment"
	if err := checkTenant(op, tenant); err != nil {
		return MFAEnrollment{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var e MFAEnrollment
	err := r.s.db.QueryRow(ctx, `
		SELECT subject_id, provider, config, updated_at
		FROM subject_mfa
		WHERE tenant = $1 AND subject_id = $2`, tenant, subjectID).
		Scan(&e.SubjectID, &e.Provider, &e.Config, &e.UpdatedAt)
	if err != nil {
		return MFAEnrollment{}, wrap(op, err)
	}
	return e, nil
}

func (r mfaRepo) PutEnrollment(ctx context.Context, tenant Tenant, e MFAEnrollment) error {
	const op = "store.MFA.PutEnrollment"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if e.SubjectID == "" || e.Provider == "" {
		return invalid(op, "subject id and provider are required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO subject_mfa (tenant, subject_id, provider, config)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant, subject_id) DO UPDATE SET
			provider   = EXCLUDED.provider,
			config     = EXCLUDED.config,
			updated_at = now()`,
		tenant, e.SubjectID, e.Provider, nonNilJSON(e.Config))
	return wrap(op, err)
}

const mfaChallengeColumns = `token, subject_id, login, provider, provider_ref, prompt,
	poll_after_ms, state, polls, issued_at, expires_at, last_polled_at, resolved_at`

func (r mfaRepo) CreateChallenge(ctx context.Context, tenant Tenant, c MFAChallenge) error {
	const op = "store.MFA.CreateChallenge"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if c.Token == "" || c.SubjectID == "" {
		return invalid(op, "token and subject id are required")
	}
	if c.State == "" {
		c.State = MFAChallengePending
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// No ON CONFLICT. A token this server has already issued must never be
	// re-issued over the top of an outstanding challenge: that would resurrect
	// a spent token, which is the one thing single-use semantics exist to stop.
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO mfa_challenges
			(tenant, token, subject_id, login, provider, provider_ref, prompt,
			 poll_after_ms, state, polls, issued_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 0, $10, $11)`,
		tenant, c.Token, c.SubjectID, c.Login, c.Provider, c.ProviderRef, c.Prompt,
		c.PollAfterMS, string(c.State), c.IssuedAt, c.ExpiresAt)
	return wrap(op, err)
}

// PollChallenge takes the challenge under a row lock, stamps the poll, and
// hands it to the caller to decide on.
//
// The lock is the mechanism, exactly as it is for the uid cursor: two polls
// arriving together must not both observe a pending challenge and both resolve
// it, because "this challenge resolves once" is the whole of single use. A
// caller inside InTx keeps its own transaction; a pool-bound Store opens one.
//
// WHAT COMES BACK IS THE ROW AS IT STOOD BEFORE THIS POLL, with Polls already
// counting it. So LastPolledAt is the PREVIOUS poll's instant — zero on the
// first poll — which is the value poll-rate enforcement has to measure
// against. Returning the stamp just written would make every poll appear to
// have arrived zero milliseconds after the last one, i.e. always too soon.
func (r mfaRepo) PollChallenge(ctx context.Context, tenant Tenant, token string, at time.Time) (MFAChallenge, error) {
	const op = "store.MFA.PollChallenge"
	if err := checkTenant(op, tenant); err != nil {
		return MFAChallenge{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var c MFAChallenge
	err := r.s.inTx(ctx, op, func(ctx context.Context, tx querier) error {
		row := tx.QueryRow(ctx, `
			SELECT `+mfaChallengeColumns+`
			FROM mfa_challenges
			WHERE tenant = $1 AND token = $2
			FOR UPDATE`, tenant, token)
		got, err := scanMFAChallenge(op, row)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE mfa_challenges
			SET polls = polls + 1, last_polled_at = $3
			WHERE tenant = $1 AND token = $2`, tenant, token, at); err != nil {
			return wrap(op, err)
		}
		got.Polls++
		c = got
		return nil
	})
	if err != nil {
		return MFAChallenge{}, err
	}
	return c, nil
}

// ResolveChallenge spends a challenge, once.
//
// The `WHERE state = 'pending'` predicate is what makes it single use: a
// second resolution affects no rows and is ErrConflict, whichever way the
// first one went.
func (r mfaRepo) ResolveChallenge(ctx context.Context, tenant Tenant, token string, state MFAChallengeState, at time.Time) error {
	const op = "store.MFA.ResolveChallenge"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	switch state {
	case MFAChallengeApproved, MFAChallengeDenied, MFAChallengeExpired:
	case MFAChallengePending:
		return invalid(op, "pending is not a resolution")
	default:
		return invalid(op, "unknown challenge state")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		UPDATE mfa_challenges
		SET state = $3, resolved_at = $4
		WHERE tenant = $1 AND token = $2 AND state = 'pending'`,
		tenant, token, string(state), at)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return conflict(op, "challenge is not outstanding")
	}
	return nil
}

func (r mfaRepo) GetChallenge(ctx context.Context, tenant Tenant, token string) (MFAChallenge, error) {
	const op = "store.MFA.GetChallenge"
	if err := checkTenant(op, tenant); err != nil {
		return MFAChallenge{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+mfaChallengeColumns+`
		FROM mfa_challenges
		WHERE tenant = $1 AND token = $2`, tenant, token)
	return scanMFAChallenge(op, row)
}

func scanMFAChallenge(op string, row rowScanner) (MFAChallenge, error) {
	var (
		c                  MFAChallenge
		state              string
		lastPolled, solved *time.Time
	)
	err := row.Scan(&c.Token, &c.SubjectID, &c.Login, &c.Provider, &c.ProviderRef, &c.Prompt,
		&c.PollAfterMS, &state, &c.Polls, &c.IssuedAt, &c.ExpiresAt, &lastPolled, &solved)
	if err != nil {
		return MFAChallenge{}, wrap(op, err)
	}
	c.State = MFAChallengeState(state)
	c.LastPolledAt, c.ResolvedAt = timeOrZero(lastPolled), timeOrZero(solved)
	return c, nil
}

// ---------------------------------------------------------------------------
// target host keys
// ---------------------------------------------------------------------------

type hostKeyRepo struct{ s *Store }

const hostKeyColumns = `hostname, port, fingerprint, key_type, decision,
	first_seen_at, last_seen_at, first_reported_by, last_reported_by`

// Record is trust-on-first-use, in one statement.
//
// The insert carries the decision the caller made; the conflict clause touches
// only the sighting columns, so a key that has already been ruled on keeps its
// answer. `known` — whether this server had seen this exact key before — comes
// back from `xmax <> 0`, which Postgres sets on a row the statement updated
// rather than inserted. Deriving it from the write itself rather than from a
// preceding SELECT is what makes two proxies reporting a brand-new key
// concurrently agree on which of them saw it first.
func (r hostKeyRepo) Record(ctx context.Context, tenant Tenant, k TargetHostKey) (TargetHostKey, bool, error) {
	const op = "store.TargetHostKeys.Record"
	if err := checkTenant(op, tenant); err != nil {
		return TargetHostKey{}, false, err
	}
	if k.Hostname == "" || k.Fingerprint == "" {
		return TargetHostKey{}, false, invalid(op, "hostname and fingerprint are required")
	}
	if k.Decision == "" {
		return TargetHostKey{}, false, invalid(op, "decision is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var (
		known bool
		got   TargetHostKey
	)
	row := r.s.db.QueryRow(ctx, `
		INSERT INTO target_host_keys
			(tenant, hostname, port, fingerprint, key_type, decision,
			 first_seen_at, last_seen_at, first_reported_by, last_reported_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8, $8)
		ON CONFLICT (tenant, hostname, port, fingerprint) DO UPDATE SET
			last_seen_at     = EXCLUDED.last_seen_at,
			last_reported_by = EXCLUDED.last_reported_by
		RETURNING `+hostKeyColumns+`, (xmax <> 0) AS known`,
		tenant, k.Hostname, k.Port, k.Fingerprint, k.KeyType, string(k.Decision),
		k.LastSeenAt, k.LastReportedBy)

	var decision string
	err := row.Scan(&got.Hostname, &got.Port, &got.Fingerprint, &got.KeyType, &decision,
		&got.FirstSeenAt, &got.LastSeenAt, &got.FirstReportedBy, &got.LastReportedBy, &known)
	if err != nil {
		return TargetHostKey{}, false, wrap(op, err)
	}
	got.Decision = HostKeyDecisionKind(decision)
	return got, known, nil
}

// ListForTarget returns every key this target has been seen presenting, oldest
// first. It is what makes a CHANGED key visible: the answer to a report is
// about one fingerprint, and "this target already presented a different one"
// is a question about the set.
func (r hostKeyRepo) ListForTarget(ctx context.Context, tenant Tenant, hostname string, port int32) ([]TargetHostKey, error) {
	const op = "store.TargetHostKeys.ListForTarget"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+hostKeyColumns+`
		FROM target_host_keys
		WHERE tenant = $1 AND hostname = $2 AND port = $3
		ORDER BY first_seen_at, fingerprint`, tenant, hostname, port)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []TargetHostKey
	for rows.Next() {
		var (
			k        TargetHostKey
			decision string
		)
		if err := rows.Scan(&k.Hostname, &k.Port, &k.Fingerprint, &k.KeyType, &decision,
			&k.FirstSeenAt, &k.LastSeenAt, &k.FirstReportedBy, &k.LastReportedBy); err != nil {
			return nil, wrap(op, err)
		}
		k.Decision = HostKeyDecisionKind(decision)
		out = append(out, k)
	}
	return out, wrap(op, rows.Err())
}

// ---------------------------------------------------------------------------
// uid leases
// ---------------------------------------------------------------------------

type uidLeaseRepo struct{ s *Store }

// Record appends the audit row for a granted block.
//
// There is no Get-by-block, no Release and no Delete, and the absence is the
// design: nothing may read this table in order to decide what to allocate.
// The cursor is the only allocator.
func (r uidLeaseRepo) Record(ctx context.Context, tenant Tenant, l UIDLease) error {
	const op = "store.UIDLeases.Record"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if l.LeaseID == "" || l.TargetID == "" {
		return invalid(op, "lease id and target id are required")
	}
	if l.To <= l.From {
		return invalid(op, "a recorded block must be non-empty")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO uid_leases
			(tenant, lease_id, target_id, proxy_id, uid_from, uid_to, term_seconds, granted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		tenant, l.LeaseID, l.TargetID, l.ProxyID, l.From, l.To, l.TermSeconds, l.GrantedAt)
	return wrap(op, err)
}

// Get resolves a lease id, which is the incident query: a uid came from a
// block, and a block came from a proxy.
func (r uidLeaseRepo) Get(ctx context.Context, tenant Tenant, leaseID string) (UIDLease, error) {
	const op = "store.UIDLeases.Get"
	if err := checkTenant(op, tenant); err != nil {
		return UIDLease{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var l UIDLease
	err := r.s.db.QueryRow(ctx, `
		SELECT lease_id, target_id, proxy_id, uid_from, uid_to, term_seconds, granted_at
		FROM uid_leases
		WHERE tenant = $1 AND lease_id = $2`, tenant, leaseID).
		Scan(&l.LeaseID, &l.TargetID, &l.ProxyID, &l.From, &l.To, &l.TermSeconds, &l.GrantedAt)
	if err != nil {
		return UIDLease{}, wrap(op, err)
	}
	return l, nil
}

// ListForTarget returns a target's grants, lowest block first. It is the other
// half of the incident query — "who else holds uids on this host" — and the
// assertion a test makes about non-overlap.
func (r uidLeaseRepo) ListForTarget(ctx context.Context, tenant Tenant, targetID string) ([]UIDLease, error) {
	const op = "store.UIDLeases.ListForTarget"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT lease_id, target_id, proxy_id, uid_from, uid_to, term_seconds, granted_at
		FROM uid_leases
		WHERE tenant = $1 AND target_id = $2
		ORDER BY uid_from`, tenant, targetID)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []UIDLease
	for rows.Next() {
		var l UIDLease
		if err := rows.Scan(&l.LeaseID, &l.TargetID, &l.ProxyID, &l.From, &l.To,
			&l.TermSeconds, &l.GrantedAt); err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, l)
	}
	return out, wrap(op, rows.Err())
}

// ---------------------------------------------------------------------------
// proxy API tokens
// ---------------------------------------------------------------------------

type proxyTokenRepo struct{ s *Store }

const proxyTokenColumns = `token_id, proxy_id, token_hash, label, issued_at, expires_at, revoked_at`

func (r proxyTokenRepo) Insert(ctx context.Context, tenant Tenant, t ProxyAPIToken) error {
	const op = "store.ProxyTokens.Insert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if t.TokenID == "" || len(t.TokenHash) == 0 {
		return invalid(op, "token id and hash are required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO proxy_api_tokens
			(tenant, token_id, proxy_id, token_hash, label, issued_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, COALESCE($6, now()), $7)`,
		tenant, t.TokenID, t.ProxyID, t.TokenHash, t.Label,
		nullableTime(t.IssuedAt), nullableTime(t.ExpiresAt))
	return wrap(op, err)
}

// GetByHash is the middleware's lookup. It takes the hash rather than the
// secret so that no repository method has ever held a usable credential.
func (r proxyTokenRepo) GetByHash(ctx context.Context, tenant Tenant, hash []byte) (ProxyAPIToken, error) {
	const op = "store.ProxyTokens.GetByHash"
	if err := checkTenant(op, tenant); err != nil {
		return ProxyAPIToken{}, err
	}
	if len(hash) == 0 {
		return ProxyAPIToken{}, invalid(op, "hash is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+proxyTokenColumns+`
		FROM proxy_api_tokens
		WHERE tenant = $1 AND token_hash = $2`, tenant, hash)
	return scanProxyToken(op, row)
}

func (r proxyTokenRepo) Revoke(ctx context.Context, tenant Tenant, tokenID string, at time.Time) error {
	const op = "store.ProxyTokens.Revoke"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		UPDATE proxy_api_tokens
		SET revoked_at = COALESCE(revoked_at, $3)
		WHERE tenant = $1 AND token_id = $2`, tenant, tokenID, at)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func (r proxyTokenRepo) ListByProxy(ctx context.Context, tenant Tenant, proxyID string) ([]ProxyAPIToken, error) {
	const op = "store.ProxyTokens.ListByProxy"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+proxyTokenColumns+`
		FROM proxy_api_tokens
		WHERE tenant = $1 AND proxy_id = $2
		ORDER BY issued_at, token_id`, tenant, proxyID)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []ProxyAPIToken
	for rows.Next() {
		t, err := scanProxyToken(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, wrap(op, rows.Err())
}

func scanProxyToken(op string, row rowScanner) (ProxyAPIToken, error) {
	var (
		t                ProxyAPIToken
		expires, revoked *time.Time
	)
	err := row.Scan(&t.TokenID, &t.ProxyID, &t.TokenHash, &t.Label, &t.IssuedAt, &expires, &revoked)
	if err != nil {
		return ProxyAPIToken{}, wrap(op, err)
	}
	t.ExpiresAt, t.RevokedAt = timeOrZero(expires), timeOrZero(revoked)
	return t, nil
}

// ---------------------------------------------------------------------------
// the fleet's own keys
// ---------------------------------------------------------------------------

// GetByKeyFingerprint answers "is this key one of ours" — the chain-leg
// question on /v1/auth/cert (proxy D11).
//
// It reads the fleet registry's OWN rows through the generated
// `key_fingerprint` column rather than a list of proxy keys maintained beside
// them, because two answers to one question drift the first time a proxy
// re-enrolls with a new key.
func (r proxyRepo) GetByKeyFingerprint(ctx context.Context, tenant Tenant, fingerprint string) (Proxy, error) {
	const op = "store.Proxies.GetByKeyFingerprint"
	if err := checkTenant(op, tenant); err != nil {
		return Proxy{}, err
	}
	if fingerprint == "" {
		// An empty fingerprint would match the NULL guard in the index and
		// nothing else, but refusing it here says why rather than answering
		// "not found" to a caller that asked a malformed question.
		return Proxy{}, invalid(op, "fingerprint is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+proxyColumns+`
		FROM proxies
		WHERE tenant = $1 AND key_fingerprint = $2`, tenant, fingerprint)
	return scanProxy(op, row)
}
