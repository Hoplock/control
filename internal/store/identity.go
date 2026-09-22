// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"time"
)

// The repositories federation, RBAC and the SSH CA read and write (0011).
//
// Every method names its tenant, like every other one in this package (M18),
// and absence is ErrNotFound rather than a zero value: a credential lookup
// that cannot tell "no such token" from "the database did not answer" is M11
// waiting to happen one layer up.

// ---------------------------------------------------------------------------
// groups
// ---------------------------------------------------------------------------

// GroupRepository stores groups and local membership (M7).
type GroupRepository interface {
	// Get returns a group. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, groupID string) (Group, error)
	// List returns every group in the tenant, in id order.
	List(ctx context.Context, tenant Tenant) ([]Group, error)
	// Upsert writes a group.
	Upsert(ctx context.Context, tenant Tenant, g Group) error
	// Delete removes a group and its local membership. Absent is
	// ErrNotFound.
	Delete(ctx context.Context, tenant Tenant, groupID string) error
	// AddMember records local membership. It is idempotent.
	AddMember(ctx context.Context, tenant Tenant, groupID, subjectID string) error
	// RemoveMember drops local membership. Absent is ErrNotFound.
	RemoveMember(ctx context.Context, tenant Tenant, groupID, subjectID string) error
	// GroupsOf returns the local groups a subject belongs to, in id order.
	// IdP-asserted membership is not here: it is computed at login by the
	// claim mapping, because a membership this server did not decide is not
	// one it may keep after the assertion that carried it has gone.
	GroupsOf(ctx context.Context, tenant Tenant, subjectID string) ([]string, error)
}

type groupRepo struct{ s *Store }

const groupColumns = `group_id, display_name, description, source, created_at, updated_at`

func (r groupRepo) Get(ctx context.Context, tenant Tenant, groupID string) (Group, error) {
	const op = "store.Groups.Get"
	if err := checkTenant(op, tenant); err != nil {
		return Group{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+groupColumns+`
		FROM identity_groups
		WHERE tenant = $1 AND group_id = $2`, tenant, groupID)
	return scanGroup(op, row)
}

func (r groupRepo) List(ctx context.Context, tenant Tenant) ([]Group, error) {
	const op = "store.Groups.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+groupColumns+`
		FROM identity_groups
		WHERE tenant = $1
		ORDER BY group_id`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []Group
	for rows.Next() {
		g, err := scanGroup(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, wrap(op, rows.Err())
}

func (r groupRepo) Upsert(ctx context.Context, tenant Tenant, g Group) error {
	const op = "store.Groups.Upsert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if g.ID == "" {
		return invalid(op, "group id is required")
	}
	if g.Source == "" {
		g.Source = "local"
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO identity_groups (tenant, group_id, display_name, description, source)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant, group_id) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			description  = EXCLUDED.description,
			source       = EXCLUDED.source,
			updated_at   = now()`,
		tenant, g.ID, g.DisplayName, g.Description, g.Source)
	return wrap(op, err)
}

func (r groupRepo) Delete(ctx context.Context, tenant Tenant, groupID string) error {
	const op = "store.Groups.Delete"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		DELETE FROM identity_groups WHERE tenant = $1 AND group_id = $2`, tenant, groupID)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func (r groupRepo) AddMember(ctx context.Context, tenant Tenant, groupID, subjectID string) error {
	const op = "store.Groups.AddMember"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if groupID == "" || subjectID == "" {
		return invalid(op, "group id and subject id are required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO identity_group_members (tenant, group_id, subject_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant, group_id, subject_id) DO NOTHING`, tenant, groupID, subjectID)
	return wrap(op, err)
}

func (r groupRepo) RemoveMember(ctx context.Context, tenant Tenant, groupID, subjectID string) error {
	const op = "store.Groups.RemoveMember"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		DELETE FROM identity_group_members
		WHERE tenant = $1 AND group_id = $2 AND subject_id = $3`, tenant, groupID, subjectID)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func (r groupRepo) GroupsOf(ctx context.Context, tenant Tenant, subjectID string) ([]string, error) {
	const op = "store.Groups.GroupsOf"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT group_id
		FROM identity_group_members
		WHERE tenant = $1 AND subject_id = $2
		ORDER BY group_id`, tenant, subjectID)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, id)
	}
	return out, wrap(op, rows.Err())
}

// ---------------------------------------------------------------------------
// role bindings
// ---------------------------------------------------------------------------

// RoleBindingRepository stores RBAC grants, per tenant and only per tenant.
type RoleBindingRepository interface {
	// List returns every binding in the tenant.
	List(ctx context.Context, tenant Tenant) ([]RoleBinding, error)
	// RolesFor returns the role codes a subject holds in the tenant,
	// including those it holds through the given groups. The groups are
	// passed in rather than looked up because the caller has just resolved
	// them — from local membership, from a mapped assertion, or from both —
	// and a second source of truth here would answer differently for a
	// federated login than for a local one.
	RolesFor(ctx context.Context, tenant Tenant, subjectID string, groups []string) ([]string, error)
	// Bind grants a role. It is idempotent.
	Bind(ctx context.Context, tenant Tenant, b RoleBinding) error
	// Unbind removes a binding. Absent is ErrNotFound.
	Unbind(ctx context.Context, tenant Tenant, b RoleBinding) error
}

type roleBindingRepo struct{ s *Store }

func (r roleBindingRepo) List(ctx context.Context, tenant Tenant) ([]RoleBinding, error) {
	const op = "store.RoleBindings.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT subject_id, group_id, role, granted_by, created_at
		FROM identity_role_bindings
		WHERE tenant = $1
		ORDER BY role, subject_id NULLS LAST, group_id NULLS LAST`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []RoleBinding
	for rows.Next() {
		var (
			b       RoleBinding
			subject *string
			group   *string
		)
		if err := rows.Scan(&subject, &group, &b.Role, &b.GrantedBy, &b.CreatedAt); err != nil {
			return nil, wrap(op, err)
		}
		if subject != nil {
			b.SubjectID = *subject
		}
		if group != nil {
			b.GroupID = *group
		}
		out = append(out, b)
	}
	return out, wrap(op, rows.Err())
}

func (r roleBindingRepo) RolesFor(ctx context.Context, tenant Tenant, subjectID string, groups []string) ([]string, error) {
	const op = "store.RoleBindings.RolesFor"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT DISTINCT role
		FROM identity_role_bindings
		WHERE tenant = $1
		  AND (subject_id = $2 OR group_id = ANY($3))
		ORDER BY role`, tenant, subjectID, nonNilStrings(groups))
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, role)
	}
	return out, wrap(op, rows.Err())
}

func (r roleBindingRepo) Bind(ctx context.Context, tenant Tenant, b RoleBinding) error {
	const op = "store.RoleBindings.Bind"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if (b.SubjectID == "") == (b.GroupID == "") {
		return invalid(op, "exactly one of subject id and group id is required")
	}
	if b.Role == "" {
		return invalid(op, "role is required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var err error
	if b.SubjectID != "" {
		_, err = r.s.db.Exec(ctx, `
			INSERT INTO identity_role_bindings (tenant, subject_id, role, granted_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant, subject_id, role) WHERE subject_id IS NOT NULL
			DO UPDATE SET granted_by = EXCLUDED.granted_by`,
			tenant, b.SubjectID, b.Role, b.GrantedBy)
	} else {
		_, err = r.s.db.Exec(ctx, `
			INSERT INTO identity_role_bindings (tenant, group_id, role, granted_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant, group_id, role) WHERE group_id IS NOT NULL
			DO UPDATE SET granted_by = EXCLUDED.granted_by`,
			tenant, b.GroupID, b.Role, b.GrantedBy)
	}
	return wrap(op, err)
}

func (r roleBindingRepo) Unbind(ctx context.Context, tenant Tenant, b RoleBinding) error {
	const op = "store.RoleBindings.Unbind"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if (b.SubjectID == "") == (b.GroupID == "") {
		return invalid(op, "exactly one of subject id and group id is required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var (
		tag interface{ RowsAffected() int64 }
		err error
	)
	if b.SubjectID != "" {
		tag, err = r.s.db.Exec(ctx, `
			DELETE FROM identity_role_bindings
			WHERE tenant = $1 AND subject_id = $2 AND role = $3`, tenant, b.SubjectID, b.Role)
	} else {
		tag, err = r.s.db.Exec(ctx, `
			DELETE FROM identity_role_bindings
			WHERE tenant = $1 AND group_id = $2 AND role = $3`, tenant, b.GroupID, b.Role)
	}
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

// ---------------------------------------------------------------------------
// connectors
// ---------------------------------------------------------------------------

// ConnectorRepository stores per-tenant federation configuration (M18).
type ConnectorRepository interface {
	// Get returns a connector. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, name string) (Connector, error)
	// List returns every connector in the tenant, in name order.
	List(ctx context.Context, tenant Tenant) ([]Connector, error)
	// Upsert writes a connector.
	Upsert(ctx context.Context, tenant Tenant, c Connector) error
	// Delete removes one. Absent is ErrNotFound.
	Delete(ctx context.Context, tenant Tenant, name string) error
}

type connectorRepo struct{ s *Store }

const connectorColumns = `connector, kind, display_name, enabled, config, created_at, updated_at`

func (r connectorRepo) Get(ctx context.Context, tenant Tenant, name string) (Connector, error) {
	const op = "store.Connectors.Get"
	if err := checkTenant(op, tenant); err != nil {
		return Connector{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+connectorColumns+`
		FROM idp_connectors
		WHERE tenant = $1 AND connector = $2`, tenant, name)
	return scanConnector(op, row)
}

func (r connectorRepo) List(ctx context.Context, tenant Tenant) ([]Connector, error) {
	const op = "store.Connectors.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+connectorColumns+`
		FROM idp_connectors
		WHERE tenant = $1
		ORDER BY connector`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []Connector
	for rows.Next() {
		c, err := scanConnector(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, wrap(op, rows.Err())
}

func (r connectorRepo) Upsert(ctx context.Context, tenant Tenant, c Connector) error {
	const op = "store.Connectors.Upsert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if c.Name == "" || c.Kind == "" {
		return invalid(op, "connector name and kind are required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO idp_connectors (tenant, connector, kind, display_name, enabled, config)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant, connector) DO UPDATE SET
			kind         = EXCLUDED.kind,
			display_name = EXCLUDED.display_name,
			enabled      = EXCLUDED.enabled,
			config       = EXCLUDED.config,
			updated_at   = now()`,
		tenant, c.Name, string(c.Kind), c.DisplayName, c.Enabled, nonNilJSON(c.Config))
	return wrap(op, err)
}

func (r connectorRepo) Delete(ctx context.Context, tenant Tenant, name string) error {
	const op = "store.Connectors.Delete"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		DELETE FROM idp_connectors WHERE tenant = $1 AND connector = $2`, tenant, name)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

// ---------------------------------------------------------------------------
// claim mappings
// ---------------------------------------------------------------------------

// ClaimMappingRepository stores the versioned claim mapping (M7, M4).
type ClaimMappingRepository interface {
	// Active returns the version logins use. Absent is ErrNotFound, which
	// means this tenant federates with nobody yet.
	Active(ctx context.Context, tenant Tenant) (ClaimMapping, error)
	// Get returns one version. Absent is ErrNotFound. A decision record
	// names a version, so old versions stay readable forever.
	Get(ctx context.Context, tenant Tenant, version int) (ClaimMapping, error)
	// List returns every version, newest first.
	List(ctx context.Context, tenant Tenant) ([]ClaimMapping, error)
	// Put stores a new version and returns it. The version number is
	// allocated here, atomically, so two operators submitting at once get
	// two versions rather than one overwriting the other.
	Put(ctx context.Context, tenant Tenant, document, digest, createdBy string) (ClaimMapping, error)
	// Activate makes a version the one logins use. Absent is ErrNotFound.
	Activate(ctx context.Context, tenant Tenant, version int) error
}

type claimMappingRepo struct{ s *Store }

const claimMappingColumns = `version, document, digest, active, created_at, created_by`

func (r claimMappingRepo) Active(ctx context.Context, tenant Tenant) (ClaimMapping, error) {
	const op = "store.ClaimMappings.Active"
	if err := checkTenant(op, tenant); err != nil {
		return ClaimMapping{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+claimMappingColumns+`
		FROM claim_mappings
		WHERE tenant = $1 AND active`, tenant)
	return scanClaimMapping(op, row)
}

func (r claimMappingRepo) Get(ctx context.Context, tenant Tenant, version int) (ClaimMapping, error) {
	const op = "store.ClaimMappings.Get"
	if err := checkTenant(op, tenant); err != nil {
		return ClaimMapping{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+claimMappingColumns+`
		FROM claim_mappings
		WHERE tenant = $1 AND version = $2`, tenant, version)
	return scanClaimMapping(op, row)
}

func (r claimMappingRepo) List(ctx context.Context, tenant Tenant) ([]ClaimMapping, error) {
	const op = "store.ClaimMappings.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+claimMappingColumns+`
		FROM claim_mappings
		WHERE tenant = $1
		ORDER BY version DESC`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []ClaimMapping
	for rows.Next() {
		m, err := scanClaimMapping(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, wrap(op, rows.Err())
}

func (r claimMappingRepo) Put(ctx context.Context, tenant Tenant, document, digest, createdBy string) (ClaimMapping, error) {
	const op = "store.ClaimMappings.Put"
	if err := checkTenant(op, tenant); err != nil {
		return ClaimMapping{}, err
	}
	if document == "" || digest == "" {
		return ClaimMapping{}, invalid(op, "document and digest are required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		INSERT INTO claim_mappings (tenant, version, document, digest, created_by)
		SELECT $1, COALESCE(MAX(version), 0) + 1, $2, $3, $4
		FROM claim_mappings WHERE tenant = $1
		RETURNING `+claimMappingColumns, tenant, document, digest, createdBy)
	return scanClaimMapping(op, row)
}

func (r claimMappingRepo) Activate(ctx context.Context, tenant Tenant, version int) error {
	const op = "store.ClaimMappings.Activate"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	return r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		if _, err := q.Exec(ctx, `
			UPDATE claim_mappings SET active = false
			WHERE tenant = $1 AND active`, tenant); err != nil {
			return wrap(op, err)
		}
		tag, err := q.Exec(ctx, `
			UPDATE claim_mappings SET active = true
			WHERE tenant = $1 AND version = $2`, tenant, version)
		if err != nil {
			return wrap(op, err)
		}
		if tag.RowsAffected() == 0 {
			return notFound(op)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// federated identities
// ---------------------------------------------------------------------------

// FederatedIdentityRepository joins an IdP's subject to this server's (M18).
type FederatedIdentityRepository interface {
	// Resolve returns the link for an external subject. Absent is
	// ErrNotFound and means this person has not logged in here before.
	Resolve(ctx context.Context, tenant Tenant, connector, externalSubject string) (FederatedIdentity, error)
	// Link records the join and stamps last_seen_at. It is idempotent, and
	// a link that would move an external subject onto a different local
	// subject is a conflict rather than a silent re-point: that is an
	// account takeover in one UPDATE.
	Link(ctx context.Context, tenant Tenant, f FederatedIdentity) error
	// ListBySubject returns every external identity behind one subject.
	ListBySubject(ctx context.Context, tenant Tenant, subjectID string) ([]FederatedIdentity, error)
}

type federatedIdentityRepo struct{ s *Store }

const federatedIdentityColumns = `connector, external_subject, subject_id, first_seen_at, last_seen_at`

func (r federatedIdentityRepo) Resolve(ctx context.Context, tenant Tenant, connector, externalSubject string) (FederatedIdentity, error) {
	const op = "store.FederatedIdentities.Resolve"
	if err := checkTenant(op, tenant); err != nil {
		return FederatedIdentity{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+federatedIdentityColumns+`
		FROM federated_identities
		WHERE tenant = $1 AND connector = $2 AND external_subject = $3`,
		tenant, connector, externalSubject)
	return scanFederatedIdentity(op, row)
}

func (r federatedIdentityRepo) Link(ctx context.Context, tenant Tenant, f FederatedIdentity) error {
	const op = "store.FederatedIdentities.Link"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if f.Connector == "" || f.ExternalSubject == "" || f.SubjectID == "" {
		return invalid(op, "connector, external subject and subject id are required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		INSERT INTO federated_identities (tenant, connector, external_subject, subject_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant, connector, external_subject) DO UPDATE SET
			last_seen_at = now()
		WHERE federated_identities.subject_id = EXCLUDED.subject_id`,
		tenant, f.Connector, f.ExternalSubject, f.SubjectID)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return conflict(op, "this external subject is already linked to a different subject")
	}
	return nil
}

func (r federatedIdentityRepo) ListBySubject(ctx context.Context, tenant Tenant, subjectID string) ([]FederatedIdentity, error) {
	const op = "store.FederatedIdentities.ListBySubject"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+federatedIdentityColumns+`
		FROM federated_identities
		WHERE tenant = $1 AND subject_id = $2
		ORDER BY connector, external_subject`, tenant, subjectID)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []FederatedIdentity
	for rows.Next() {
		f, err := scanFederatedIdentity(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, wrap(op, rows.Err())
}

// ---------------------------------------------------------------------------
// federation flow state
// ---------------------------------------------------------------------------

// FlowStateRepository stores outstanding logins. A flow is SINGLE USE: Consume
// resolves it and refuses a second attempt, so a replayed callback is told
// "spent" rather than "never issued" (PLAN §6's rule for MFA challenges, and
// the same reasoning).
type FlowStateRepository interface {
	// Begin stores a flow. A duplicate state is a conflict.
	Begin(ctx context.Context, tenant Tenant, f FlowState) error
	// Consume marks a flow used and returns it as it was. A state that was
	// never issued, and one that was already consumed, are both
	// ErrNotFound and ErrConflict respectively — two different facts that
	// deserve two different audit records.
	Consume(ctx context.Context, tenant Tenant, state string, now time.Time) (FlowState, error)
	// DeleteExpired removes flows past their expiry and returns how many.
	DeleteExpired(ctx context.Context, tenant Tenant, before time.Time) (int64, error)
}

type flowStateRepo struct{ s *Store }

const flowStateColumns = `state, connector, kind, nonce, pkce_verifier, request_id,
	redirect_uri, created_at, expires_at, consumed_at`

func (r flowStateRepo) Begin(ctx context.Context, tenant Tenant, f FlowState) error {
	const op = "store.FlowStates.Begin"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if f.State == "" || f.Connector == "" {
		return invalid(op, "state and connector are required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO federation_flow_states
			(tenant, state, connector, kind, nonce, pkce_verifier, request_id,
			 redirect_uri, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		tenant, f.State, f.Connector, string(f.Kind), f.Nonce, f.PKCEVerifier,
		f.RequestID, f.RedirectURI, f.CreatedAt, f.ExpiresAt)
	return wrap(op, err)
}

func (r flowStateRepo) Consume(ctx context.Context, tenant Tenant, state string, now time.Time) (FlowState, error) {
	const op = "store.FlowStates.Consume"
	if err := checkTenant(op, tenant); err != nil {
		return FlowState{}, err
	}

	var out FlowState
	err := r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		row := q.QueryRow(ctx, `
			SELECT `+flowStateColumns+`
			FROM federation_flow_states
			WHERE tenant = $1 AND state = $2
			FOR UPDATE`, tenant, state)
		f, err := scanFlowState(op, row)
		if err != nil {
			return err
		}
		if !f.ConsumedAt.IsZero() {
			return conflict(op, "this login has already been completed")
		}
		if _, err := q.Exec(ctx, `
			UPDATE federation_flow_states SET consumed_at = $3
			WHERE tenant = $1 AND state = $2`, tenant, state, now); err != nil {
			return wrap(op, err)
		}
		f.ConsumedAt = now
		out = f
		return nil
	})
	if err != nil {
		return FlowState{}, err
	}
	return out, nil
}

func (r flowStateRepo) DeleteExpired(ctx context.Context, tenant Tenant, before time.Time) (int64, error) {
	const op = "store.FlowStates.DeleteExpired"
	if err := checkTenant(op, tenant); err != nil {
		return 0, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		DELETE FROM federation_flow_states
		WHERE tenant = $1 AND expires_at < $2`, tenant, before)
	if err != nil {
		return 0, wrap(op, err)
	}
	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------------------
// north-bound principals
// ---------------------------------------------------------------------------

// NorthPrincipalRepository stores the north-bound credential model (M2, M18).
type NorthPrincipalRepository interface {
	// Get returns a principal by id. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, principalID string) (NorthPrincipal, error)
	// ByTokenDigest and BySessionDigest are the middleware's lookups. The
	// tenant is the one the presented credential named itself, which is
	// M22's shape reused on this surface: a credential that carries its
	// tenant is a credential this layer can look up without a cross-tenant
	// scan.
	ByTokenDigest(ctx context.Context, tenant Tenant, digest string) (NorthPrincipal, error)
	BySessionDigest(ctx context.Context, tenant Tenant, digest string) (NorthPrincipal, error)
	// Put writes a principal.
	Put(ctx context.Context, tenant Tenant, p NorthPrincipal) error
	// Touch records that a credential was used.
	Touch(ctx context.Context, tenant Tenant, principalID string, at time.Time) error
	// Revoke ends a credential. Absent is ErrNotFound; already revoked is
	// not an error, because the caller's intent is satisfied.
	Revoke(ctx context.Context, tenant Tenant, principalID string, at time.Time) error
	// List returns every principal in the tenant, newest first.
	List(ctx context.Context, tenant Tenant) ([]NorthPrincipal, error)
}

type northPrincipalRepo struct{ s *Store }

const northPrincipalColumns = `principal_id, kind, subject_id, display_name, source,
	break_glass, mapping_version, token_digest, session_digest, scopes,
	created_at, last_used_at, expires_at, revoked_at`

func (r northPrincipalRepo) Get(ctx context.Context, tenant Tenant, principalID string) (NorthPrincipal, error) {
	const op = "store.NorthPrincipals.Get"
	if err := checkTenant(op, tenant); err != nil {
		return NorthPrincipal{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+northPrincipalColumns+`
		FROM north_principals
		WHERE tenant = $1 AND principal_id = $2`, tenant, principalID)
	return scanNorthPrincipal(op, row)
}

func (r northPrincipalRepo) ByTokenDigest(ctx context.Context, tenant Tenant, digest string) (NorthPrincipal, error) {
	return r.byDigest(ctx, "store.NorthPrincipals.ByTokenDigest", tenant, "token_digest", digest)
}

func (r northPrincipalRepo) BySessionDigest(ctx context.Context, tenant Tenant, digest string) (NorthPrincipal, error) {
	return r.byDigest(ctx, "store.NorthPrincipals.BySessionDigest", tenant, "session_digest", digest)
}

func (r northPrincipalRepo) byDigest(ctx context.Context, op string, tenant Tenant, column, digest string) (NorthPrincipal, error) {
	if err := checkTenant(op, tenant); err != nil {
		return NorthPrincipal{}, err
	}
	if digest == "" {
		// An empty digest would match every row whose other digest
		// column is empty. Refuse it here rather than let a caller with
		// no credential authenticate as the first session in the table.
		return NorthPrincipal{}, invalid(op, "a credential digest is required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// The column name is a package constant chosen by the two callers
	// above, never caller input.
	row := r.s.db.QueryRow(ctx, `
		SELECT `+northPrincipalColumns+`
		FROM north_principals
		WHERE tenant = $1 AND `+column+` = $2`, tenant, digest)
	return scanNorthPrincipal(op, row)
}

func (r northPrincipalRepo) Put(ctx context.Context, tenant Tenant, p NorthPrincipal) error {
	const op = "store.NorthPrincipals.Put"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if p.ID == "" || p.Kind == "" {
		return invalid(op, "principal id and kind are required")
	}
	if (p.TokenDigest == "") == (p.SessionDigest == "") {
		return invalid(op, "exactly one of a token digest and a session digest is required")
	}
	scopes, err := json.Marshal(nonNilScopes(p.Scopes))
	if err != nil {
		return invalid(op, "scopes could not be encoded")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err = r.s.db.Exec(ctx, `
		INSERT INTO north_principals
			(tenant, principal_id, kind, subject_id, display_name, source,
			 break_glass, mapping_version, token_digest, session_digest, scopes,
			 expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (tenant, principal_id) DO UPDATE SET
			kind            = EXCLUDED.kind,
			subject_id      = EXCLUDED.subject_id,
			display_name    = EXCLUDED.display_name,
			source          = EXCLUDED.source,
			break_glass     = EXCLUDED.break_glass,
			mapping_version = EXCLUDED.mapping_version,
			token_digest    = EXCLUDED.token_digest,
			session_digest  = EXCLUDED.session_digest,
			scopes          = EXCLUDED.scopes,
			expires_at      = EXCLUDED.expires_at`,
		tenant, p.ID, string(p.Kind), p.SubjectID, p.DisplayName, p.Source,
		p.BreakGlass, p.MappingVersion, p.TokenDigest, p.SessionDigest, scopes,
		nullableTime(p.ExpiresAt))
	return wrap(op, err)
}

func (r northPrincipalRepo) Touch(ctx context.Context, tenant Tenant, principalID string, at time.Time) error {
	const op = "store.NorthPrincipals.Touch"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		UPDATE north_principals SET last_used_at = $3
		WHERE tenant = $1 AND principal_id = $2`, tenant, principalID, at)
	return wrap(op, err)
}

func (r northPrincipalRepo) Revoke(ctx context.Context, tenant Tenant, principalID string, at time.Time) error {
	const op = "store.NorthPrincipals.Revoke"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		UPDATE north_principals SET revoked_at = COALESCE(revoked_at, $3)
		WHERE tenant = $1 AND principal_id = $2`, tenant, principalID, at)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func (r northPrincipalRepo) List(ctx context.Context, tenant Tenant) ([]NorthPrincipal, error) {
	const op = "store.NorthPrincipals.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+northPrincipalColumns+`
		FROM north_principals
		WHERE tenant = $1
		ORDER BY created_at DESC, principal_id`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []NorthPrincipal
	for rows.Next() {
		p, err := scanNorthPrincipal(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, wrap(op, rows.Err())
}

// ---------------------------------------------------------------------------
// the SSH CA
// ---------------------------------------------------------------------------

// CAKeyRepository stores per-tenant certificate-authority keys (proxy D6a).
type CAKeyRepository interface {
	// Active returns the key new certificates are signed with. Absent is
	// ErrNotFound, which means this tenant has no CA yet.
	Active(ctx context.Context, tenant Tenant) (CAKey, error)
	// Get returns one key. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, keyID string) (CAKey, error)
	// List returns every key the tenant holds, newest first. This is the
	// trust bundle's source: an operator publishes the active key plus
	// every retired one still inside its TrustedUntil.
	List(ctx context.Context, tenant Tenant) ([]CAKey, error)
	// Insert writes a new key. A key id that already exists is a conflict:
	// rotation creates a new name, it never silently replaces a key
	// certificates are still chained to.
	Insert(ctx context.Context, tenant Tenant, k CAKey) error
	// Rotate retires the active key and activates a new one, atomically.
	// trustedUntil is how long the retired key stays in the trust bundle;
	// passing the rotation instant itself is the compromise case.
	Rotate(ctx context.Context, tenant Tenant, next CAKey, at, trustedUntil time.Time) error
}

type caKeyRepo struct{ s *Store }

const caKeyColumns = `key_id, algorithm, public_key, private_ref, private_key,
	active, trusted_until, comment, created_at, retired_at`

func (r caKeyRepo) Active(ctx context.Context, tenant Tenant) (CAKey, error) {
	const op = "store.CAKeys.Active"
	if err := checkTenant(op, tenant); err != nil {
		return CAKey{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+caKeyColumns+`
		FROM tenant_ca_keys
		WHERE tenant = $1 AND active`, tenant)
	return scanCAKey(op, row)
}

func (r caKeyRepo) Get(ctx context.Context, tenant Tenant, keyID string) (CAKey, error) {
	const op = "store.CAKeys.Get"
	if err := checkTenant(op, tenant); err != nil {
		return CAKey{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+caKeyColumns+`
		FROM tenant_ca_keys
		WHERE tenant = $1 AND key_id = $2`, tenant, keyID)
	return scanCAKey(op, row)
}

func (r caKeyRepo) List(ctx context.Context, tenant Tenant) ([]CAKey, error) {
	const op = "store.CAKeys.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+caKeyColumns+`
		FROM tenant_ca_keys
		WHERE tenant = $1
		ORDER BY created_at DESC, key_id`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []CAKey
	for rows.Next() {
		k, err := scanCAKey(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, wrap(op, rows.Err())
}

func (r caKeyRepo) Insert(ctx context.Context, tenant Tenant, k CAKey) error {
	const op = "store.CAKeys.Insert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if k.KeyID == "" || len(k.PublicKey) == 0 {
		return invalid(op, "key id and public key are required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO tenant_ca_keys
			(tenant, key_id, algorithm, public_key, private_ref, private_key,
			 active, trusted_until, comment)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		tenant, k.KeyID, k.Algorithm, k.PublicKey, k.PrivateRef, k.PrivateKey,
		k.Active, nullableTime(k.TrustedUntil), k.Comment)
	return wrap(op, err)
}

func (r caKeyRepo) Rotate(ctx context.Context, tenant Tenant, next CAKey, at, trustedUntil time.Time) error {
	const op = "store.CAKeys.Rotate"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if next.KeyID == "" || len(next.PublicKey) == 0 {
		return invalid(op, "key id and public key are required")
	}
	return r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		// The retirement and the activation are one transaction because
		// the partial unique index allows exactly one active key per
		// tenant: doing them in two statements from outside would leave
		// a window with none, and issuance in that window is an outage.
		if _, err := q.Exec(ctx, `
			UPDATE tenant_ca_keys
			SET active = false, retired_at = $2, trusted_until = $3
			WHERE tenant = $1 AND active`, tenant, at, trustedUntil); err != nil {
			return wrap(op, err)
		}
		_, err := q.Exec(ctx, `
			INSERT INTO tenant_ca_keys
				(tenant, key_id, algorithm, public_key, private_ref, private_key,
				 active, comment)
			VALUES ($1, $2, $3, $4, $5, $6, true, $7)`,
			tenant, next.KeyID, next.Algorithm, next.PublicKey, next.PrivateRef,
			next.PrivateKey, next.Comment)
		return wrap(op, err)
	})
}

// SSHCertificateRepository is the append-only record of issued certificates.
type SSHCertificateRepository interface {
	// NextSerial advances the tenant's serial counter and returns the value
	// to use. It advances under a row lock, for the reason the uid cursor
	// does: a serial that repeats makes a revocation list ambiguous.
	NextSerial(ctx context.Context, tenant Tenant) (int64, error)
	// Insert records an issued certificate.
	Insert(ctx context.Context, tenant Tenant, c SSHCertificate) error
	// Get returns one by serial. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, serial int64) (SSHCertificate, error)
	// Outstanding returns certificates that have neither expired nor been
	// revoked at the given instant, newest first. Rotation reads it to
	// answer "what is still out there", which is the question a rotation
	// story without one cannot answer.
	Outstanding(ctx context.Context, tenant Tenant, now time.Time) ([]SSHCertificate, error)
	// ListBySubject returns a subject's certificates, newest first.
	ListBySubject(ctx context.Context, tenant Tenant, subjectID string, limit int) ([]SSHCertificate, error)
	// Revoke marks one revoked. Absent is ErrNotFound.
	Revoke(ctx context.Context, tenant Tenant, serial int64, at time.Time, reason string) error
	// RevokeByCAKey revokes every outstanding certificate signed by one CA
	// key and returns how many. This is the compromise-rotation path.
	RevokeByCAKey(ctx context.Context, tenant Tenant, caKeyID string, at time.Time, reason string) (int64, error)
}

type sshCertificateRepo struct{ s *Store }

const sshCertificateColumns = `serial, key_id, ca_key_id, subject_id, session_id,
	principals, target, target_port, source_address, valid_after, valid_before,
	issued_at, revoked_at, revoked_reason, certificate`

func (r sshCertificateRepo) NextSerial(ctx context.Context, tenant Tenant) (int64, error) {
	const op = "store.SSHCertificates.NextSerial"
	if err := checkTenant(op, tenant); err != nil {
		return 0, err
	}

	var serial int64
	err := r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		row := q.QueryRow(ctx, `
			INSERT INTO ssh_certificate_serials (tenant, next)
			VALUES ($1, 2)
			ON CONFLICT (tenant) DO UPDATE SET next = ssh_certificate_serials.next + 1
			RETURNING next - 1`, tenant)
		if err := row.Scan(&serial); err != nil {
			return wrap(op, err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return serial, nil
}

func (r sshCertificateRepo) Insert(ctx context.Context, tenant Tenant, c SSHCertificate) error {
	const op = "store.SSHCertificates.Insert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if c.Serial <= 0 || c.Certificate == "" {
		return invalid(op, "serial and certificate are required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO ssh_certificates
			(tenant, serial, key_id, ca_key_id, subject_id, session_id, principals,
			 target, target_port, source_address, valid_after, valid_before,
			 issued_at, certificate)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		tenant, c.Serial, c.KeyID, c.CAKeyID, c.SubjectID, c.SessionID,
		nonNilStrings(c.Principals), c.Target, c.TargetPort, c.SourceAddress,
		c.ValidAfter, c.ValidBefore, c.IssuedAt, c.Certificate)
	return wrap(op, err)
}

func (r sshCertificateRepo) Get(ctx context.Context, tenant Tenant, serial int64) (SSHCertificate, error) {
	const op = "store.SSHCertificates.Get"
	if err := checkTenant(op, tenant); err != nil {
		return SSHCertificate{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+sshCertificateColumns+`
		FROM ssh_certificates
		WHERE tenant = $1 AND serial = $2`, tenant, serial)
	return scanSSHCertificate(op, row)
}

func (r sshCertificateRepo) Outstanding(ctx context.Context, tenant Tenant, now time.Time) ([]SSHCertificate, error) {
	const op = "store.SSHCertificates.Outstanding"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+sshCertificateColumns+`
		FROM ssh_certificates
		WHERE tenant = $1 AND revoked_at IS NULL AND valid_before > $2
		ORDER BY issued_at DESC, serial DESC`, tenant, now)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	return collectSSHCertificates(op, rows)
}

func (r sshCertificateRepo) ListBySubject(ctx context.Context, tenant Tenant, subjectID string, limit int) ([]SSHCertificate, error) {
	const op = "store.SSHCertificates.ListBySubject"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+sshCertificateColumns+`
		FROM ssh_certificates
		WHERE tenant = $1 AND subject_id = $2
		ORDER BY issued_at DESC, serial DESC
		LIMIT $3`, tenant, subjectID, limit)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	return collectSSHCertificates(op, rows)
}

func (r sshCertificateRepo) Revoke(ctx context.Context, tenant Tenant, serial int64, at time.Time, reason string) error {
	const op = "store.SSHCertificates.Revoke"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		UPDATE ssh_certificates
		SET revoked_at = COALESCE(revoked_at, $3), revoked_reason = $4
		WHERE tenant = $1 AND serial = $2`, tenant, serial, at, reason)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func (r sshCertificateRepo) RevokeByCAKey(ctx context.Context, tenant Tenant, caKeyID string, at time.Time, reason string) (int64, error) {
	const op = "store.SSHCertificates.RevokeByCAKey"
	if err := checkTenant(op, tenant); err != nil {
		return 0, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		UPDATE ssh_certificates
		SET revoked_at = $4, revoked_reason = $5
		WHERE tenant = $1 AND ca_key_id = $2 AND revoked_at IS NULL AND valid_before > $3`,
		tenant, caKeyID, at, at, reason)
	if err != nil {
		return 0, wrap(op, err)
	}
	return tag.RowsAffected(), nil
}

func collectSSHCertificates(op string, rows rowIterator) ([]SSHCertificate, error) {
	var out []SSHCertificate
	for rows.Next() {
		c, err := scanSSHCertificate(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, wrap(op, rows.Err())
}

// ---------------------------------------------------------------------------
// software key custody
// ---------------------------------------------------------------------------

// SoftwareKeyRepository stores the default custodian's key material.
type SoftwareKeyRepository interface {
	// Get returns a key. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, name string) (SoftwareKey, error)
	// Insert writes a new key. An existing name is a conflict: a key store
	// never silently replaces a key something is chained to.
	Insert(ctx context.Context, tenant Tenant, k SoftwareKey) error
	// List returns every key the tenant holds, newest first.
	List(ctx context.Context, tenant Tenant) ([]SoftwareKey, error)
	// Delete removes a key. Absent is ErrNotFound.
	Delete(ctx context.Context, tenant Tenant, name string) error
}

type softwareKeyRepo struct{ s *Store }

const softwareKeyColumns = `name, algorithm, public_key, private_key, created_at`

func (r softwareKeyRepo) Get(ctx context.Context, tenant Tenant, name string) (SoftwareKey, error) {
	const op = "store.SoftwareKeys.Get"
	if err := checkTenant(op, tenant); err != nil {
		return SoftwareKey{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var k SoftwareKey
	err := r.s.db.QueryRow(ctx, `
		SELECT `+softwareKeyColumns+`
		FROM software_keys
		WHERE tenant = $1 AND name = $2`, tenant, name).
		Scan(&k.Name, &k.Algorithm, &k.PublicKey, &k.PrivateKey, &k.CreatedAt)
	if err != nil {
		return SoftwareKey{}, wrap(op, err)
	}
	return k, nil
}

func (r softwareKeyRepo) Insert(ctx context.Context, tenant Tenant, k SoftwareKey) error {
	const op = "store.SoftwareKeys.Insert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if k.Name == "" || len(k.PublicKey) == 0 || len(k.PrivateKey) == 0 {
		return invalid(op, "name, public key and private key are required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO software_keys (tenant, name, algorithm, public_key, private_key)
		VALUES ($1, $2, $3, $4, $5)`,
		tenant, k.Name, k.Algorithm, k.PublicKey, k.PrivateKey)
	return wrap(op, err)
}

func (r softwareKeyRepo) List(ctx context.Context, tenant Tenant) ([]SoftwareKey, error) {
	const op = "store.SoftwareKeys.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+softwareKeyColumns+`
		FROM software_keys
		WHERE tenant = $1
		ORDER BY created_at DESC, name`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []SoftwareKey
	for rows.Next() {
		var k SoftwareKey
		if err := rows.Scan(&k.Name, &k.Algorithm, &k.PublicKey, &k.PrivateKey, &k.CreatedAt); err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, k)
	}
	return out, wrap(op, rows.Err())
}

func (r softwareKeyRepo) Delete(ctx context.Context, tenant Tenant, name string) error {
	const op = "store.SoftwareKeys.Delete"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		DELETE FROM software_keys WHERE tenant = $1 AND name = $2`, tenant, name)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}
