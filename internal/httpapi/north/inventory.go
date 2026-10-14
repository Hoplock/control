// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/authoring"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// Inventory (0019): what an operator manages — targets and their labels,
// subjects and their groups, role bindings, the claim mapping, proxy enrollment,
// and the fleet's configuration. RBAC-gated by the route table and AUDITED: every
// write below commits with its operator record, in one transaction where the
// write has one.
//
// LABEL EDITS ARE THE SHARPEST EDGE. A label is a policy input, so a relabel is
// a policy change: `POST …/targets/relabel` diffs the change, replays it over a
// window of recorded decisions through the same simulation a bundle gets, and
// only writes when asked to.

// labelString renders a label set canonically, for an audit record's before and
// after: sorted `key=value` pairs, comma-joined.
func labelString(labels map[string]string) string {
	keys := slices.Sorted(maps.Keys(labels))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	return strings.Join(parts, ",")
}

// ---------------------------------------------------------------------------
// targets
// ---------------------------------------------------------------------------

type targetView struct {
	ID               string            `json:"id"`
	Hostname         string            `json:"hostname"`
	Zone             string            `json:"zone"`
	Labels           map[string]string `json:"labels"`
	CredentialMethod string            `json:"credential_method,omitempty"`
	CreatedAt        time.Time         `json:"created_at,omitzero"`
	UpdatedAt        time.Time         `json:"updated_at,omitzero"`
}

func viewTarget(t store.Target) targetView {
	return targetView{
		ID: t.ID, Hostname: t.Hostname, Zone: t.Zone, Labels: nonNilLabels(t.Labels),
		CredentialMethod: t.CredentialMethod, CreatedAt: t.CreatedAt.UTC(), UpdatedAt: t.UpdatedAt.UTC(),
	}
}

type targetBody struct {
	Hostname         string            `json:"hostname"`
	Zone             string            `json:"zone"`
	Labels           map[string]string `json:"labels"`
	CredentialMethod string            `json:"credential_method"`
}

// handleTargetList lists targets, filtered by `?label=key=value`, repeatable
// and ANDed.
func (s *Server) handleTargetList(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	filter := map[string]string{}
	for _, raw := range r.URL.Query()["label"] {
		k, v, found := strings.Cut(raw, "=")
		if !found || strings.TrimSpace(k) == "" {
			s.invalid(w, r, "label", access.ProblemInvalid, "")
			return
		}
		filter[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	rows, err := s.st.Targets().ListByLabels(r.Context(), tenant, filter)
	if err != nil {
		s.fail(w, r, "list targets", err)
		return
	}
	out := make([]targetView, 0, len(rows))
	for _, t := range rows {
		out = append(out, viewTarget(t))
	}
	answer(w, http.StatusOK, map[string]any{"targets": out})
}

func (s *Server) handleTargetGet(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	t, err := s.st.Targets().Get(r.Context(), tenant, r.PathValue("target"))
	if err != nil {
		s.inventoryFailure(w, r, "read target", err)
		return
	}
	answer(w, http.StatusOK, viewTarget(t))
}

func (s *Server) handleTargetPut(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	var body targetBody
	if err := decodeJSON(r, &body); err != nil {
		s.invalid(w, r, "body", access.ProblemInvalid, "")
		return
	}
	if strings.TrimSpace(body.Hostname) == "" {
		s.invalid(w, r, "hostname", access.ProblemRequired, "")
		return
	}
	id := r.PathValue("target")
	var stored store.Target
	err := s.st.InTx(r.Context(), func(ctx context.Context, tx *store.Store) error {
		before, err := tx.Targets().Get(ctx, tenant, id)
		existed := err == nil
		if err != nil && !store.IsNotFound(err) {
			return err
		}
		next := store.Target{
			ID: id, Hostname: strings.TrimSpace(body.Hostname), Zone: strings.TrimSpace(body.Zone),
			Labels: body.Labels, CredentialMethod: body.CredentialMethod,
		}
		if err := tx.Targets().Upsert(ctx, tenant, next); err != nil {
			return err
		}
		if stored, err = tx.Targets().Get(ctx, tenant, id); err != nil {
			return err
		}
		act := audit.OperatorAct{
			Event: audit.EventTargetPut, Actor: actor, Target: stored.Hostname,
			After:   labelString(stored.Labels),
			Fields:  map[string]string{"target_id": id, "zone": stored.Zone, "created": strconv.FormatBool(!existed)},
			Message: "target " + id + " written",
		}
		if existed {
			act.Before = labelString(before.Labels)
			act.Fields["zone_before"] = before.Zone
			act.Fields["hostname_before"] = before.Hostname
		}
		return s.emitter.OperatorEvent(ctx, tx, tenant, act)
	})
	if err != nil {
		s.inventoryFailure(w, r, "write target", err)
		return
	}
	answer(w, http.StatusOK, viewTarget(stored))
}

func (s *Server) handleTargetDelete(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	id := r.PathValue("target")
	err := s.st.InTx(r.Context(), func(ctx context.Context, tx *store.Store) error {
		before, err := tx.Targets().Get(ctx, tenant, id)
		if err != nil {
			return err
		}
		if err := tx.Targets().Delete(ctx, tenant, id); err != nil {
			return err
		}
		return s.emitter.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
			Event: audit.EventTargetDeleted, Actor: actor, Target: before.Hostname,
			Before:  labelString(before.Labels),
			Fields:  map[string]string{"target_id": id, "zone": before.Zone},
			Message: "target " + id + " deleted",
		})
	})
	if err != nil {
		s.inventoryFailure(w, r, "delete target", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type relabelChange struct {
	Target string            `json:"target"`
	Labels map[string]string `json:"labels"`
}

type relabelBody struct {
	Changes []relabelChange `json:"changes"`
	// Replay, when set, replays the recorded decisions in a window with the
	// new labels, against the active policy: a relabel simulated like any
	// other policy change.
	Replay *authoring.ReplayWindow `json:"replay"`
	// Apply writes the change. Without it the answer is the diff and the
	// simulation and nothing is written.
	Apply bool `json:"apply"`
}

type relabelDiff struct {
	Target   string            `json:"target"`
	Hostname string            `json:"hostname"`
	Before   map[string]string `json:"before"`
	After    map[string]string `json:"after"`
	Added    []string          `json:"added"`
	Removed  []string          `json:"removed"`
	Changed  []string          `json:"changed"`
}

func diffLabels(before, after map[string]string) (added, removed, changed []string) {
	added, removed, changed = []string{}, []string{}, []string{}
	for _, k := range slices.Sorted(maps.Keys(after)) {
		old, ok := before[k]
		switch {
		case !ok:
			added = append(added, k)
		case old != after[k]:
			changed = append(changed, k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(before)) {
		if _, ok := after[k]; !ok {
			removed = append(removed, k)
		}
	}
	return added, removed, changed
}

func (s *Server) handleTargetRelabel(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	var body relabelBody
	if err := decodeJSON(r, &body); err != nil {
		s.invalid(w, r, "body", access.ProblemInvalid, "")
		return
	}
	if len(body.Changes) == 0 {
		s.invalid(w, r, "changes", access.ProblemRequired, "")
		return
	}

	diffs := make([]relabelDiff, 0, len(body.Changes))
	targets := make([]store.Target, 0, len(body.Changes))
	override := decision.LabelOverride{}
	for _, c := range body.Changes {
		t, err := s.st.Targets().Get(r.Context(), tenant, c.Target)
		if err != nil {
			s.inventoryFailure(w, r, "read target", err)
			return
		}
		added, removed, changed := diffLabels(t.Labels, c.Labels)
		diffs = append(diffs, relabelDiff{
			Target: t.ID, Hostname: t.Hostname, Before: nonNilLabels(t.Labels), After: nonNilLabels(c.Labels),
			Added: added, Removed: removed, Changed: changed,
		})
		override[t.Hostname] = nonNilLabels(c.Labels)
		t.Labels = c.Labels
		targets = append(targets, t)
	}

	out := map[string]any{"changes": diffs, "applied": false}
	if body.Replay != nil {
		res, err := s.authoring.Simulate(r.Context(), tenant, authoring.SimulateRequest{Replay: body.Replay, Labels: override})
		if err != nil {
			s.policyFailure(w, r, "simulate relabel", err)
			return
		}
		out["replay"] = res.Replay
	}
	if body.Apply {
		err := s.st.InTx(r.Context(), func(ctx context.Context, tx *store.Store) error {
			ids := make([]string, 0, len(targets))
			for i, t := range targets {
				if err := tx.Targets().Upsert(ctx, tenant, t); err != nil {
					return err
				}
				ids = append(ids, t.ID)
				if err := s.emitter.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
					Event: audit.EventTargetPut, Actor: actor, Target: t.Hostname,
					Before: labelString(diffs[i].Before), After: labelString(t.Labels),
					Fields:  map[string]string{"target_id": t.ID, "relabel": "true"},
					Message: "target " + t.ID + " relabelled",
				}); err != nil {
					return err
				}
			}
			return s.emitter.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
				Event: audit.EventTargetsRelabelled, Actor: actor,
				Fields:  map[string]string{"targets": strings.Join(ids, ","), "count": strconv.Itoa(len(ids))},
				Message: fmt.Sprintf("%d target(s) relabelled", len(ids)),
			})
		})
		if err != nil {
			s.inventoryFailure(w, r, "relabel targets", err)
			return
		}
		out["applied"] = true
	}
	answer(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// subjects, role bindings and the claim mapping
// ---------------------------------------------------------------------------

type subjectView struct {
	ID             string            `json:"id"`
	Source         string            `json:"source"`
	DisplayName    string            `json:"display_name"`
	Principals     []string          `json:"principals"`
	Groups         []string          `json:"groups"`
	Claims         map[string]string `json:"claims"`
	BreakGlass     bool              `json:"break_glass"`
	MappingVersion int               `json:"mapping_version,omitempty"`
}

func viewSubject(sub store.Subject) subjectView {
	return subjectView{
		ID: sub.ID, Source: sub.Source, DisplayName: sub.DisplayName, Principals: nonNilStrings(sub.Principals),
		Groups: nonNilStrings(sub.Groups), Claims: nonNilLabels(sub.Claims), BreakGlass: sub.BreakGlass,
		MappingVersion: sub.MappingVersion,
	}
}

type subjectBody struct {
	Source      string            `json:"source"`
	DisplayName string            `json:"display_name"`
	Principals  []string          `json:"principals"`
	Groups      []string          `json:"groups"`
	Claims      map[string]string `json:"claims"`
	BreakGlass  bool              `json:"break_glass"`
}

func (s *Server) handleSubjectList(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	rows, err := s.st.Subjects().List(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "list subjects", err)
		return
	}
	out := make([]subjectView, 0, len(rows))
	for _, sub := range rows {
		out = append(out, viewSubject(sub))
	}
	answer(w, http.StatusOK, map[string]any{"subjects": out})
}

func (s *Server) handleSubjectGet(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	sub, err := s.st.Subjects().Get(r.Context(), tenant, r.PathValue("subject"))
	if err != nil {
		s.inventoryFailure(w, r, "read subject", err)
		return
	}
	answer(w, http.StatusOK, viewSubject(sub))
}

func (s *Server) handleSubjectPut(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	var body subjectBody
	if err := decodeJSON(r, &body); err != nil {
		s.invalid(w, r, "body", access.ProblemInvalid, "")
		return
	}
	id := r.PathValue("subject")
	source := strings.TrimSpace(body.Source)
	if source == "" {
		source = identity.SourceLocal
	}
	var stored store.Subject
	err := s.st.InTx(r.Context(), func(ctx context.Context, tx *store.Store) error {
		before, err := tx.Subjects().Get(ctx, tenant, id)
		existed := err == nil
		if err != nil && !store.IsNotFound(err) {
			return err
		}
		next := store.Subject{
			ID: id, Source: source, DisplayName: body.DisplayName, Principals: body.Principals,
			Groups: body.Groups, Claims: body.Claims, BreakGlass: body.BreakGlass,
		}
		if existed {
			next.MappingVersion = before.MappingVersion
		}
		if err := tx.Subjects().Upsert(ctx, tenant, next); err != nil {
			return err
		}
		if stored, err = tx.Subjects().Get(ctx, tenant, id); err != nil {
			return err
		}
		act := audit.OperatorAct{
			Event: audit.EventSubjectPut, Actor: actor, Subject: id,
			After:   strings.Join(stored.Groups, ","),
			Fields:  map[string]string{"created": strconv.FormatBool(!existed), "break_glass": strconv.FormatBool(stored.BreakGlass)},
			Message: "subject " + id + " written",
		}
		if existed {
			act.Before = strings.Join(before.Groups, ",")
		}
		return s.emitter.OperatorEvent(ctx, tx, tenant, act)
	})
	if err != nil {
		s.inventoryFailure(w, r, "write subject", err)
		return
	}
	answer(w, http.StatusOK, viewSubject(stored))
}

func (s *Server) handleSubjectDelete(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	id := r.PathValue("subject")
	err := s.st.InTx(r.Context(), func(ctx context.Context, tx *store.Store) error {
		before, err := tx.Subjects().Get(ctx, tenant, id)
		if err != nil {
			return err
		}
		if err := tx.Subjects().Delete(ctx, tenant, id); err != nil {
			return err
		}
		return s.emitter.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
			Event: audit.EventSubjectDeleted, Actor: actor, Subject: id,
			Before: strings.Join(before.Groups, ","), Message: "subject " + id + " deleted",
		})
	})
	if err != nil {
		s.inventoryFailure(w, r, "delete subject", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type roleBindingView struct {
	SubjectID string    `json:"subject_id,omitempty"`
	GroupID   string    `json:"group_id,omitempty"`
	Role      string    `json:"role"`
	GrantedBy string    `json:"granted_by,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
}

type roleBindingBody struct {
	SubjectID string `json:"subject_id"`
	GroupID   string `json:"group_id"`
	Role      string `json:"role"`
}

func (s *Server) handleRoleBindingList(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	rows, err := s.st.RoleBindings().List(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "list role bindings", err)
		return
	}
	out := make([]roleBindingView, 0, len(rows))
	for _, b := range rows {
		out = append(out, roleBindingView{SubjectID: b.SubjectID, GroupID: b.GroupID, Role: b.Role, GrantedBy: b.GrantedBy, CreatedAt: b.CreatedAt.UTC()})
	}
	answer(w, http.StatusOK, map[string]any{"bindings": out, "roles": identity.AllRoles})
}

// bindingFrom validates a role binding: exactly one holder, and a role this
// build defines — an unknown code is refused, never coerced.
func (s *Server) bindingFrom(w http.ResponseWriter, r *http.Request, b roleBindingBody) (store.RoleBinding, bool) {
	subject, group := strings.TrimSpace(b.SubjectID), strings.TrimSpace(b.GroupID)
	if (subject == "") == (group == "") {
		s.invalid(w, r, "subject_id", "exactly_one_of_subject_id_or_group_id", "")
		return store.RoleBinding{}, false
	}
	role, err := identity.ParseRole(b.Role)
	if err != nil {
		s.invalid(w, r, "role", access.ProblemInvalid, "")
		return store.RoleBinding{}, false
	}
	return store.RoleBinding{SubjectID: subject, GroupID: group, Role: string(role)}, true
}

func (s *Server) handleRoleBind(w http.ResponseWriter, r *http.Request) {
	s.roleBindingAct(w, r, true)
}

func (s *Server) handleRoleUnbind(w http.ResponseWriter, r *http.Request) {
	s.roleBindingAct(w, r, false)
}

func (s *Server) roleBindingAct(w http.ResponseWriter, r *http.Request, bind bool) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	var body roleBindingBody
	if err := decodeJSON(r, &body); err != nil {
		s.invalid(w, r, "body", access.ProblemInvalid, "")
		return
	}
	b, ok := s.bindingFrom(w, r, body)
	if !ok {
		return
	}
	b.GrantedBy = actor.Principal
	holder := "subject:" + b.SubjectID
	if b.GroupID != "" {
		holder = "group:" + b.GroupID
	}
	event, verb := audit.EventRoleBound, "bound"
	if !bind {
		event, verb = audit.EventRoleUnbound, "unbound"
	}
	err := s.st.InTx(r.Context(), func(ctx context.Context, tx *store.Store) error {
		var err error
		if bind {
			err = tx.RoleBindings().Bind(ctx, tenant, b)
		} else {
			err = tx.RoleBindings().Unbind(ctx, tenant, b)
		}
		if err != nil {
			return err
		}
		return s.emitter.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
			Event: event, Actor: actor, Subject: b.SubjectID,
			Fields:  map[string]string{"holder": holder, "role": b.Role},
			Message: fmt.Sprintf("role %s %s for %s", b.Role, verb, holder),
		})
	})
	if err != nil {
		s.inventoryFailure(w, r, "role binding", err)
		return
	}
	answer(w, http.StatusOK, roleBindingView{SubjectID: b.SubjectID, GroupID: b.GroupID, Role: b.Role, GrantedBy: b.GrantedBy})
}

type claimMappingBody struct {
	Document string `json:"document"`
}

// handleClaimMappingPut writes and activates a new claim mapping version. The
// mapping decides which IdP claims become policy attributes — whoever writes it
// can grant themselves a group — so it is identity:write, the admin's, and
// audited with the versions it moved between.
func (s *Server) handleClaimMappingPut(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	var body claimMappingBody
	if err := decodeJSON(r, &body); err != nil {
		s.invalid(w, r, "body", access.ProblemInvalid, "")
		return
	}
	if strings.TrimSpace(body.Document) == "" {
		s.invalid(w, r, "document", access.ProblemRequired, "")
		return
	}
	before, err := s.mapping(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "read claim mapping", err)
		return
	}
	m, err := s.federation.PutMapping(r.Context(), tenant, []byte(body.Document), actor.Subject)
	if err != nil {
		var rejected *identity.MappingRejected
		if errors.As(err, &rejected) {
			// The mapping's own rejections, field by field, as the
			// compiler's are for a bundle (M21).
			writeError(w, http.StatusBadRequest, Error{
				Code: CodeInvalidRequest, CorrelationID: CorrelationIDFrom(r.Context()),
				Parameters: map[string]any{"field": "document", "problem": "invalid", "rejections": rejected.Rejections},
				Message:    rejected.Error(),
			})
			return
		}
		s.fail(w, r, "write claim mapping", err)
		return
	}
	beforeStr := ""
	if before != 0 {
		beforeStr = strconv.Itoa(before)
	}
	if err := s.emitter.OperatorEvent(r.Context(), nil, tenant, audit.OperatorAct{
		Event: audit.EventClaimMappingActivated, Actor: actor,
		Before: beforeStr, After: strconv.Itoa(m.Version), Fields: map[string]string{"digest": m.Digest},
		Message: fmt.Sprintf("claim mapping %d activated", m.Version),
	}); err != nil {
		s.fail(w, r, "record claim mapping", err)
		return
	}
	answer(w, http.StatusOK, map[string]any{"version": m.Version, "digest": m.Digest, "previous": before})
}

// ---------------------------------------------------------------------------
// proxies: the fleet view, enrollment and withdrawal (0006)
// ---------------------------------------------------------------------------

func (s *Server) handleProxyList(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	health, err := s.fleet.Health(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "fleet health", err)
		return
	}
	if health == nil {
		health = []fleet.ProxyHealth{}
	}
	answer(w, http.StatusOK, map[string]any{"proxies": health, "liveness": s.fleet.Liveness()})
}

type enrollmentBody struct {
	ProxyID string   `json:"proxy_id"`
	Zones   []string `json:"zones"`
	// ExpiresIn bounds how long the enrollment token is good for, in
	// seconds. Zero takes an hour.
	ExpiresIn int `json:"expires_in_seconds"`
}

// handleProxyEnrollment approves a proxy's enrollment in advance: it issues the
// one-time token the proxy presents, bound to the zones it may claim. The token
// is in the answer ONCE; only its hash is stored, and it is never logged.
func (s *Server) handleProxyEnrollment(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	var body enrollmentBody
	if err := decodeJSON(r, &body); err != nil {
		s.invalid(w, r, "body", access.ProblemInvalid, "")
		return
	}
	if strings.TrimSpace(body.ProxyID) == "" {
		s.invalid(w, r, "proxy_id", access.ProblemRequired, "")
		return
	}
	if len(body.Zones) == 0 {
		s.invalid(w, r, "zones", access.ProblemRequired, "")
		return
	}
	expiresIn := time.Hour
	if body.ExpiresIn > 0 {
		expiresIn = time.Duration(body.ExpiresIn) * time.Second
	}
	expiresAt := s.now().Add(expiresIn).UTC()
	zones := make([]fleet.Zone, 0, len(body.Zones))
	for _, z := range body.Zones {
		zones = append(zones, fleet.Zone(z))
	}
	token, err := s.fleet.IssueGrant(r.Context(), tenant, fleet.EnrollmentGrant{
		ProxyID: body.ProxyID, GrantedZones: zones, CreatedBy: actor.Principal,
	}, expiresAt)
	if err != nil {
		s.inventoryFailure(w, r, "issue enrollment", err)
		return
	}
	if err := s.emitter.OperatorEvent(r.Context(), nil, tenant, audit.OperatorAct{
		Event: audit.EventProxyApproved, Actor: actor,
		Fields: map[string]string{
			"proxy_id": body.ProxyID, "zones": strings.Join(body.Zones, ","),
			"expires_at": expiresAt.Format(time.RFC3339),
		},
		Message: "enrollment approved for proxy " + body.ProxyID,
	}); err != nil {
		s.fail(w, r, "record enrollment", err)
		return
	}
	answer(w, http.StatusCreated, map[string]any{
		"proxy_id": body.ProxyID, "zones": body.Zones, "expires_at": expiresAt, "token": token.String(),
	})
}

// handleProxyWithdraw removes a proxy from the fleet: its enrollment state
// moves to revoked, which takes it out of the graph, and every channel
// credential it holds is revoked, so it can no longer ask anything.
func (s *Server) handleProxyWithdraw(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	id := r.PathValue("proxy")
	now := s.now().UTC()
	revoked := 0
	err := s.st.InTx(r.Context(), func(ctx context.Context, tx *store.Store) error {
		p, err := tx.Proxies().Get(ctx, tenant, id)
		if err != nil {
			return err
		}
		before := string(p.State)
		p.State = store.EnrollmentRevoked
		if err := tx.Proxies().Upsert(ctx, tenant, p); err != nil {
			return err
		}
		tokens, err := tx.ProxyTokens().ListByProxy(ctx, tenant, id)
		if err != nil {
			return err
		}
		for _, t := range tokens {
			if !t.RevokedAt.IsZero() {
				continue
			}
			if err := tx.ProxyTokens().Revoke(ctx, tenant, t.TokenID, now); err != nil {
				return err
			}
			revoked++
		}
		return s.emitter.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
			Event: audit.EventProxyWithdrawn, Actor: actor, Before: before, After: string(store.EnrollmentRevoked),
			Fields:  map[string]string{"proxy_id": id, "tokens_revoked": strconv.Itoa(revoked)},
			Message: "proxy " + id + " withdrawn from the fleet",
		})
	})
	if err != nil {
		s.inventoryFailure(w, r, "withdraw proxy", err)
		return
	}
	answer(w, http.StatusOK, map[string]any{"proxy_id": id, "state": store.EnrollmentRevoked, "tokens_revoked": revoked})
}

// ---------------------------------------------------------------------------
// fleet configuration (proxy D18)
// ---------------------------------------------------------------------------

type configBody struct {
	Settings map[string]any `json:"settings"`
}

// configScope reads `{scope}/{id}`: `zone/<name>` or `proxy/<id>`.
func (s *Server) configScope(w http.ResponseWriter, r *http.Request) (store.ConfigScope, bool) {
	kind := store.ConfigScopeKind(r.PathValue("scope"))
	if kind != store.ConfigScopeZone && kind != store.ConfigScopeProxy {
		s.invalid(w, r, "scope", access.ProblemInvalid, "")
		return store.ConfigScope{}, false
	}
	return store.ConfigScope{Kind: kind, ID: r.PathValue("id")}, true
}

// handleConfigPublish publishes a scope's document. It is refused at publish
// time when it names a setting that is not fleet-owned or a value not of its
// key's shape, rather than staged as a rollout that can only report
// `rejected`; otherwise each proxy whose composed document moved is announced
// with one `config_changed`, and the answer says which the stream took.
func (s *Server) handleConfigPublish(w http.ResponseWriter, r *http.Request) {
	s.configAct(w, r, false)
}

func (s *Server) handleConfigRollback(w http.ResponseWriter, r *http.Request) {
	s.configAct(w, r, true)
}

func (s *Server) configAct(w http.ResponseWriter, r *http.Request, rollback bool) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	scope, ok := s.configScope(w, r)
	if !ok {
		return
	}
	var (
		pub   fleet.ConfigPublication
		err   error
		event = audit.EventFleetConfigPublished
	)
	if rollback {
		event = audit.EventFleetConfigRolledBack
		pub, err = s.fleet.RollbackConfig(r.Context(), tenant, scope, actor.Principal)
	} else {
		var body configBody
		if derr := decodeJSON(r, &body); derr != nil {
			s.invalid(w, r, "body", access.ProblemInvalid, "")
			return
		}
		pub, err = s.fleet.PublishConfig(r.Context(), tenant, scope, fleet.ConfigDocument(body.Settings), actor.Principal)
	}
	if err != nil {
		s.configFailure(w, r, err)
		return
	}
	changed := make([]string, 0, len(pub.Changed))
	for _, c := range pub.Changed {
		changed = append(changed, c.ProxyID)
	}
	if err := s.emitter.OperatorEvent(r.Context(), nil, tenant, audit.OperatorAct{
		Event: event, Actor: actor, After: strconv.FormatInt(pub.Version, 10),
		Fields: map[string]string{
			"scope": string(scope.Kind) + ":" + scope.ID, "changed": strings.Join(changed, ","),
			"unannounced": strconv.Itoa(pub.Unannounced()),
		},
		Message: fmt.Sprintf("fleet configuration %s:%s at version %d", scope.Kind, scope.ID, pub.Version),
	}); err != nil {
		s.fail(w, r, "record configuration", err)
		return
	}
	if pub.Changed == nil {
		pub.Changed = []fleet.ConfigAnnouncement{}
	}
	answer(w, http.StatusOK, pub)
}

func (s *Server) configFailure(w http.ResponseWriter, r *http.Request, err error) {
	id := CorrelationIDFrom(r.Context())
	var ke *fleet.ConfigKeyError
	switch {
	case errors.As(err, &ke):
		code := CodeConfigKeyNotFleetOwned
		if ke.Code == fleet.CodeConfigValueInvalid {
			code = CodeConfigValueInvalid
		}
		params := map[string]any{"key": ke.Key}
		if ke.Want != "" {
			params["want"] = ke.Want
		}
		writeError(w, http.StatusUnprocessableEntity, Error{Code: code, CorrelationID: id, Parameters: params, Message: ke.Error()})
	case errors.Is(err, fleet.ErrNoRollbackTarget):
		writeError(w, http.StatusConflict, Error{
			Code: CodeInvalidRequest, CorrelationID: id,
			Parameters: map[string]any{"field": "scope", "problem": "no_previous_version"},
			Message:    "this scope has never been republished, so there is nothing to roll back to",
		})
	default:
		s.inventoryFailure(w, r, "publish configuration", err)
	}
}

// handleConfigRollouts is the rollout view: every proxy's desired and running
// document and its reported state. `?drifted=true` narrows it to drift.
func (s *Server) handleConfigRollouts(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	var (
		rows []fleet.ConfigRollout
		err  error
	)
	if r.URL.Query().Get("drifted") == "true" {
		rows, err = s.fleet.ConfigDrift(r.Context(), tenant)
	} else {
		rows, err = s.fleet.ConfigRollouts(r.Context(), tenant)
	}
	if err != nil {
		s.fail(w, r, "configuration rollouts", err)
		return
	}
	if rows == nil {
		rows = []fleet.ConfigRollout{}
	}
	answer(w, http.StatusOK, map[string]any{"rollouts": rows, "fleet_owned_keys": fleet.FleetOwnedKeys()})
}

// ---------------------------------------------------------------------------
// what each build offers, and what each target was seen to take
// ---------------------------------------------------------------------------

// handleAlgorithmDeclarations serves each proxy's latest recorded declaration:
// every floor level's key exchanges and every profile's offer per axis
// (Hoplock/proxy#72) — the impact preview's per-build evidence.
func (s *Server) handleAlgorithmDeclarations(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	decls, err := s.fleet.AlgorithmDeclarations(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "algorithm declarations", err)
		return
	}
	if decls == nil {
		decls = []fleet.DeclaredAlgorithms{}
	}
	answer(w, http.StatusOK, map[string]any{"declarations": decls})
}

type rungObservationView struct {
	Hostname   string    `json:"target"`
	Port       int32     `json:"target_port,omitempty"`
	Platform   string    `json:"platform,omitempty"`
	Execution  []string  `json:"execution"`
	Reach      []string  `json:"reach"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
	Fresh      bool      `json:"fresh"`
	ReportedBy string    `json:"reported_by,omitempty"`
}

type kexObservationView struct {
	fleet.KexObservation
	Fresh bool `json:"fresh"`
}

// handleTargetObservations serves the two target observations side by side,
// each with its own date and freshness: the rungs a target was seen to take,
// and the key exchange it negotiated (stored by host and port).
func (s *Server) handleTargetObservations(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	now, ttl := s.now(), s.fleet.TargetObservationTTL()
	caps, err := s.st.TargetCapabilities().List(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "target capabilities", err)
		return
	}
	kex, err := s.fleet.TargetKex(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "target key exchanges", err)
		return
	}
	rungs := make([]rungObservationView, 0, len(caps))
	for _, c := range caps {
		rungs = append(rungs, rungObservationView{
			Hostname: c.Hostname, Port: c.Port, Platform: c.Platform,
			Execution: nonNilStrings(c.Execution), Reach: nonNilStrings(c.Reach),
			ObservedAt: c.ObservedAt.UTC(), ReportedBy: c.ReportedBy,
			Fresh: !c.ObservedAt.IsZero() && now.Sub(c.ObservedAt) <= ttl,
		})
	}
	kexes := make([]kexObservationView, 0, len(kex))
	for _, o := range kex {
		kexes = append(kexes, kexObservationView{KexObservation: o, Fresh: o.Fresh(now, ttl)})
	}
	answer(w, http.StatusOK, map[string]any{
		"rungs": rungs, "key_exchanges": kexes, "ttl_seconds": int64(ttl / time.Second),
		"floor_levels": []contract.AlgorithmFloor{contract.AlgorithmFloorModernKex, contract.AlgorithmFloorPQHybridKex},
	})
}

// inventoryFailure classifies a store or fleet error: an absence, a conflict, a
// row the store refused as invalid, or an outage — never a 401 (M11).
func (s *Server) inventoryFailure(w http.ResponseWriter, r *http.Request, op string, err error) {
	id := CorrelationIDFrom(r.Context())
	switch {
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: id, Message: "there is no such thing in this tenant",
		})
	case store.IsConflict(err):
		writeError(w, http.StatusConflict, Error{
			Code: CodeInvalidRequest, CorrelationID: id,
			Parameters: map[string]any{"field": "body", "problem": "conflict"},
			Message:    "this conflicts with what is already stored",
		})
	case store.IsInvalid(err):
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeInvalidRequest, CorrelationID: id,
			Parameters: map[string]any{"field": "body", "problem": "invalid"},
			Message:    "this cannot be stored: " + err.Error(),
		})
	default:
		s.fail(w, r, op, err)
	}
}
