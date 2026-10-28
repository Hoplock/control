// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

// Directory provisioning (0040, PLAN M15, M7, M28): what a registered identity
// sync has told this deployment, in the directory's own vocabulary.
//
// A link joins the directory's identifier for a person to the subject Control
// allocated for them, and holds the deactivation the directory placed. A
// directory group is the directory's name for a group id. Neither is a policy
// input: only what the tenant's claim mapping makes of them reaches `subjects`.

// DirectoryLink is one directory identity and the subject it is linked to.
type DirectoryLink struct {
	// ExternalID is the directory's stable identifier: the join key.
	ExternalID string
	// SubjectID is the subject Control allocated when the id was first linked.
	SubjectID string
	// Provider is the registered sync's provider.
	Provider string
	// LinkedAt is when the id was first linked.
	LinkedAt time.Time
	// Provisioned reports that an upsert was applied, and so that the subject
	// row exists. False for a link a deactivation made ahead of its create.
	Provisioned bool
	// UpdatedAt is the directory's own time of the last applied upsert; zero
	// until one is applied.
	UpdatedAt time.Time
	// What the directory last said, in its own vocabulary.
	Username    string
	DisplayName string
	Email       string
	Attributes  map[string]string
	GroupIDs    []string
	// DeactivatedAt is when Control applied the standing deactivation, by its
	// own clock; zero when the identity is active. DeactivationReason is the
	// directory's reason code.
	DeactivatedAt      time.Time
	DeactivationReason string
	// InvalidateOwed and DeactivationOwed are what a committed change still
	// owes the revocation stream, so a retry of the same call delivers it.
	InvalidateOwed   bool
	DeactivationOwed bool
}

// Deactivated reports a standing deactivation.
func (l DirectoryLink) Deactivated() bool { return !l.DeactivatedAt.IsZero() }

// DirectoryGroup is one group as the directory names it.
type DirectoryGroup struct {
	ExternalID string
	Provider   string
	// Name is what the claim mapping's `groups.map` matches.
	Name string
	// UpdatedAt is the directory's own time of the last applied upsert; zero
	// until one is applied.
	UpdatedAt time.Time
	// DeletedAt is the tombstone, by Control's clock; zero for a live group.
	DeletedAt time.Time
}

// Deleted reports a tombstone.
func (g DirectoryGroup) Deleted() bool { return !g.DeletedAt.IsZero() }

// SubjectDirectory is what a subject read says of a subject the directory
// manages: whose it is, and the standing deactivation, if one stands.
type SubjectDirectory struct {
	Provider   string
	ExternalID string
	// DeactivatedAt is zero when the subject is active.
	DeactivatedAt      time.Time
	DeactivationReason string
}

// Deactivated reports a standing deactivation.
func (d SubjectDirectory) Deactivated() bool { return !d.DeactivatedAt.IsZero() }

// IdentitySyncPrincipalPrefix starts the principal a registered identity sync
// acts as: `identity-sync:<provider>`, with no subject (0040).
const IdentitySyncPrincipalPrefix = "identity-sync:"

// DeactivatedBy is who placed the deactivation: the registered sync, never a
// person.
func (d SubjectDirectory) DeactivatedBy() StandingActor {
	return StandingActor{Principal: IdentitySyncPrincipalPrefix + d.Provider}
}

// SubjectDirectory is what a subject read says of the link.
func (l DirectoryLink) SubjectDirectory() *SubjectDirectory {
	return &SubjectDirectory{
		Provider: l.Provider, ExternalID: l.ExternalID,
		DeactivatedAt: l.DeactivatedAt, DeactivationReason: l.DeactivationReason,
	}
}

// ---------------------------------------------------------------------------
// links
// ---------------------------------------------------------------------------

type directoryLinkRepo struct{ s *Store }

const directoryLinkColumns = `external_id, subject_id, provider, linked_at, provisioned,
	directory_updated_at, username, display_name, email, attributes, group_ids,
	deactivated_at, deactivation_reason, invalidate_owed, deactivation_owed`

func scanDirectoryLink(op string, row rowScanner) (DirectoryLink, error) {
	var (
		l                    DirectoryLink
		updated, deactivated *time.Time
	)
	err := row.Scan(&l.ExternalID, &l.SubjectID, &l.Provider, &l.LinkedAt, &l.Provisioned,
		&updated, &l.Username, &l.DisplayName, &l.Email, &l.Attributes, &l.GroupIDs,
		&deactivated, &l.DeactivationReason, &l.InvalidateOwed, &l.DeactivationOwed)
	if err != nil {
		return DirectoryLink{}, wrap(op, err)
	}
	l.LinkedAt = l.LinkedAt.UTC()
	l.UpdatedAt = utcOrZero(updated)
	l.DeactivatedAt = utcOrZero(deactivated)
	return l, nil
}

func utcOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// directoryKeySeparator keeps a tenant from running into an id in an advisory
// lock's key.
const directoryKeySeparator = "\x1f"

// lockKey takes a transaction-scoped advisory lock on one key, exclusive or
// shared, refused outside a transaction because a lock released when the
// statement ends serialises nothing.
func (s *Store) lockKey(ctx context.Context, op, kind string, tenant Tenant, id string, shared bool) error {
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if s.pool != nil {
		return invalid(op, "the lock is taken inside a transaction")
	}
	if id == "" {
		return invalid(op, "an id is required")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	fn := "pg_advisory_xact_lock"
	if shared {
		fn = "pg_advisory_xact_lock_shared"
	}
	// fn is one of the two constants above, never caller input.
	_, err := s.db.Exec(ctx, `SELECT `+fn+`(hashtextextended($1, 0))`,
		kind+directoryKeySeparator+string(tenant)+directoryKeySeparator+id)
	return wrap(op, err)
}

// Lock serialises everything done under one external id for the rest of the
// transaction, the way an access-context assertion id is serialised (0017).
func (r directoryLinkRepo) Lock(ctx context.Context, tenant Tenant, externalID string) error {
	return r.s.lockKey(ctx, "store.DirectoryLinks.Lock", "directory-subject", tenant, externalID, false)
}

// Get returns the link of an external id. Absent is ErrNotFound.
func (r directoryLinkRepo) Get(ctx context.Context, tenant Tenant, externalID string) (DirectoryLink, error) {
	const op = "store.DirectoryLinks.Get"
	if err := checkTenant(op, tenant); err != nil {
		return DirectoryLink{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	return scanDirectoryLink(op, r.s.db.QueryRow(ctx, `
		SELECT `+directoryLinkColumns+` FROM directory_links
		WHERE tenant = $1 AND external_id = $2`, tenant, externalID))
}

// BySubject returns the link of a subject id. Absent is ErrNotFound, and means
// the directory does not manage the subject.
func (r directoryLinkRepo) BySubject(ctx context.Context, tenant Tenant, subjectID string) (DirectoryLink, error) {
	const op = "store.DirectoryLinks.BySubject"
	if err := checkTenant(op, tenant); err != nil {
		return DirectoryLink{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	return scanDirectoryLink(op, r.s.db.QueryRow(ctx, `
		SELECT `+directoryLinkColumns+` FROM directory_links
		WHERE tenant = $1 AND subject_id = $2`, tenant, subjectID))
}

// ByPrincipal returns the link of a directory-managed subject that presents
// principal, other than the subject named by except. Absent is ErrNotFound.
func (r directoryLinkRepo) ByPrincipal(ctx context.Context, tenant Tenant, principal, except string) (DirectoryLink, error) {
	const op = "store.DirectoryLinks.ByPrincipal"
	if err := checkTenant(op, tenant); err != nil {
		return DirectoryLink{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	return scanDirectoryLink(op, r.s.db.QueryRow(ctx, `
		SELECT d.external_id, d.subject_id, d.provider, d.linked_at, d.provisioned,
			d.directory_updated_at, d.username, d.display_name, d.email, d.attributes, d.group_ids,
			d.deactivated_at, d.deactivation_reason, d.invalidate_owed, d.deactivation_owed
		FROM subjects s
		JOIN directory_links d ON d.tenant = s.tenant AND d.subject_id = s.subject_id
		WHERE s.tenant = $1 AND $2 = ANY(s.principals) AND s.subject_id <> $3
		ORDER BY d.subject_id
		LIMIT 1`, tenant, principal, except))
}

// Put writes a link whole: inserted the first time, replaced after.
func (r directoryLinkRepo) Put(ctx context.Context, tenant Tenant, l DirectoryLink) error {
	const op = "store.DirectoryLinks.Put"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if l.ExternalID == "" || l.SubjectID == "" || l.Provider == "" || l.LinkedAt.IsZero() {
		return invalid(op, "an external id, a subject id, a provider and the time it was linked are required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	tag, err := r.s.db.Exec(ctx, `
		INSERT INTO directory_links (tenant, external_id, subject_id, provider, linked_at, provisioned,
			directory_updated_at, username, display_name, email, attributes, group_ids,
			deactivated_at, deactivation_reason, invalidate_owed, deactivation_owed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (tenant, external_id) DO UPDATE SET
			provider             = EXCLUDED.provider,
			provisioned          = EXCLUDED.provisioned,
			directory_updated_at = EXCLUDED.directory_updated_at,
			username             = EXCLUDED.username,
			display_name         = EXCLUDED.display_name,
			email                = EXCLUDED.email,
			attributes           = EXCLUDED.attributes,
			group_ids            = EXCLUDED.group_ids,
			deactivated_at       = EXCLUDED.deactivated_at,
			deactivation_reason  = EXCLUDED.deactivation_reason,
			invalidate_owed      = EXCLUDED.invalidate_owed,
			deactivation_owed    = EXCLUDED.deactivation_owed
		WHERE directory_links.subject_id = EXCLUDED.subject_id`,
		tenant, l.ExternalID, l.SubjectID, l.Provider, l.LinkedAt.UTC(), l.Provisioned,
		nullableTime(l.UpdatedAt), l.Username, l.DisplayName, l.Email, nonNilMap(l.Attributes),
		nonNilStrings(l.GroupIDs), nullableTime(l.DeactivatedAt), l.DeactivationReason,
		l.InvalidateOwed, l.DeactivationOwed)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		// A link never moves to another subject: re-pointing one would be an
		// account takeover in one UPDATE.
		return conflict(op, "this external id is linked to a different subject")
	}
	return nil
}

// Settle clears what a link owes the stream once it has been delivered.
func (r directoryLinkRepo) Settle(ctx context.Context, tenant Tenant, externalID string, invalidate, deactivation bool) error {
	const op = "store.DirectoryLinks.Settle"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	_, err := r.s.db.Exec(ctx, `
		UPDATE directory_links SET
			invalidate_owed   = invalidate_owed AND NOT $3,
			deactivation_owed = deactivation_owed AND NOT $4
		WHERE tenant = $1 AND external_id = $2`, tenant, externalID, invalidate, deactivation)
	return wrap(op, err)
}

// Members returns the provisioned links whose group ids include a directory
// group's id, in subject order: the subjects a renamed or deleted group
// re-derives.
func (r directoryLinkRepo) Members(ctx context.Context, tenant Tenant, groupExternalID string) ([]DirectoryLink, error) {
	const op = "store.DirectoryLinks.Members"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	rows, err := r.s.db.Query(ctx, `
		SELECT `+directoryLinkColumns+` FROM directory_links
		WHERE tenant = $1 AND group_ids @> ARRAY[$2]::text[] AND provisioned
		ORDER BY subject_id`, tenant, groupExternalID)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []DirectoryLink
	for rows.Next() {
		l, err := scanDirectoryLink(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, wrap(op, rows.Err())
}

// For returns the links of these subjects, by subject id: the read a page of
// the subject list makes for its own rows (M26).
func (r directoryLinkRepo) For(ctx context.Context, tenant Tenant, subjectIDs []string) (map[string]DirectoryLink, error) {
	const op = "store.DirectoryLinks.For"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if err := checkPageIDs(op, subjectIDs); err != nil {
		return nil, err
	}
	out := map[string]DirectoryLink{}
	if len(subjectIDs) == 0 {
		return out, nil
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	rows, err := r.s.db.Query(ctx, `
		SELECT `+directoryLinkColumns+` FROM directory_links
		WHERE tenant = $1 AND subject_id = ANY($2)`, tenant, subjectIDs)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	for rows.Next() {
		l, err := scanDirectoryLink(op, rows)
		if err != nil {
			return nil, err
		}
		out[l.SubjectID] = l
	}
	return out, wrap(op, rows.Err())
}

// ---------------------------------------------------------------------------
// groups
// ---------------------------------------------------------------------------

type directoryGroupRepo struct{ s *Store }

const directoryGroupColumns = `external_id, provider, name, directory_updated_at, deleted_at`

func scanDirectoryGroup(op string, row rowScanner) (DirectoryGroup, error) {
	var (
		g                DirectoryGroup
		updated, deleted *time.Time
	)
	if err := row.Scan(&g.ExternalID, &g.Provider, &g.Name, &updated, &deleted); err != nil {
		return DirectoryGroup{}, wrap(op, err)
	}
	g.UpdatedAt = utcOrZero(updated)
	g.DeletedAt = utcOrZero(deleted)
	return g, nil
}

// directoryGroupsKey names the tenant's directory-groups lock.
const directoryGroupsKey = "groups"

// Lock takes the tenant's directory-groups lock exclusively for the rest of
// the transaction: what a group change takes, so it re-derives its members
// with no subject write in flight. Every subject write holds it shared (Share).
func (r directoryGroupRepo) Lock(ctx context.Context, tenant Tenant) error {
	return r.s.lockKey(ctx, "store.DirectoryGroups.Lock", "directory", tenant, directoryGroupsKey, false)
}

// Share takes the tenant's directory-groups lock shared for the rest of the
// transaction: subject writes run beside each other, and never beside a group
// change.
func (r directoryGroupRepo) Share(ctx context.Context, tenant Tenant) error {
	return r.s.lockKey(ctx, "store.DirectoryGroups.Share", "directory", tenant, directoryGroupsKey, true)
}

// Get returns a group. Absent is ErrNotFound.
func (r directoryGroupRepo) Get(ctx context.Context, tenant Tenant, externalID string) (DirectoryGroup, error) {
	const op = "store.DirectoryGroups.Get"
	if err := checkTenant(op, tenant); err != nil {
		return DirectoryGroup{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	return scanDirectoryGroup(op, r.s.db.QueryRow(ctx, `
		SELECT `+directoryGroupColumns+` FROM directory_groups
		WHERE tenant = $1 AND external_id = $2`, tenant, externalID))
}

// Put writes a group whole.
func (r directoryGroupRepo) Put(ctx context.Context, tenant Tenant, g DirectoryGroup) error {
	const op = "store.DirectoryGroups.Put"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if g.ExternalID == "" || g.Provider == "" {
		return invalid(op, "an external id and a provider are required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO directory_groups (tenant, external_id, provider, name, directory_updated_at, deleted_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant, external_id) DO UPDATE SET
			provider             = EXCLUDED.provider,
			name                 = EXCLUDED.name,
			directory_updated_at = EXCLUDED.directory_updated_at,
			deleted_at           = EXCLUDED.deleted_at`,
		tenant, g.ExternalID, g.Provider, g.Name, nullableTime(g.UpdatedAt), nullableTime(g.DeletedAt))
	return wrap(op, err)
}

// Names resolves group ids to the names of the live groups among them, by id.
// An id with no group, a deleted group or one with no name is absent.
func (r directoryGroupRepo) Names(ctx context.Context, tenant Tenant, externalIDs []string) (map[string]string, error) {
	const op = "store.DirectoryGroups.Names"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	out := map[string]string{}
	if len(externalIDs) == 0 {
		return out, nil
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	rows, err := r.s.db.Query(ctx, `
		SELECT external_id, name FROM directory_groups
		WHERE tenant = $1 AND external_id = ANY($2) AND deleted_at IS NULL AND name <> ''`,
		tenant, externalIDs)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, wrap(op, err)
		}
		out[id] = name
	}
	return out, wrap(op, rows.Err())
}
