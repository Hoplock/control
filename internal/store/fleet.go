// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"time"
)

// ProxyHealthReport is what one heartbeat carries.
//
// ContractVersion and DeclaredCapabilities are pointers because "the report did
// not mention it" and "the report said none" are different facts: a heartbeat
// that says nothing about capabilities has not withdrawn them, and a proxy that
// declares an empty set has. A zero int and a nil map cannot tell the two apart.
type ProxyHealthReport struct {
	// ProxyID is who reported.
	ProxyID string
	// At is when. Required: a heartbeat with no time cannot age.
	At time.Time
	// SessionCount is how many sessions the proxy currently holds. Only the
	// proxy can count them — it holds the session registry.
	SessionCount int
	// LastError is the last error the proxy reported, empty for none. An
	// empty value leaves the stored error's timestamp alone rather than
	// claiming the error just happened.
	LastError string
	// ContractVersion, when named, updates the enrolled vocabulary.
	ContractVersion *int
	// DeclaredCapabilities, when named, replaces the declared set.
	DeclaredCapabilities json.RawMessage
}

// nullableInt maps a nil pointer onto SQL NULL, so a COALESCE keeps the stored
// value. "The report did not mention it" is not "the report said zero".
func nullableInt(v *int) *int { return v }

// nullableJSON maps absent JSON onto SQL NULL, for the same reason. It differs
// from nonNilJSON, which writes `{}`: here the column must be left alone, and
// `{}` would overwrite a real capability set with an empty one.
func nullableJSON(v json.RawMessage) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

// ---------------------------------------------------------------------------
// proxy enrollments
// ---------------------------------------------------------------------------

type proxyEnrollmentRepo struct{ s *Store }

const proxyEnrollmentColumns = `proxy_id, granted_zones, token_hash, expires_at, consumed_at, created_by, created_at`

func (r proxyEnrollmentRepo) Create(ctx context.Context, tenant Tenant, e ProxyEnrollment) error {
	const op = "store.ProxyEnrollments.Create"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if e.ProxyID == "" {
		return invalid(op, "proxy id is required")
	}
	if len(e.TokenHash) == 0 {
		return invalid(op, "token hash is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO proxy_enrollments (tenant, proxy_id, granted_zones, token_hash, expires_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		tenant, e.ProxyID, nonNilStrings(e.GrantedZones), e.TokenHash,
		nullableTime(e.ExpiresAt), e.CreatedBy)
	return wrap(op, err)
}

func (r proxyEnrollmentRepo) Get(ctx context.Context, tenant Tenant, proxyID string) (ProxyEnrollment, error) {
	const op = "store.ProxyEnrollments.Get"
	if err := checkTenant(op, tenant); err != nil {
		return ProxyEnrollment{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+proxyEnrollmentColumns+`
		FROM proxy_enrollments
		WHERE tenant = $1 AND proxy_id = $2`, tenant, proxyID)
	return scanProxyEnrollment(op, row)
}

func (r proxyEnrollmentRepo) Consume(ctx context.Context, tenant Tenant, proxyID string, at time.Time) error {
	const op = "store.ProxyEnrollments.Consume"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if at.IsZero() {
		return invalid(op, "consumption time is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// The `consumed_at IS NULL` predicate is the whole guarantee: the check
	// and the write are one statement, so two enrollments racing on one token
	// cannot both win. Doing it as a read then a write in Go would make the
	// token single-use only when nobody was in a hurry.
	tag, err := r.s.db.Exec(ctx, `
		UPDATE proxy_enrollments
		SET consumed_at = $3
		WHERE tenant = $1 AND proxy_id = $2 AND consumed_at IS NULL`,
		tenant, proxyID, at)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		// Absent and already-consumed are told apart, because they are
		// different operator problems: one is "no grant was issued", the
		// other is "the token was already used".
		var exists bool
		if err := r.s.db.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM proxy_enrollments WHERE tenant = $1 AND proxy_id = $2
			)`, tenant, proxyID).Scan(&exists); err != nil {
			return wrap(op, err)
		}
		if exists {
			return &Error{Op: op, Kind: KindConflict, Err: errEnrollmentConsumed}
		}
		return notFound(op)
	}
	return nil
}

func (r proxyEnrollmentRepo) Delete(ctx context.Context, tenant Tenant, proxyID string) error {
	const op = "store.ProxyEnrollments.Delete"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		DELETE FROM proxy_enrollments WHERE tenant = $1 AND proxy_id = $2`, tenant, proxyID)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func scanProxyEnrollment(op string, row rowScanner) (ProxyEnrollment, error) {
	var (
		e        ProxyEnrollment
		expires  *time.Time
		consumed *time.Time
	)
	if err := row.Scan(&e.ProxyID, &e.GrantedZones, &e.TokenHash, &expires, &consumed,
		&e.CreatedBy, &e.CreatedAt); err != nil {
		return ProxyEnrollment{}, wrap(op, err)
	}
	e.ExpiresAt = timeOrZero(expires)
	e.ConsumedAt = timeOrZero(consumed)
	return e, nil
}

// ---------------------------------------------------------------------------
// proxy edges
// ---------------------------------------------------------------------------

type proxyEdgeRepo struct{ s *Store }

const proxyEdgeColumns = `proxy_id, to_zone, direction, address, next_proxy_id, cost, created_at, updated_at`

func (r proxyEdgeRepo) List(ctx context.Context, tenant Tenant) ([]ProxyEdge, error) {
	const op = "store.ProxyEdges.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+proxyEdgeColumns+`
		FROM proxy_edges
		WHERE tenant = $1
		ORDER BY proxy_id, to_zone, direction, next_proxy_id`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []ProxyEdge
	for rows.Next() {
		e, err := scanProxyEdge(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, wrap(op, rows.Err())
}

func (r proxyEdgeRepo) ReplaceForProxy(ctx context.Context, tenant Tenant, proxyID string, edges []ProxyEdge) error {
	const op = "store.ProxyEdges.ReplaceForProxy"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if proxyID == "" {
		return invalid(op, "proxy id is required")
	}
	for _, e := range edges {
		if e.ToZone == "" {
			return invalid(op, "edge zone is required")
		}
		if e.Direction != HopDial && e.Direction != HopRelay {
			return invalid(op, "edge direction must be dial or relay")
		}
		if e.Cost <= 0 {
			return invalid(op, "edge cost must be positive")
		}
		if e.Direction == HopDial && e.Address == "" && e.NextProxyID == "" {
			return invalid(op, "a dial edge needs an address or a pinned next proxy")
		}
	}

	// Delete-then-insert in one transaction: a declaration is the whole set,
	// and two statements outside a transaction leave a window where the proxy
	// reaches nothing.
	run := func(ctx context.Context, tx *Store) error {
		if _, err := tx.db.Exec(ctx, `
			DELETE FROM proxy_edges WHERE tenant = $1 AND proxy_id = $2`, tenant, proxyID); err != nil {
			return wrap(op, err)
		}
		for _, e := range edges {
			if _, err := tx.db.Exec(ctx, `
				INSERT INTO proxy_edges (tenant, proxy_id, to_zone, direction, address, next_proxy_id, cost)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				tenant, proxyID, e.ToZone, string(e.Direction), e.Address, e.NextProxyID, e.Cost); err != nil {
				return wrap(op, err)
			}
		}
		return nil
	}

	if r.s.pool == nil {
		// Already inside a transaction: use it rather than refusing.
		return run(ctx, r.s)
	}
	return r.s.InTx(ctx, run)
}

func scanProxyEdge(op string, row rowScanner) (ProxyEdge, error) {
	var (
		e         ProxyEdge
		direction string
	)
	if err := row.Scan(&e.ProxyID, &e.ToZone, &direction, &e.Address, &e.NextProxyID,
		&e.Cost, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return ProxyEdge{}, wrap(op, err)
	}
	e.Direction = HopDirection(direction)
	return e, nil
}

// ---------------------------------------------------------------------------
// relay registrations
// ---------------------------------------------------------------------------

type relayRegistrationRepo struct{ s *Store }

func (r relayRegistrationRepo) List(ctx context.Context, tenant Tenant) ([]RelayRegistration, error) {
	const op = "store.RelayRegistrations.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT upstream_proxy_id, downstream_proxy_id, registered_at, last_seen_at
		FROM relay_registrations
		WHERE tenant = $1
		ORDER BY upstream_proxy_id, downstream_proxy_id`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []RelayRegistration
	for rows.Next() {
		var reg RelayRegistration
		if err := rows.Scan(&reg.UpstreamProxyID, &reg.DownstreamProxyID,
			&reg.RegisteredAt, &reg.LastSeenAt); err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, reg)
	}
	return out, wrap(op, rows.Err())
}

func (r relayRegistrationRepo) ReplaceForUpstream(ctx context.Context, tenant Tenant, upstreamProxyID string, downstream []string, at time.Time) error {
	const op = "store.RelayRegistrations.ReplaceForUpstream"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if upstreamProxyID == "" {
		return invalid(op, "upstream proxy id is required")
	}
	if at.IsZero() {
		return invalid(op, "report time is required")
	}

	run := func(ctx context.Context, tx *Store) error {
		// Registrations this upstream no longer reports are gone. A relay
		// edge to a dropped registration must fall out of routing at once:
		// the alternative is a route that hangs, which the user experiences
		// as an outage nobody chose.
		if _, err := tx.db.Exec(ctx, `
			DELETE FROM relay_registrations
			WHERE tenant = $1 AND upstream_proxy_id = $2
			  AND NOT (downstream_proxy_id = ANY($3))`,
			tenant, upstreamProxyID, nonNilStrings(downstream)); err != nil {
			return wrap(op, err)
		}
		for _, d := range downstream {
			if d == "" {
				return invalid(op, "downstream proxy id is required")
			}
			// registered_at survives a re-report: an operator wants to know
			// how long the link has been up, not when it was last confirmed.
			if _, err := tx.db.Exec(ctx, `
				INSERT INTO relay_registrations (tenant, upstream_proxy_id, downstream_proxy_id, registered_at, last_seen_at)
				VALUES ($1, $2, $3, $4, $4)
				ON CONFLICT (tenant, upstream_proxy_id, downstream_proxy_id) DO UPDATE SET
					last_seen_at = EXCLUDED.last_seen_at`,
				tenant, upstreamProxyID, d, at); err != nil {
				return wrap(op, err)
			}
		}
		return nil
	}

	if r.s.pool == nil {
		return run(ctx, r.s)
	}
	return r.s.InTx(ctx, run)
}

// ---------------------------------------------------------------------------
// configuration
// ---------------------------------------------------------------------------

type proxyConfigRepo struct{ s *Store }

func (r proxyConfigRepo) InsertVersion(ctx context.Context, tenant Tenant, v ProxyConfigVersion) error {
	const op = "store.ProxyConfigs.InsertVersion"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if err := checkScope(op, v.Scope); err != nil {
		return err
	}
	if v.Version <= 0 {
		return invalid(op, "config version must be positive")
	}
	if len(v.Document) == 0 {
		return invalid(op, "config document is required")
	}
	if v.Hash == "" {
		return invalid(op, "config hash is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO proxy_configs (tenant, scope_kind, scope_id, version, document, hash, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		tenant, string(v.Scope.Kind), v.Scope.ID, v.Version, string(v.Document), v.Hash, v.CreatedBy)
	return wrap(op, err)
}

func (r proxyConfigRepo) GetVersion(ctx context.Context, tenant Tenant, scope ConfigScope, version int64) (ProxyConfigVersion, error) {
	const op = "store.ProxyConfigs.GetVersion"
	if err := checkTenant(op, tenant); err != nil {
		return ProxyConfigVersion{}, err
	}
	if err := checkScope(op, scope); err != nil {
		return ProxyConfigVersion{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var (
		v   ProxyConfigVersion
		doc string
	)
	v.Scope = scope
	err := r.s.db.QueryRow(ctx, `
		SELECT version, document, hash, created_by, created_at
		FROM proxy_configs
		WHERE tenant = $1 AND scope_kind = $2 AND scope_id = $3 AND version = $4`,
		tenant, string(scope.Kind), scope.ID, version,
	).Scan(&v.Version, &doc, &v.Hash, &v.CreatedBy, &v.CreatedAt)
	if err != nil {
		return ProxyConfigVersion{}, wrap(op, err)
	}
	v.Document = json.RawMessage(doc)
	return v, nil
}

func (r proxyConfigRepo) NextVersion(ctx context.Context, tenant Tenant, scope ConfigScope) (int64, error) {
	const op = "store.ProxyConfigs.NextVersion"
	if err := checkTenant(op, tenant); err != nil {
		return 0, err
	}
	if err := checkScope(op, scope); err != nil {
		return 0, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var next int64
	err := r.s.db.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM proxy_configs
		WHERE tenant = $1 AND scope_kind = $2 AND scope_id = $3`,
		tenant, string(scope.Kind), scope.ID).Scan(&next)
	if err != nil {
		return 0, wrap(op, err)
	}
	return next, nil
}

func (r proxyConfigRepo) SetDesired(ctx context.Context, tenant Tenant, scope ConfigScope, version int64, publishedBy string, at time.Time) error {
	const op = "store.ProxyConfigs.SetDesired"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if err := checkScope(op, scope); err != nil {
		return err
	}
	if version <= 0 {
		return invalid(op, "config version must be positive")
	}
	if at.IsZero() {
		return invalid(op, "publication time is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// previous_version is the version being displaced, which is what a
	// rollback republishes. Re-publishing the version already desired leaves
	// it alone: a no-op publish must not erase the rollback target.
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO proxy_config_desired (tenant, scope_kind, scope_id, version, previous_version, published_by, published_at)
		VALUES ($1, $2, $3, $4, 0, $5, $6)
		ON CONFLICT (tenant, scope_kind, scope_id) DO UPDATE SET
			version          = EXCLUDED.version,
			previous_version = CASE
				WHEN proxy_config_desired.version = EXCLUDED.version
					THEN proxy_config_desired.previous_version
				ELSE proxy_config_desired.version
			END,
			published_by     = EXCLUDED.published_by,
			published_at     = EXCLUDED.published_at`,
		tenant, string(scope.Kind), scope.ID, version, publishedBy, at)
	return wrap(op, err)
}

func (r proxyConfigRepo) GetDesired(ctx context.Context, tenant Tenant, scope ConfigScope) (ProxyConfigDesired, error) {
	const op = "store.ProxyConfigs.GetDesired"
	if err := checkTenant(op, tenant); err != nil {
		return ProxyConfigDesired{}, err
	}
	if err := checkScope(op, scope); err != nil {
		return ProxyConfigDesired{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	d := ProxyConfigDesired{Scope: scope}
	err := r.s.db.QueryRow(ctx, `
		SELECT version, previous_version, published_by, published_at
		FROM proxy_config_desired
		WHERE tenant = $1 AND scope_kind = $2 AND scope_id = $3`,
		tenant, string(scope.Kind), scope.ID,
	).Scan(&d.Version, &d.PreviousVersion, &d.PublishedBy, &d.PublishedAt)
	if err != nil {
		return ProxyConfigDesired{}, wrap(op, err)
	}
	return d, nil
}

func (r proxyConfigRepo) PutState(ctx context.Context, tenant Tenant, s ProxyConfigState) error {
	const op = "store.ProxyConfigs.PutState"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if s.ProxyID == "" {
		return invalid(op, "proxy id is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// The running columns are untouched: what a proxy reported is its fact,
	// and a publish does not change what is out there.
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO proxy_config_state (tenant, proxy_id, desired_version, desired_hash, desired_document, desired_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant, proxy_id) DO UPDATE SET
			desired_version  = EXCLUDED.desired_version,
			desired_hash     = EXCLUDED.desired_hash,
			desired_document = EXCLUDED.desired_document,
			desired_at       = EXCLUDED.desired_at`,
		tenant, s.ProxyID, s.DesiredVersion, s.DesiredHash,
		string(nonNilJSON(s.DesiredDocument)), nullableTime(s.DesiredAt))
	return wrap(op, err)
}

func (r proxyConfigRepo) GetState(ctx context.Context, tenant Tenant, proxyID string) (ProxyConfigState, error) {
	const op = "store.ProxyConfigs.GetState"
	if err := checkTenant(op, tenant); err != nil {
		return ProxyConfigState{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT proxy_id, desired_version, desired_hash, desired_document, desired_at,
		       running_version, running_hash, reported_at
		FROM proxy_config_state
		WHERE tenant = $1 AND proxy_id = $2`, tenant, proxyID)
	return scanProxyConfigState(op, row)
}

func (r proxyConfigRepo) ListStates(ctx context.Context, tenant Tenant) ([]ProxyConfigState, error) {
	const op = "store.ProxyConfigs.ListStates"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT proxy_id, desired_version, desired_hash, desired_document, desired_at,
		       running_version, running_hash, reported_at
		FROM proxy_config_state
		WHERE tenant = $1
		ORDER BY proxy_id`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []ProxyConfigState
	for rows.Next() {
		s, err := scanProxyConfigState(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, wrap(op, rows.Err())
}

func (r proxyConfigRepo) ReportRunning(ctx context.Context, tenant Tenant, proxyID string, version int64, hash string, at time.Time) error {
	const op = "store.ProxyConfigs.ReportRunning"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if proxyID == "" {
		return invalid(op, "proxy id is required")
	}
	if at.IsZero() {
		return invalid(op, "report time is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		UPDATE proxy_config_state
		SET running_version = $3, running_hash = $4, reported_at = $5
		WHERE tenant = $1 AND proxy_id = $2`,
		tenant, proxyID, version, hash, at)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func scanProxyConfigState(op string, row rowScanner) (ProxyConfigState, error) {
	var (
		s          ProxyConfigState
		doc        string
		desiredAt  *time.Time
		reportedAt *time.Time
	)
	if err := row.Scan(&s.ProxyID, &s.DesiredVersion, &s.DesiredHash, &doc, &desiredAt,
		&s.RunningVersion, &s.RunningHash, &reportedAt); err != nil {
		return ProxyConfigState{}, wrap(op, err)
	}
	s.DesiredDocument = json.RawMessage(doc)
	s.DesiredAt = timeOrZero(desiredAt)
	s.ReportedAt = timeOrZero(reportedAt)
	return s, nil
}

// checkScope rejects a scope the schema cannot store.
func checkScope(op string, scope ConfigScope) error {
	if scope.Kind != ConfigScopeZone && scope.Kind != ConfigScopeProxy {
		return invalid(op, "config scope kind must be zone or proxy")
	}
	if scope.ID == "" {
		return invalid(op, "config scope id is required")
	}
	return nil
}

// ---------------------------------------------------------------------------
// target capabilities
// ---------------------------------------------------------------------------

type targetCapabilityRepo struct{ s *Store }

const targetCapabilityColumns = `hostname, target_port, platform, execution, reach, observed_at, detail, reported_by, received_at`

func (r targetCapabilityRepo) Put(ctx context.Context, tenant Tenant, rec TargetCapabilityRecord) error {
	const op = "store.TargetCapabilities.Put"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if rec.Hostname == "" {
		return invalid(op, "hostname is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// ObservedAt is written as NULL when zero rather than defaulted to now().
	// A record with no observation time is stale by definition, and inventing
	// one here is exactly the fail-open M17 forbids.
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO target_capabilities (tenant, hostname, target_port, platform,
			execution, reach, observed_at, detail, reported_by, received_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, COALESCE($10, now()))
		ON CONFLICT (tenant, hostname, target_port, platform) DO UPDATE SET
			execution   = EXCLUDED.execution,
			reach       = EXCLUDED.reach,
			observed_at = EXCLUDED.observed_at,
			detail      = EXCLUDED.detail,
			reported_by = EXCLUDED.reported_by,
			received_at = EXCLUDED.received_at`,
		tenant, rec.Hostname, rec.Port, rec.Platform,
		nonNilStrings(rec.Execution), nonNilStrings(rec.Reach),
		nullableTime(rec.ObservedAt), nonNilMap(rec.Detail), rec.ReportedBy,
		nullableTime(rec.ReceivedAt))
	return wrap(op, err)
}

func (r targetCapabilityRepo) Get(ctx context.Context, tenant Tenant, hostname string, port int32, platform string) (TargetCapabilityRecord, error) {
	const op = "store.TargetCapabilities.Get"
	if err := checkTenant(op, tenant); err != nil {
		return TargetCapabilityRecord{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+targetCapabilityColumns+`
		FROM target_capabilities
		WHERE tenant = $1 AND hostname = $2 AND target_port = $3 AND platform = $4`,
		tenant, hostname, port, platform)
	return scanTargetCapability(op, row)
}

func (r targetCapabilityRepo) List(ctx context.Context, tenant Tenant) ([]TargetCapabilityRecord, error) {
	const op = "store.TargetCapabilities.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+targetCapabilityColumns+`
		FROM target_capabilities
		WHERE tenant = $1
		ORDER BY hostname, target_port, platform`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []TargetCapabilityRecord
	for rows.Next() {
		rec, err := scanTargetCapability(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, wrap(op, rows.Err())
}

func scanTargetCapability(op string, row rowScanner) (TargetCapabilityRecord, error) {
	var (
		rec      TargetCapabilityRecord
		observed *time.Time
	)
	if err := row.Scan(&rec.Hostname, &rec.Port, &rec.Platform, &rec.Execution, &rec.Reach,
		&observed, &rec.Detail, &rec.ReportedBy, &rec.ReceivedAt); err != nil {
		return TargetCapabilityRecord{}, wrap(op, err)
	}
	rec.ObservedAt = timeOrZero(observed)
	return rec, nil
}
