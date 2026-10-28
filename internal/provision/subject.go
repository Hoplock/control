// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package provision

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// subjectOutcome is what a subject call committed, and what is left to do once
// it has.
type subjectOutcome struct {
	// link is the link as it now stands.
	link store.DirectoryLink
	// deactivate asks for the deactivation's revocations and kill: the call
	// was a deactivation, newly placed or not.
	deactivate bool
	// placed reports that THIS call placed the deactivation, so its kill
	// publishes a second pass one decision budget later.
	placed bool
}

// getLink reads a link, reporting absence as a bool rather than an error.
func getLink(ctx context.Context, tx *store.Store, tenant store.Tenant, externalID string) (store.DirectoryLink, bool, error) {
	link, err := tx.DirectoryLinks().Get(ctx, tenant, externalID)
	switch {
	case err == nil:
		return link, true, nil
	case store.IsNotFound(err):
		return store.DirectoryLink{}, false, nil
	default:
		return store.DirectoryLink{}, false, err
	}
}

// upsertSubject applies an upsert in one transaction: the link, the subject row
// and their records commit together or not at all.
//
// ORDER (item 5 of the phase): an upsert whose UpdatedAt is not later than the
// last applied one changes nothing; `Active: false` is a deactivation; and
// `Active: true` lifts a standing deactivation only when its UpdatedAt is later
// than both the last applied one and the instant Control applied the
// deactivation, so a directory clock behind Control's delays a re-activation
// and never shortens a deactivation.
func (s *Sink) upsertSubject(ctx context.Context, req subjectRequest) (subjectOutcome, error) {
	var out subjectOutcome
	err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		// Shared with every other subject write and exclusive of a group
		// change, then this id's own lock: two calls on one id have one
		// effect, and a group change never re-derives this subject from what
		// this call is replacing.
		if err := tx.DirectoryGroups().Share(ctx, req.tenant); err != nil {
			return err
		}
		if err := tx.DirectoryLinks().Lock(ctx, req.tenant, req.externalID); err != nil {
			return err
		}
		link, found, err := getLink(ctx, tx, req.tenant, req.externalID)
		if err != nil {
			return err
		}
		if found && !link.UpdatedAt.IsZero() && !req.updatedAt.After(link.UpdatedAt) {
			// A replay or a stale write: nothing changes and nothing is
			// written. What an earlier call still owes the stream is
			// delivered after the commit.
			out.link = link
			return nil
		}

		now := s.clock()
		if !found {
			id, err := s.allocate(ctx, tx, req.tenant)
			if err != nil {
				return err
			}
			link = store.DirectoryLink{ExternalID: req.externalID, SubjectID: id, Provider: s.provider, LinkedAt: now}
		}

		// Logins are unique to one subject (item 7). Every writer that gives
		// a subject this name holds its lock, so two cannot both find it free.
		if req.username != "" {
			if err := tx.Subjects().LockPrincipal(ctx, req.tenant, req.username); err != nil {
				return err
			}
			other, err := tx.Subjects().PresentedByAnother(ctx, req.tenant, req.username, link.SubjectID)
			switch {
			case err == nil:
				return &refusal{
					kind: ext.KindConflict, field: "username", conflict: other.ID,
					msg: "another subject in this tenant already presents this login name; Control never adopts or merges an existing subject",
				}
			case !store.IsNotFound(err):
				return err
			}
		}

		var before *store.Subject
		if link.Provisioned {
			b, err := tx.Subjects().Get(ctx, req.tenant, link.SubjectID)
			switch {
			case err == nil:
				before = &b
			case !store.IsNotFound(err):
				return err
			}
		}

		link.Provider = s.provider
		link.Provisioned = true
		link.UpdatedAt = req.updatedAt
		link.Username, link.DisplayName, link.Email = req.username, req.displayName, req.email
		link.Attributes, link.GroupIDs = req.attributes, req.groupIDs

		var reactivated *store.DirectoryLink
		switch {
		case !req.active:
			// `Active: false` is a deactivation, with exactly
			// DeactivateSubject's effect.
			out.deactivate = true
			if !link.Deactivated() {
				link.DeactivatedAt, link.DeactivationReason = now, ReasonDirectoryInactive
				out.placed = true
			}
			link.DeactivationOwed = true
		case link.Deactivated() && req.updatedAt.After(link.DeactivatedAt):
			was := link
			reactivated = &was
			link.DeactivatedAt, link.DeactivationReason, link.DeactivationOwed = time.Time{}, "", false
		}

		mapping, err := identity.ActiveMapping(ctx, tx, req.tenant)
		if err != nil {
			return err
		}
		subject, err := s.derive(ctx, tx, req.tenant, link, mapping)
		if err != nil {
			return err
		}
		if err := tx.Subjects().Upsert(ctx, req.tenant, subject); err != nil {
			return err
		}
		if before != nil && changed(*before, subject) {
			// A narrowing takes effect now rather than at a cached allow's
			// expiry: the subject's cached decisions are dropped.
			link.InvalidateOwed = true
		}

		var revoked credentialsRevoked
		if out.deactivate {
			if revoked, err = s.revokeCredentials(ctx, tx, req.tenant, link.SubjectID, now); err != nil {
				return err
			}
		}
		if err := tx.DirectoryLinks().Put(ctx, req.tenant, link); err != nil {
			return err
		}

		event, verb := audit.EventSyncSubjectUpdated, "updated"
		if before == nil {
			event, verb = audit.EventSyncSubjectProvisioned, "provisioned"
		}
		if err := s.recorder.SyncEvent(ctx, tx, req.tenant, audit.SyncAct{
			Event: event, Principal: s.principal, Provider: s.provider, ExternalID: link.ExternalID,
			Subject: link.SubjectID, Fields: subjectFields(before, subject),
			Message: "subject " + link.SubjectID + " " + verb + " by the directory",
		}); err != nil {
			return err
		}
		if out.placed {
			if err := s.recordDeactivated(ctx, tx, req.tenant, link, revoked); err != nil {
				return err
			}
		}
		if reactivated != nil {
			if err := s.recorder.SyncEvent(ctx, tx, req.tenant, audit.SyncAct{
				Event: audit.EventSyncSubjectReactivated, Principal: s.principal, Provider: s.provider,
				ExternalID: link.ExternalID, Subject: link.SubjectID, Severity: contract.SeverityWarn,
				Fields: map[string]string{
					"deactivated_at":       reactivated.DeactivatedAt.Format(time.RFC3339Nano),
					"deactivation_code":    reactivated.DeactivationReason,
					"directory_updated_at": req.updatedAt.Format(time.RFC3339Nano),
				},
				Message: "subject " + link.SubjectID + " re-activated by the directory",
			}); err != nil {
				return err
			}
		}
		out.link = link
		return nil
	})
	return out, err
}

// deactivate places the directory's standing deactivation, in one transaction
// with its record and the revocation of the subject's north-bound credentials
// and certificates. A deactivation for an id with no link is kept on a link of
// its own, and the create that arrives later provisions the subject already
// deactivated. One already standing writes no second record.
func (s *Sink) deactivate(ctx context.Context, tenant store.Tenant, externalID, reasonCode string) (subjectOutcome, error) {
	out := subjectOutcome{deactivate: true}
	err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.DirectoryGroups().Share(ctx, tenant); err != nil {
			return err
		}
		if err := tx.DirectoryLinks().Lock(ctx, tenant, externalID); err != nil {
			return err
		}
		link, found, err := getLink(ctx, tx, tenant, externalID)
		if err != nil {
			return err
		}
		now := s.clock()
		if !found {
			id, err := s.allocate(ctx, tx, tenant)
			if err != nil {
				return err
			}
			link = store.DirectoryLink{ExternalID: externalID, SubjectID: id, Provider: s.provider, LinkedAt: now}
		}
		if !link.Deactivated() {
			link.DeactivatedAt, link.DeactivationReason = now, reasonCode
			out.placed = true
		}

		var revoked credentialsRevoked
		if link.Provisioned {
			// Revoked on every call, not only the first: a retry finishes
			// what the first could not.
			link.DeactivationOwed = true
			if revoked, err = s.revokeCredentials(ctx, tx, tenant, link.SubjectID, now); err != nil {
				return err
			}
		}
		if err := tx.DirectoryLinks().Put(ctx, tenant, link); err != nil {
			return err
		}
		if out.placed {
			if err := s.recordDeactivated(ctx, tx, tenant, link, revoked); err != nil {
				return err
			}
		}
		out.link = link
		return nil
	})
	return out, err
}

// credentialsRevoked is what a deactivation revoked inside its transaction.
type credentialsRevoked struct {
	principals, certificates int64
}

// revokeCredentials ends the subject's north-bound sessions and API tokens and
// marks its outstanding SSH certificates revoked, inside the deactivation's
// transaction: from the commit, no credential minted before it is a way back
// in.
func (s *Sink) revokeCredentials(ctx context.Context, tx *store.Store, tenant store.Tenant, subjectID string, now time.Time) (credentialsRevoked, error) {
	var out credentialsRevoked
	var err error
	if out.principals, err = tx.NorthPrincipals().RevokeBySubject(ctx, tenant, subjectID, now); err != nil {
		return out, err
	}
	if out.certificates, err = tx.SSHCertificates().RevokeBySubject(ctx, tenant, subjectID, now,
		"the subject was deactivated by the directory"); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Sink) recordDeactivated(ctx context.Context, tx *store.Store, tenant store.Tenant, link store.DirectoryLink, revoked credentialsRevoked) error {
	return s.recorder.SyncEvent(ctx, tx, tenant, audit.SyncAct{
		Event: audit.EventSyncSubjectDeactivated, Principal: s.principal, Provider: s.provider,
		ExternalID: link.ExternalID, Subject: link.SubjectID, Severity: contract.SeverityWarn,
		Fields: map[string]string{
			"reason_code":          link.DeactivationReason,
			"provisioned":          strconv.FormatBool(link.Provisioned),
			"credentials_revoked":  strconv.FormatInt(revoked.principals, 10),
			"certificates_revoked": strconv.FormatInt(revoked.certificates, 10),
			"deactivated_at":       link.DeactivatedAt.Format(time.RFC3339Nano),
		},
		Message: "subject " + link.SubjectID + " deactivated by the directory",
	})
}

// derive builds the subject row a link stands for, through the tenant's claim
// mapping (item 3): the directory's attributes and email offered as claims, its
// groups resolved to their names and offered under the mapping's group claim.
// Only what the mapping names becomes an attribute or a group, and the row
// records the mapping's version (M4). Local membership merges in as it does for
// a federated subject, and the principals are exactly the directory's username.
func (s *Sink) derive(ctx context.Context, tx *store.Store, tenant store.Tenant, link store.DirectoryLink, mapping *identity.Mapping) (store.Subject, error) {
	names, err := tx.DirectoryGroups().Names(ctx, tenant, link.GroupIDs)
	if err != nil {
		return store.Subject{}, err
	}
	groupNames := slices.Compact(slices.Sorted(maps.Values(names)))
	attrs := mapping.ApplyDirectory(link.Attributes, link.Email, groupNames)
	local, err := tx.Groups().GroupsOf(ctx, tenant, link.SubjectID)
	if err != nil {
		return store.Subject{}, err
	}
	groups := slices.Compact(slices.Sorted(slices.Values(append(local, attrs.Groups...))))
	principals := []string{}
	if link.Username != "" {
		principals = []string{link.Username}
	}
	return store.Subject{
		ID:             link.SubjectID,
		Source:         s.provider,
		DisplayName:    firstNonEmpty(link.DisplayName, link.Username, link.SubjectID),
		Principals:     principals,
		Groups:         groups,
		Claims:         attrs.Claims,
		BreakGlass:     false,
		MappingVersion: attrs.MappingVersion,
	}, nil
}

// changed reports a change to what a decision reads: groups, claims or the
// login names.
func changed(before, after store.Subject) bool {
	return !slices.Equal(nonNil(before.Groups), nonNil(after.Groups)) ||
		!maps.Equal(before.Claims, after.Claims) ||
		!slices.Equal(nonNil(before.Principals), nonNil(after.Principals))
}

// subjectFields are a subject record's before and after, as JSON so an empty
// list still says "none".
func subjectFields(before *store.Subject, after store.Subject) map[string]string {
	out := map[string]string{
		"groups_after":     jsonOf(nonNil(after.Groups)),
		"claims_after":     jsonOf(nonNilMap(after.Claims)),
		"principals_after": jsonOf(nonNil(after.Principals)),
		"mapping_version":  strconv.Itoa(after.MappingVersion),
	}
	if before != nil {
		out["groups_before"] = jsonOf(nonNil(before.Groups))
		out["claims_before"] = jsonOf(nonNilMap(before.Claims))
		out["principals_before"] = jsonOf(nonNil(before.Principals))
		out["mapping_version_before"] = strconv.Itoa(before.MappingVersion)
	}
	return out
}

func jsonOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilMap(v map[string]string) map[string]string {
	if v == nil {
		return map[string]string{}
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
