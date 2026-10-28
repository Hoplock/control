// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// The records a registered identity sync's calls write (0040), on the tenant's
// control stream, each in the transaction that makes the change. They are not
// under the operator prefix because no operator acted: the directory did, and
// the sync is the actor (`identity-sync:<provider>`, no subject).
const (
	// EventSyncSubjectProvisioned is a subject the directory created here.
	EventSyncSubjectProvisioned = "identity_sync.subject_provisioned"
	// EventSyncSubjectUpdated is an applied upsert of a subject that existed.
	EventSyncSubjectUpdated = "identity_sync.subject_updated"
	// EventSyncSubjectDeactivated is a standing deactivation placed.
	EventSyncSubjectDeactivated = "identity_sync.subject_deactivated"
	// EventSyncSubjectReactivated is a standing deactivation lifted.
	EventSyncSubjectReactivated = "identity_sync.subject_reactivated"
	// EventSyncGroupUpserted is an applied group upsert.
	EventSyncGroupUpserted = "identity_sync.group_upserted"
	// EventSyncGroupDeleted is a group deleted, or a tombstone kept for one.
	EventSyncGroupDeleted = "identity_sync.group_deleted"
	// EventSyncRefused is a call Control refused: malformed, or a username
	// another subject presents.
	EventSyncRefused = "identity_sync.refused"
)

// Attribute keys every sync record carries, so that "everything the directory
// did to this person" is one filter whatever the event.
const (
	AttrSyncProvider   = "provider"
	AttrSyncExternalID = "external_id"
	// AttrSyncPrefix namespaces a sync record's own fields.
	AttrSyncPrefix = "identity_sync."
)

// SyncAct is one applied (or refused) call of the identity sync.
type SyncAct struct {
	// Event is one of the EventSync constants.
	Event string
	// Principal is the sync's actor, `identity-sync:<provider>`.
	Principal string
	// Provider is the registered sync's provider.
	Provider string
	// ExternalID is the directory's id for the subject or the group.
	ExternalID string
	// Subject is the subject the call concerned, where it concerned one, so
	// the existing subject filter finds it.
	Subject string
	// Fields are the act's own facts, under AttrSyncPrefix.
	Fields map[string]string
	// Message is the sentence a record list shows.
	Message string
	// Severity defaults to info: a directory keeping this deployment in step
	// is routine. A deactivation and a refusal are worth a look, and say so.
	Severity contract.LogSeverity
	At       time.Time
}

// SyncEvent records one call of the identity sync. With tx it writes inside the
// call's own transaction, so the change and its record commit together; nil
// writes on its own, for a refusal that changed nothing.
func (e *Emitter) SyncEvent(ctx context.Context, tx *store.Store, tenant store.Tenant, act SyncAct) error {
	if tenant == "" {
		return fmt.Errorf("audit: an identity sync record needs a tenant")
	}
	if act.Event == "" || act.Principal == "" {
		return fmt.Errorf("audit: an identity sync record names its event and its actor")
	}
	recordID, err := newRecordID()
	if err != nil {
		return err
	}
	attrs := map[string]string{
		AttrEvent:      act.Event,
		AttrBreakGlass: strconv.FormatBool(false),
	}
	put(attrs, AttrPrincipalID, act.Principal)
	put(attrs, AttrSyncProvider, act.Provider)
	put(attrs, AttrSyncExternalID, act.ExternalID)
	for k, v := range act.Fields {
		put(attrs, AttrSyncPrefix+k, v)
	}

	severity := act.Severity
	if severity == "" {
		severity = contract.SeverityInfo
	}
	at := act.At
	if at.IsZero() {
		at = e.now()
	}
	message := act.Message
	if message == "" {
		message = act.Event
	}

	in := e.in
	if tx != nil {
		in = in.within(tx)
	}
	_, err = in.Ingest(ctx, Submission{
		Tenant:   tenant,
		Stream:   StreamControl,
		Priority: true,
		Records: []contract.LogRecord{{
			RecordID:   recordID,
			SessionID:  "",
			Timestamp:  at.UTC().Format(time.RFC3339Nano),
			Kind:       "policy_decision",
			Severity:   severity,
			Message:    message,
			Subject:    act.Subject,
			Attributes: attrs,
		}},
	})
	return err
}
