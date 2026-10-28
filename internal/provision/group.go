// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package provision

import (
	"context"
	"strconv"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// A group's name is what the tenant's claim mapping matches. So a group whose
// name changes — created, renamed, deleted, or re-created after a delete —
// re-derives every subject the directory says is a member, in the same
// transaction, under the tenant's directory-groups lock held exclusively: no
// member is left half-applied, and no subject write in flight is re-derived
// from what it is replacing.

// getGroup reads a group, reporting absence as a bool rather than an error.
func getGroup(ctx context.Context, tx *store.Store, tenant store.Tenant, externalID string) (store.DirectoryGroup, bool, error) {
	g, err := tx.DirectoryGroups().Get(ctx, tenant, externalID)
	switch {
	case err == nil:
		return g, true, nil
	case store.IsNotFound(err):
		return store.DirectoryGroup{}, false, nil
	default:
		return store.DirectoryGroup{}, false, err
	}
}

// upsertGroup applies a group upsert. One whose UpdatedAt is not later than the
// last applied one, or than a tombstone's, changes nothing and writes nothing.
func (s *Sink) upsertGroup(ctx context.Context, req groupRequest) error {
	return s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.DirectoryGroups().Lock(ctx, req.tenant); err != nil {
			return err
		}
		cur, found, err := getGroup(ctx, tx, req.tenant, req.externalID)
		if err != nil {
			return err
		}
		if found && !cur.UpdatedAt.IsZero() && !req.updatedAt.After(cur.UpdatedAt) {
			return nil
		}
		if found && cur.Deleted() && !req.updatedAt.After(cur.DeletedAt) {
			// Written before the delete and delivered after it.
			return nil
		}
		before := ""
		if found && !cur.Deleted() {
			before = cur.Name
		}
		if err := tx.DirectoryGroups().Put(ctx, req.tenant, store.DirectoryGroup{
			ExternalID: req.externalID, Provider: s.provider, Name: req.name, UpdatedAt: req.updatedAt,
		}); err != nil {
			return err
		}
		members := 0
		if before != req.name {
			if members, err = s.rederive(ctx, tx, req.tenant, req.externalID); err != nil {
				return err
			}
		}
		fields := map[string]string{"name": req.name, "members_rederived": strconv.Itoa(members)}
		if before != "" && before != req.name {
			fields["name_before"] = before
		}
		if found && cur.Deleted() {
			fields["tombstone_lifted"] = "true"
		}
		return s.recorder.SyncEvent(ctx, tx, req.tenant, audit.SyncAct{
			Event: audit.EventSyncGroupUpserted, Principal: s.principal, Provider: s.provider,
			ExternalID: req.externalID, Fields: fields,
			Message: "directory group " + req.externalID + " written",
		})
	})
}

// deleteGroup deletes a group, keeping a tombstone so an older upsert delivered
// after it changes nothing. Deleting an id with no group keeps the tombstone
// too; deleting one already deleted changes nothing and writes nothing.
func (s *Sink) deleteGroup(ctx context.Context, tenant store.Tenant, externalID string) error {
	return s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.DirectoryGroups().Lock(ctx, tenant); err != nil {
			return err
		}
		cur, found, err := getGroup(ctx, tx, tenant, externalID)
		if err != nil {
			return err
		}
		if found && cur.Deleted() {
			return nil
		}
		tomb := store.DirectoryGroup{ExternalID: externalID, Provider: s.provider, DeletedAt: s.clock()}
		if found {
			tomb.Name, tomb.UpdatedAt = cur.Name, cur.UpdatedAt
		}
		if err := tx.DirectoryGroups().Put(ctx, tenant, tomb); err != nil {
			return err
		}
		members := 0
		if found && cur.Name != "" {
			// A group with no name mapped nothing, so its members have
			// nothing to lose.
			if members, err = s.rederive(ctx, tx, tenant, externalID); err != nil {
				return err
			}
		}
		return s.recorder.SyncEvent(ctx, tx, tenant, audit.SyncAct{
			Event: audit.EventSyncGroupDeleted, Principal: s.principal, Provider: s.provider,
			ExternalID: externalID,
			Fields:     map[string]string{"name": tomb.Name, "members_rederived": strconv.Itoa(members)},
			Message:    "directory group " + externalID + " deleted",
		})
	})
}

// rederive re-derives every provisioned subject the directory says is a member
// of a group, inside tx, and marks those whose groups changed as owing a cache
// invalidation. It returns how many it re-derived.
func (s *Sink) rederive(ctx context.Context, tx *store.Store, tenant store.Tenant, groupExternalID string) (int, error) {
	links, err := tx.DirectoryLinks().Members(ctx, tenant, groupExternalID)
	if err != nil || len(links) == 0 {
		return 0, err
	}
	mapping, err := identity.ActiveMapping(ctx, tx, tenant)
	if err != nil {
		return 0, err
	}
	for _, link := range links {
		before, err := tx.Subjects().Get(ctx, tenant, link.SubjectID)
		if err != nil {
			return 0, err
		}
		subject, err := s.derive(ctx, tx, tenant, link, mapping)
		if err != nil {
			return 0, err
		}
		if err := tx.Subjects().Upsert(ctx, tenant, subject); err != nil {
			return 0, err
		}
		if changed(before, subject) && !link.InvalidateOwed {
			link.InvalidateOwed = true
			if err := tx.DirectoryLinks().Put(ctx, tenant, link); err != nil {
				return 0, err
			}
		}
	}
	return len(links), nil
}
