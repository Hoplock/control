// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// This server's own authentication records (M7, M8).
//
// Everything else in this package relays what a proxy shipped. These records are
// Control's: a console login, a federated login, a token issued, and — the one
// M7 names outright — A BREAK-GLASS LOGIN.
//
// THEY JOIN A CHAIN OF THEIR OWN ([StreamControl]). A record this server wrote
// is not a record a proxy submitted, and filing it under `unattributed` would
// put it in the same chain as a submission from an unbound token. A separate
// stream also means `audit-verify --stream control` verifies exactly the records
// nobody outside this process produced.
//
// THE FLAG IS ASSERTED, NEVER INFERRED. A reader must not have to conclude
// "break-glass" from `source == local`: "local" will one day mean something else,
// and a record whose most important fact is a convention is a record that stops
// being true quietly. So `break_glass` is its own attribute, written from the
// principal that carried it.

// Attribute keys these records carry, beyond the ones a proxy's record uses.
const (
	// AttrBreakGlass is "true" on every record a break-glass credential
	// touched. It is the attribute M7's acceptance criterion is asserted
	// against.
	AttrBreakGlass = "break_glass"
	// AttrMappingVersion names the claim-mapping version that produced the
	// identity's attributes (M4, M7). "Why did Alice match the sre rule" is
	// answered by the mapping as often as by the rule.
	AttrMappingVersion = "mapping_version"
	// AttrSource names the connector, or "local".
	AttrSource = "source"
	// AttrPrincipalID names the credential that was minted. It is not a
	// secret: the credential itself never appears in a record, because an
	// audit record of a credential is a credential in the audit store.
	AttrPrincipalID = "principal_id"
	// AttrDenyReason is this server's own refusal vocabulary. It is written
	// here and NEVER disclosed to the caller (M4).
	AttrDenyReason = "deny_reason"
	// AttrGroups is the mapped and local groups, comma separated.
	AttrGroups = "groups"
	// AttrCorrelationID ties the record to the request log.
	AttrCorrelationID = "correlation_id"
	// AttrAttributePrefix namespaces the mapped policy attributes, so that a
	// claim called `event` cannot overwrite the record's own `event`.
	AttrAttributePrefix = "attribute."
)

// Emitter writes this server's own records through the same ingest path a
// proxy's records take.
//
// Through the SAME path on purpose: the chain, the redaction and the
// idempotency are the parts that must not have a second implementation. A
// second writer that appended directly to the table would be a second chance to
// get the hash wrong.
type Emitter struct {
	in  *Ingester
	now func() time.Time
}

// NewEmitter builds an emitter over an ingester.
func NewEmitter(in *Ingester) (*Emitter, error) {
	if in == nil {
		return nil, fmt.Errorf("audit: an ingester is required")
	}
	return &Emitter{in: in, now: time.Now}, nil
}

// AuthEvent records one authentication outcome. It returns only after the
// commit, which is what lets [identity.Federation] treat a break-glass login it
// cannot write down as a login it refuses.
func (e *Emitter) AuthEvent(ctx context.Context, tenant store.Tenant, ev identity.AuthEvent) error {
	if tenant == "" {
		return fmt.Errorf("audit: an authentication record needs a tenant")
	}

	recordID, err := newRecordID()
	if err != nil {
		return err
	}

	attrs := map[string]string{
		AttrEvent:      ev.Event,
		AttrSource:     ev.Source,
		AttrBreakGlass: strconv.FormatBool(ev.BreakGlass),
	}
	if ev.MappingVersion > 0 {
		attrs[AttrMappingVersion] = strconv.Itoa(ev.MappingVersion)
	}
	if ev.PrincipalID != "" {
		attrs[AttrPrincipalID] = ev.PrincipalID
	}
	if ev.Reason != "" {
		attrs[AttrDenyReason] = ev.Reason
	}
	if len(ev.Groups) > 0 {
		attrs[AttrGroups] = strings.Join(ev.Groups, ",")
	}
	if ev.CorrelationID != "" {
		attrs[AttrCorrelationID] = ev.CorrelationID
	}
	for name, value := range ev.Attributes {
		attrs[AttrAttributePrefix+name] = value
	}

	severity := contract.SeverityInfo
	switch {
	case ev.BreakGlass:
		// A break-glass login is not routine, and a record filed at
		// `info` is a record nobody's alerting sees.
		severity = contract.SeverityCritical
	case ev.Event == "login_denied":
		severity = contract.SeverityWarn
	}

	// The session id is the credential's id where one was minted, and the
	// correlation id otherwise. A denial has no principal, and `auth` records
	// require a session id — so the alternative would be inventing one that
	// ties the record to nothing.
	sessionID := firstNonEmptyOf(ev.PrincipalID, ev.CorrelationID, recordID)

	_, err = e.in.Ingest(ctx, Submission{
		Tenant:   tenant,
		Stream:   StreamControl,
		Priority: true,
		Records: []contract.LogRecord{{
			RecordID:   recordID,
			SessionID:  sessionID,
			Timestamp:  e.now().UTC().Format(time.RFC3339),
			Kind:       "auth",
			Severity:   severity,
			Message:    authMessage(ev),
			Subject:    ev.Subject,
			Login:      ev.Login,
			Attributes: attrs,
		}},
	})
	return err
}

// authMessage is the sentence an operator reads in a record list.
//
// It names break-glass explicitly, because the list is the first place somebody
// looks and a message that reads like every other login is one they scroll past.
func authMessage(ev identity.AuthEvent) string {
	switch ev.Event {
	case "login":
		if ev.BreakGlass {
			return "break-glass local login"
		}
		return "login through " + or(ev.Source, "an unnamed connector")
	case "login_denied":
		if ev.BreakGlass {
			return "break-glass local login refused"
		}
		return "login refused"
	case "logout":
		return "session ended"
	case "token_issued":
		return "an api token was issued"
	}
	return ev.Event
}

// newRecordID mints an id for a record this server wrote.
//
// It is generated here rather than derived from the event, because `record_id`
// is the ingest path's idempotency key: two break-glass logins a second apart
// are two records, and a derived id would silently make them one.
func newRecordID() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("audit: a record id could not be generated")
	}
	return "ctl-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)), nil
}

func firstNonEmptyOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
