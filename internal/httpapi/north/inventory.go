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

// targetList is the target list (M26): by target id, the primary key.
var targetList = list{name: "targets", key: []keyKind{keyString}}

// handleTargetList lists targets, filtered by `?label=key=value`, repeatable
// and ANDed, one page at a time (M26).
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
	// The cursor binds the filter the reader applies, so a repeated key
	// binds the value that won, not each value asked.
	filters := make([]string, 0, len(filter))
	for k, v := range filter {
		filters = append(filters, "label="+k+"="+v)
	}
	p, ok := readPage(w, r, targetList, filters...)
	if !ok {
		return
	}
	page, err := s.st.Targets().Page(r.Context(), tenant, filter, p.Page)
	if err != nil {
		s.fail(w, r, "list targets", err)
		return
	}
	out := make([]targetView, 0, len(page.Rows))
	var last []any
	for _, t := range page.Rows {
		out = append(out, viewTarget(t))
	}
	if n := len(page.Rows); n > 0 {
		last = []any{page.Rows[n-1].ID}
	}
	s.answerPage(w, r, p, "targets", out, page.More, last, nil)
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
		s.refuseBody(w, r, err)
		return
	}
	if strings.TrimSpace(body.Hostname) == "" {
		s.invalid(w, r, "hostname", access.ProblemRequired, "")
		return
	}
	id := r.PathValue("target")
	var stored store.Target
	err := s.st.InTx(r.Context(), func(ctx context.Context, tx *store.Store) error {
		// Under the target lock, the row read is the row replaced.
		if err := tx.Targets().Lock(ctx, tenant); err != nil {
			return err
		}
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
		if err := tx.Targets().Lock(ctx, tenant); err != nil {
			return err
		}
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

// relabelDiffOf is one change's diff against a target as read.
func relabelDiffOf(t store.Target, labels map[string]string) relabelDiff {
	added, removed, changed := diffLabels(t.Labels, labels)
	return relabelDiff{
		Target: t.ID, Hostname: t.Hostname, Before: nonNilLabels(t.Labels), After: nonNilLabels(labels),
		Added: added, Removed: removed, Changed: changed,
	}
}

// relabelFailure answers a relabel's failure, naming the target that is not
// there when that is what failed: a 404 among many changes says which.
func (s *Server) relabelFailure(w http.ResponseWriter, r *http.Request, target string, err error) {
	if store.IsNotFound(err) && target != "" {
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
			Parameters: map[string]any{"target": target},
			Message:    "there is no such target in this tenant",
		})
		return
	}
	s.inventoryFailure(w, r, "relabel targets", err)
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
		s.refuseBody(w, r, err)
		return
	}
	if len(body.Changes) == 0 {
		s.invalid(w, r, "changes", access.ProblemRequired, "")
		return
	}
	seen := map[string]bool{}
	for i, c := range body.Changes {
		field := fmt.Sprintf("changes[%d].target", i)
		switch {
		case strings.TrimSpace(c.Target) == "":
			s.invalid(w, r, field, access.ProblemRequired, "")
			return
		case seen[c.Target]:
			// Two changes to one target would simulate one and write
			// the other.
			s.invalid(w, r, field, "repeated", "")
			return
		}
		seen[c.Target] = true
	}

	diffs := make([]relabelDiff, 0, len(body.Changes))
	override := decision.LabelOverride{}
	for _, c := range body.Changes {
		t, err := s.st.Targets().Get(r.Context(), tenant, c.Target)
		if err != nil {
			s.relabelFailure(w, r, c.Target, err)
			return
		}
		diffs = append(diffs, relabelDiffOf(t, c.Labels))
		override[t.Hostname] = nonNilLabels(c.Labels)
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
		// The simulation above may have taken a while. The write re-reads
		// every target under the tenant's target lock and changes only its
		// labels, so a hostname, zone or credential method written in the
		// meantime is kept, and the record's before is what it replaced.
		var missing string
		err := s.st.InTx(r.Context(), func(ctx context.Context, tx *store.Store) error {
			if err := tx.Targets().Lock(ctx, tenant); err != nil {
				return err
			}
			applied := make([]relabelDiff, 0, len(body.Changes))
			ids := make([]string, 0, len(body.Changes))
			for _, c := range body.Changes {
				t, err := tx.Targets().Get(ctx, tenant, c.Target)
				if err != nil {
					missing = c.Target
					return err
				}
				applied = append(applied, relabelDiffOf(t, c.Labels))
				before := t.Labels
				t.Labels = c.Labels
				if err := tx.Targets().Upsert(ctx, tenant, t); err != nil {
					return err
				}
				ids = append(ids, t.ID)
				if err := s.emitter.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
					Event: audit.EventTargetPut, Actor: actor, Target: t.Hostname,
					Before: labelString(before), After: labelString(t.Labels),
					Fields:  map[string]string{"target_id": t.ID, "relabel": "true"},
					Message: "target " + t.ID + " relabelled",
				}); err != nil {
					return err
				}
			}
			out["changes"] = applied
			return s.emitter.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
				Event: audit.EventTargetsRelabelled, Actor: actor,
				Fields:  map[string]string{"targets": strings.Join(ids, ","), "count": strconv.Itoa(len(ids))},
				Message: fmt.Sprintf("%d target(s) relabelled", len(ids)),
			})
		})
		if err != nil {
			s.relabelFailure(w, r, missing, err)
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

// subjectList is the subject list (M26): by subject id, the primary key.
var subjectList = list{name: "subjects", key: []keyKind{keyString}}

func (s *Server) handleSubjectList(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	p, ok := readPage(w, r, subjectList)
	if !ok {
		return
	}
	page, err := s.st.Subjects().Page(r.Context(), tenant, p.Page)
	if err != nil {
		s.fail(w, r, "list subjects", err)
		return
	}
	out := make([]subjectView, 0, len(page.Rows))
	var last []any
	for _, sub := range page.Rows {
		out = append(out, viewSubject(sub))
	}
	if n := len(page.Rows); n > 0 {
		last = []any{page.Rows[n-1].ID}
	}
	s.answerPage(w, r, p, "subjects", out, page.More, last, nil)
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
		s.refuseBody(w, r, err)
		return
	}
	// A stored claim is a policy attribute on every decision for this
	// subject, so it is held to the claim mapping's rule: a name this
	// server sets for itself, chain_hop_proxy_id above all, is never
	// written here either (0023).
	for _, name := range slices.Sorted(maps.Keys(body.Claims)) {
		if problem := identity.AttributeProblem(name); problem != "" {
			s.invalid(w, r, "claims."+name, problem, "")
			return
		}
	}
	id := r.PathValue("subject")
	source := strings.TrimSpace(body.Source)
	if source == "" {
		source = identity.SourceLocal
	}
	var stored store.Subject
	err := s.st.InTx(r.Context(), func(ctx context.Context, tx *store.Store) error {
		// Under the subject lock, the row read is the row replaced.
		if err := tx.Subjects().Lock(ctx, tenant); err != nil {
			return err
		}
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
		if err := tx.Subjects().Lock(ctx, tenant); err != nil {
			return err
		}
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
		s.refuseBody(w, r, err)
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
		s.refuseBody(w, r, err)
		return
	}
	if strings.TrimSpace(body.Document) == "" {
		s.invalid(w, r, "document", access.ProblemRequired, "")
		return
	}
	// The version is stored, activated and recorded in one transaction,
	// with the version it replaced read under the tenant's lock.
	previous := 0
	m, err := s.federation.PutMapping(r.Context(), tenant, []byte(body.Document), actor.Subject,
		func(ctx context.Context, tx *store.Store, before int, m *identity.Mapping) error {
			previous = before
			act := audit.OperatorAct{
				Event: audit.EventClaimMappingActivated, Actor: actor,
				After: strconv.Itoa(m.Version), Fields: map[string]string{"digest": m.Digest},
				Message: fmt.Sprintf("claim mapping %d activated", m.Version),
			}
			if before != 0 {
				act.Before = strconv.Itoa(before)
			}
			return s.emitter.OperatorEvent(ctx, tx, tenant, act)
		})
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
		s.inventoryFailure(w, r, "write claim mapping", err)
		return
	}
	answer(w, http.StatusOK, map[string]any{"version": m.Version, "digest": m.Digest, "previous": previous})
}

// ---------------------------------------------------------------------------
// proxies: the fleet view, enrollment and withdrawal (0006)
// ---------------------------------------------------------------------------

// proxyList is the fleet view (M26): by proxy id, the primary key.
var proxyList = list{name: "proxies", key: []keyKind{keyString}}

// handleProxyList is the fleet view, one page at a time (M26). `liveness`, the
// staleness rule every row was judged by, is the same on every page.
func (s *Server) handleProxyList(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	p, ok := readPage(w, r, proxyList)
	if !ok {
		return
	}
	page, err := s.fleet.HealthPage(r.Context(), tenant, p.Page)
	if err != nil {
		s.fail(w, r, "fleet health", err)
		return
	}
	var last []any
	if n := len(page.Rows); n > 0 {
		last = []any{page.Rows[n-1].ProxyID}
	}
	s.answerPage(w, r, p, "proxies", page.Rows, page.More, last, map[string]any{"liveness": s.fleet.Liveness()})
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
		s.refuseBody(w, r, err)
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
	// The grant and its record commit together.
	token, err := s.fleet.IssueGrant(r.Context(), tenant, fleet.EnrollmentGrant{
		ProxyID: body.ProxyID, GrantedZones: zones, CreatedBy: actor.Principal,
	}, expiresAt, func(ctx context.Context, tx *store.Store) error {
		return s.emitter.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
			Event: audit.EventProxyApproved, Actor: actor,
			Fields: map[string]string{
				"proxy_id": body.ProxyID, "zones": strings.Join(body.Zones, ","),
				"expires_at": expiresAt.Format(time.RFC3339),
			},
			Message: "enrollment approved for proxy " + body.ProxyID,
		})
	})
	if err != nil {
		s.inventoryFailure(w, r, "issue enrollment", err)
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
	event := audit.EventFleetConfigPublished
	if rollback {
		event = audit.EventFleetConfigRolledBack
	}
	// The publication and its record commit together, before anything is
	// announced: the record states the act, and the answer says which
	// announcements the stream took.
	record := func(ctx context.Context, tx *store.Store, rec fleet.ConfigRecord) error {
		act := audit.OperatorAct{
			Event: event, Actor: actor, After: strconv.FormatInt(rec.Version, 10),
			Fields: map[string]string{
				"scope": string(scope.Kind) + ":" + scope.ID, "changed": strings.Join(rec.Changed, ","),
			},
			Message: fmt.Sprintf("fleet configuration %s:%s at version %d", scope.Kind, scope.ID, rec.Version),
		}
		if rec.Before != 0 {
			act.Before = strconv.FormatInt(rec.Before, 10)
		}
		return s.emitter.OperatorEvent(ctx, tx, tenant, act)
	}
	var (
		pub fleet.ConfigPublication
		err error
	)
	if rollback {
		pub, err = s.fleet.RollbackConfig(r.Context(), tenant, scope, actor.Principal, record)
	} else {
		var body configBody
		if derr := decodeJSON(r, &body); derr != nil {
			s.refuseBody(w, r, derr)
			return
		}
		pub, err = s.fleet.PublishConfig(r.Context(), tenant, scope, fleet.ConfigDocument(body.Settings), actor.Principal, record)
	}
	if err != nil {
		s.configFailure(w, r, err)
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
		message := fmt.Sprintf("%q is not a fleet-owned setting: the proxy keeps it in its bootstrap file and rejects a document naming it", ke.Key)
		if ke.Want != "" {
			params["want"] = ke.Want
		}
		if code == CodeConfigValueInvalid {
			message = fmt.Sprintf("setting %q must be %s", ke.Key, ke.Want)
		}
		writeError(w, http.StatusUnprocessableEntity, Error{Code: code, CorrelationID: id, Parameters: params, Message: message})
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

// rolloutList is the rollout view (M26): by proxy id, the primary key. Its one
// filter is `drifted=true`, so a cursor from one view is refused on the other.
var rolloutList = list{name: "rollouts", key: []keyKind{keyString}}

// handleConfigRollouts is the rollout view, one page at a time (M26): each
// proxy's desired and running document and its reported state.
// `?drifted=true` narrows it to drift, and any other value of it is the whole
// view. `fleet_owned_keys` is the same on every page.
func (s *Server) handleConfigRollouts(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	drifted := r.URL.Query().Get("drifted") == "true"
	var filters []string
	if drifted {
		filters = append(filters, "drifted=true")
	}
	p, ok := readPage(w, r, rolloutList, filters...)
	if !ok {
		return
	}
	page, err := s.fleet.ConfigRolloutPage(r.Context(), tenant, drifted, p.Page)
	if err != nil {
		s.fail(w, r, "configuration rollouts", err)
		return
	}
	var last []any
	if n := len(page.Rows); n > 0 {
		last = []any{page.Rows[n-1].ProxyID}
	}
	s.answerPage(w, r, p, "rollouts", page.Rows, page.More, last, map[string]any{"fleet_owned_keys": fleet.FleetOwnedKeys()})
}

// ---------------------------------------------------------------------------
// what each build offers, and what each target was seen to take
// ---------------------------------------------------------------------------

// declarationList is the algorithm declarations (M26): by proxy id, the
// primary key.
var declarationList = list{name: "declarations", key: []keyKind{keyString}}

// handleAlgorithmDeclarations serves each proxy's latest recorded declaration,
// one page at a time (M26): every floor level's key exchanges and every
// profile's offer per axis (Hoplock/proxy#72) — the impact preview's per-build
// evidence.
func (s *Server) handleAlgorithmDeclarations(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	p, ok := readPage(w, r, declarationList)
	if !ok {
		return
	}
	page, err := s.fleet.AlgorithmDeclarationPage(r.Context(), tenant, p.Page)
	if err != nil {
		s.fail(w, r, "algorithm declarations", err)
		return
	}
	var last []any
	if n := len(page.Rows); n > 0 {
		last = []any{page.Rows[n-1].ProxyID}
	}
	s.answerPage(w, r, p, "declarations", page.Rows, page.More, last, nil)
}

// A target's two observations are two lists, keyed differently (M26.5): the
// rungs by hostname, port and platform, the key exchange by hostname and port,
// because the proxy's report of it names no platform. Each item carries its
// own date and its own freshness, judged in the one window `ttl_seconds`
// states on every page.

// rungList is the rung observations (M26): the primary key.
var rungList = list{name: "rungs", key: []keyKind{keyString, keyInt32, keyString}}

// kexList is the key-exchange observations (M26): the primary key.
var kexList = list{name: "key_exchanges", key: []keyKind{keyString, keyInt32}}

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

// handleTargetRungs serves the rungs each target was seen to take, one page at
// a time (M26).
func (s *Server) handleTargetRungs(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	p, ok := readPage(w, r, rungList)
	if !ok {
		return
	}
	now, ttl := s.now(), s.fleet.TargetObservationTTL()
	page, err := s.st.TargetCapabilities().Page(r.Context(), tenant, p.Page)
	if err != nil {
		s.fail(w, r, "target rung observations", err)
		return
	}
	rungs := make([]rungObservationView, 0, len(page.Rows))
	for _, c := range page.Rows {
		rungs = append(rungs, rungObservationView{
			Hostname: c.Hostname, Port: c.Port, Platform: c.Platform,
			Execution: nonNilStrings(c.Execution), Reach: nonNilStrings(c.Reach),
			ObservedAt: c.ObservedAt.UTC(), ReportedBy: c.ReportedBy,
			Fresh: !c.ObservedAt.IsZero() && now.Sub(c.ObservedAt) <= ttl,
		})
	}
	var last []any
	if n := len(page.Rows); n > 0 {
		c := page.Rows[n-1]
		last = []any{c.Hostname, int64(c.Port), c.Platform}
	}
	s.answerPage(w, r, p, "rungs", rungs, page.More, last, map[string]any{"ttl_seconds": int64(ttl / time.Second)})
}

// handleTargetKeyExchanges serves the key exchange each target was seen to
// negotiate, one page at a time (M26), beside the floor levels a route may
// name, so a reader can place each observation against a candidate floor.
func (s *Server) handleTargetKeyExchanges(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	p, ok := readPage(w, r, kexList)
	if !ok {
		return
	}
	now, ttl := s.now(), s.fleet.TargetObservationTTL()
	page, err := s.fleet.TargetKexPage(r.Context(), tenant, p.Page)
	if err != nil {
		s.fail(w, r, "target key exchanges", err)
		return
	}
	kexes := make([]kexObservationView, 0, len(page.Rows))
	for _, o := range page.Rows {
		kexes = append(kexes, kexObservationView{KexObservation: o, Fresh: o.Fresh(now, ttl)})
	}
	var last []any
	if n := len(page.Rows); n > 0 {
		o := page.Rows[n-1]
		last = []any{o.Hostname, int64(o.Port)}
	}
	s.answerPage(w, r, p, "key_exchanges", kexes, page.More, last, map[string]any{
		"ttl_seconds":  int64(ttl / time.Second),
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
		// The store's own sentence, never err.Error(): that names the Go
		// operation that refused, and a database refusal's text names its
		// tables and constraints (0023).
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeInvalidRequest, CorrelationID: id,
			Parameters: map[string]any{"field": "body", "problem": "invalid"},
			Message:    "this cannot be stored: " + store.Reason(err),
		})
	default:
		s.fail(w, r, op, err)
	}
}
