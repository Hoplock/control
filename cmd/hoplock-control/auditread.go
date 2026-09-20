// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/store"
)

// The record read-back path: `GET /debug/logs/{record_id}`, plus a bounded
// session listing beside it.
//
// IT EXISTS BECAUSE THE PRIORITY ACK IS OTHERWISE UNOBSERVABLE. The contract
// defines no way to read a record back, and upstream `Hoplock/proxy#56`
// (merged) says outright that this is deliberate: a proxy writes logs and
// never queries them, so an operator read API on `/v1` would be one every
// Hoplock Control implements and no proxy calls. The guarantee the proxy acts
// on — that a `200` means the record is durable — can therefore only be graded
// through a path this server exposes outside `/v1`, and the conformance suite
// takes it as an input (`logs.read_url`). Adding a read endpoint to `/v1`
// instead would be a contract change, which is not this phase's to make
// (`docs/CROSS-REPO-PROTOCOL.md` §3.2).
//
// IT IS SCHEDULED FOR DELETION, AND THAT IS WRITTEN DOWN WHERE THE SESSION
// THAT MUST DELETE IT WILL SEE IT. `docs/PROTOCOL.md` §3 permits a debug
// endpoint only when a named production API will supersede it and THAT PHASE'S
// PROMPT CARRIES THE REMOVAL — a note in a learnings file does not count.
// `prompts/queued/0014-northbound-api-and-policy-lifecycle.md` names every
// file, config key, fixture and CI line that goes with this one, and carries
// an acceptance criterion asserting it is gone.
//
// The four properties that keep it from being a back door meanwhile are the
// publish path's, with one of them read differently:
//
//   - IT IS OFF UNLESS CONFIGURED. `audit.read_listener` is empty by default.
//   - IT REQUIRES A CREDENTIAL OF ITS OWN. Audit records are the most
//     sensitive documents this server holds, and the south-bound proxy token
//     is not the credential for reading them: a token that ships logs must not
//     also read everybody's.
//   - IT IS A SEPARATE PORT (M2), and a separate one from the publish
//     listener too, because reading the audit store and publishing the kill
//     switch are different privileges.
//   - IT ONLY READS. There is no write, no delete and no retention control on
//     it — the store is append-only and this path cannot pretend otherwise.

// auditReadServer is the handler tree for the read listener.
type auditReadServer struct {
	reader *audit.Reader
	tenant store.Tenant
	token  string
}

func newAuditReadServer(reader *audit.Reader, tenant store.Tenant, token string) (*auditReadServer, error) {
	if token == "" {
		// Config refuses this pairing too; the second check is here
		// because this constructor is what a test would reach for, and
		// an unauthenticated audit read must not be reachable by
		// forgetting an argument.
		return nil, fmt.Errorf("auditread: a token is required to bind the audit read listener")
	}
	return &auditReadServer{reader: reader, tenant: tenant, token: token}, nil
}

func (a *auditReadServer) handler() http.Handler {
	mux := http.NewServeMux()
	// One record by its client-assigned id. This is the path the
	// durability assertion goes through, and the id in the URL is what
	// makes the assertion exact: a listing that happened to contain the id
	// would pass without the record having been found.
	mux.Handle("GET /debug/logs/{record_id}", http.HandlerFunc(a.getRecord))
	// A bounded session listing, in replay order. It is here because a
	// record read back on its own answers "is it stored" and nothing else,
	// and the first thing anybody does with a stored record is look at what
	// surrounded it.
	mux.Handle("GET /debug/logs", http.HandlerFunc(a.listSession))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusNotFound, "this listener reads audit records and serves nothing else")
	}))
	return mux
}

// auditRecordView is one record as this path renders it.
//
// It carries the chain fields as well as the content: a reader who can see the
// position and the hash can check the record against a chain they already hold
// without asking this server to vouch for it, which is the same property
// `ext.AuditRecord` gives an export sink.
type auditRecordView struct {
	RecordID   string            `json:"record_id"`
	Stream     string            `json:"stream"`
	ChainSeq   int64             `json:"chain_seq"`
	PrevHash   string            `json:"prev_hash,omitempty"`
	Hash       string            `json:"hash"`
	SessionID  string            `json:"session_id,omitempty"`
	Kind       string            `json:"kind"`
	Severity   string            `json:"severity"`
	Subject    string            `json:"subject,omitempty"`
	Login      string            `json:"login,omitempty"`
	Target     string            `json:"target,omitempty"`
	Message    string            `json:"message,omitempty"`
	Event      string            `json:"event,omitempty"`
	DecisionID string            `json:"decision_id,omitempty"`
	ProxyID    string            `json:"proxy_id,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	RecordedAt time.Time         `json:"recorded_at"`
	ReceivedAt time.Time         `json:"received_at"`
	// Body is the canonical bytes the hash covers, as a string, so a
	// reader can recompute the link themselves.
	Body string `json:"body"`
}

func viewOf(row store.AuditRecord) auditRecordView {
	return auditRecordView{
		RecordID:   row.RecordID,
		Stream:     row.Stream,
		ChainSeq:   row.ChainSeq,
		PrevHash:   row.PrevHash,
		Hash:       row.Hash,
		SessionID:  row.SessionID,
		Kind:       row.Kind,
		Severity:   row.Severity,
		Subject:    row.Subject,
		Login:      row.Login,
		Target:     row.Target,
		Message:    row.Message,
		Event:      row.Event,
		DecisionID: row.DecisionID,
		ProxyID:    row.ProxyID,
		Attributes: row.Attributes,
		RecordedAt: row.RecordedAt,
		ReceivedAt: row.ReceivedAt,
		Body:       row.Body,
	}
}

func (a *auditReadServer) getRecord(w http.ResponseWriter, r *http.Request) {
	if !a.authorised(r) {
		writeProblem(w, http.StatusUnauthorized, "the audit read credential was rejected")
		return
	}
	id := r.PathValue("record_id")
	if id == "" {
		writeProblem(w, http.StatusBadRequest, "a record id is required")
		return
	}

	row, err := a.reader.Get(r.Context(), a.tenant, id)
	if err != nil {
		if store.IsNotFound(err) {
			writeProblem(w, http.StatusNotFound, "no record with that id is stored")
			return
		}
		writeProblem(w, http.StatusInternalServerError, "the record could not be read")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(row))
}

func (a *auditReadServer) listSession(w http.ResponseWriter, r *http.Request) {
	if !a.authorised(r) {
		writeProblem(w, http.StatusUnauthorized, "the audit read credential was rejected")
		return
	}

	limit, err := strconv.Atoi(orDefault(r.URL.Query().Get("limit"), "100"))
	if err != nil || limit <= 0 {
		writeProblem(w, http.StatusBadRequest, "limit must be a positive integer")
		return
	}

	var rows []store.AuditRecord
	if session := r.URL.Query().Get("session_id"); session != "" {
		rows, err = a.reader.Session(r.Context(), a.tenant, session, limit)
	} else {
		rows, err = a.reader.Find(r.Context(), a.tenant, audit.Query{Limit: limit})
	}
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "the records could not be read")
		return
	}

	views := make([]auditRecordView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewOf(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": views})
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// authorised checks the bearer token in constant time, for the reason the
// publish listener does: the value is a credential and the listener answers as
// fast as an attacker can ask.
func (a *auditReadServer) authorised(r *http.Request) bool {
	const prefix = "Bearer "
	v := r.Header.Get("Authorization")
	if len(v) <= len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
		return false
	}
	presented := strings.TrimSpace(v[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(presented), []byte(a.token)) == 1
}
