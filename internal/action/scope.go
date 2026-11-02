// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package action

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/store"
)

// A confined action (0041, M28): the scope a handler hands Control in
// ext.Action.Within, what each kind reaches, and the check between them. The
// check runs inside the transaction Execute opens, after the action's lock and
// before the ledger answers, so a repeat is checked too and nothing the check
// read can move before the act.

// The reason codes a confined action adds. They are stable.
const (
	// ReasonOutOfScope is an action refused because it reaches outside the
	// scope its requester was confined to: KindDenied.
	ReasonOutOfScope = "out_of_scope"
	// ReasonNotThisLock is an unlock with IfPlacedBy that found a lock its
	// action did not place, and left it standing: nothing to act on.
	ReasonNotThisLock = "not_this_lock"
)

// The bounds of a scope. They are this phase's own: a scope larger than this is
// a list of the tenant, which is nil.
const (
	maxScopeIDs      = 1000
	maxScopeLabels   = 64
	maxScopeLabelLen = 256
)

// What an out-of-scope refusal's record names as `outside` when it is not one
// subject or target id.
const (
	// outsideAllSubjects and outsideAllTargets are a dimension the kind
	// reaches whole, or a grant selector that lists no hostname.
	outsideAllSubjects = "all_subjects"
	outsideAllTargets  = "all_targets"
	// outsideUnrecordedTarget is a hop of a session to a hostname this server
	// holds no target row for: its decision record names no target id.
	outsideUnrecordedTarget = "unrecorded_target"
)

// The scope fields an out-of-scope refusal names, in the order they are
// checked.
const (
	scopeSubjectIDs   = "subject_ids"
	scopeTargetIDs    = "target_ids"
	scopeTargetLabels = "target_labels"
)

// scope is a validated ext.ActionScope. A nil set, or no labels, confines
// nothing on that field.
type scope struct {
	subjects map[string]bool
	targets  map[string]bool
	// labels are the required labels; labelKeys orders them, so the label a
	// refusal names is the same on every run.
	labels    map[string]string
	labelKeys []string
}

// validateScope holds a scope to its bounds (item 7): something confined, at
// most maxScopeIDs ids per list, each an identifier, and at most
// maxScopeLabels labels, each key and value one non-blank bounded line.
func validateScope(w *ext.ActionScope) (*scope, *refusal) {
	if len(w.SubjectIDs) == 0 && len(w.TargetIDs) == 0 && len(w.TargetLabels) == 0 {
		return nil, invalidField("within",
			"a scope has to confine something; an action confined to the whole tenant carries no scope at all")
	}
	s := &scope{}
	var bad *refusal
	if s.subjects, bad = idSet("within.subject_ids", w.SubjectIDs); bad != nil {
		return nil, bad
	}
	if s.targets, bad = idSet("within.target_ids", w.TargetIDs); bad != nil {
		return nil, bad
	}
	if len(w.TargetLabels) > maxScopeLabels {
		return nil, invalidField("within.target_labels", "it holds more than %d labels", maxScopeLabels)
	}
	if len(w.TargetLabels) > 0 {
		s.labels = maps.Clone(w.TargetLabels)
		s.labelKeys = slices.Sorted(maps.Keys(s.labels))
		for _, k := range s.labelKeys {
			for _, v := range []string{k, s.labels[k]} {
				switch {
				case strings.TrimSpace(v) == "":
					return nil, invalidField("within.target_labels", "a label key or value is blank")
				case len(v) > maxScopeLabelLen:
					return nil, invalidField("within.target_labels", "a label key or value is longer than %d bytes", maxScopeLabelLen)
				case hasControl(v):
					return nil, invalidField("within.target_labels", "a label key or value contains a control character")
				}
			}
		}
	}
	return s, nil
}

// idSet holds a list of ids to its bound and each id to checkToken's. An empty
// list is nil: it confines nothing.
func idSet(field string, ids []string) (map[string]bool, *refusal) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > maxScopeIDs {
		return nil, invalidField(field, "it holds more than %d ids", maxScopeIDs)
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		if bad := checkToken(field, id); bad != nil {
			return nil, bad
		}
		set[id] = true
	}
	return set, nil
}

// firstField is the scope field an unknown selector is refused on: the first
// that is set, in the order the check runs.
func (s *scope) firstField() string {
	switch {
	case s.subjects != nil:
		return scopeSubjectIDs
	case s.targets != nil:
		return scopeTargetIDs
	}
	return scopeTargetLabels
}

// outOfScope is an action that reaches outside its scope. The error names the
// scope field and never the id: the requester learns nothing it was not
// confined to, and Control's own record names what lay outside.
func outOfScope(field, outside string) *refusal {
	return &refusal{
		kind: ext.KindDenied, code: ReasonOutOfScope, scopeField: field, outside: outside,
		msg: "the action reaches outside the scope it was confined to: within." + field,
	}
}

// unknownOutOfScope is a selector Control holds nothing for, refused rather
// than answered KindNotFound, because the scope does not name it (item 5).
func unknownOutOfScope(field, selectorField, id string) *refusal {
	r := outOfScope(field, id)
	r.unknownSelector = selectorField
	return r
}

// confine checks what req reaches against the scope it was confined to,
// inside tx. A nil scope confines nothing. An action outside is a *refusal,
// which rolls the transaction back and is recorded by refuse.
func (e *Executor) confine(ctx context.Context, tx *store.Store, req request) error {
	s := req.within
	if s == nil {
		return nil
	}
	switch req.kind {
	case ext.ActionKillSession:
		if req.sessionID != "" {
			return s.session(ctx, tx, req.tenant, req.sessionID)
		}
		return s.subjectWide(req.subjectID)
	case ext.ActionLockSubject, ext.ActionUnlockSubject:
		return s.subjectWide(req.subjectID)
	case ext.ActionRevokeGrant:
		return s.grant(ctx, tx, req.tenant, req.grantID)
	case ext.ActionQuarantineTarget:
		return s.target(ctx, tx, req.tenant, req.targetID)
	case ext.ActionUnspecified:
	}
	return nil
}

// subjectWide is a kind that reaches one subject and every target: a kill by
// subject, a lock, an unlock. The subject is admitted by its id alone, so no
// read is made, and a target scope refuses it whole (item 3).
func (s *scope) subjectWide(subjectID string) error {
	if s.subjects != nil && !s.subjects[subjectID] {
		return outOfScope(scopeSubjectIDs, subjectID)
	}
	if s.targets != nil {
		return outOfScope(scopeTargetIDs, outsideAllTargets)
	}
	if len(s.labels) > 0 {
		return outOfScope(scopeTargetLabels, outsideAllTargets)
	}
	return nil
}

// session is a kill by session: every subject and every target on its decision
// records, every hop of a chain included.
func (s *scope) session(ctx context.Context, tx *store.Store, tenant store.Tenant, sessionID string) error {
	reach, err := tx.Decisions().SessionReach(ctx, tenant, sessionID)
	if err != nil {
		return err
	}
	if len(reach.SubjectIDs) == 0 && len(reach.TargetIDs) == 0 {
		return unknownOutOfScope(s.firstField(), "session_id", sessionID)
	}
	if s.subjects != nil {
		for _, id := range reach.SubjectIDs {
			if !s.subjects[id] {
				return outOfScope(scopeSubjectIDs, id)
			}
		}
	}
	if s.targets != nil {
		for _, id := range reach.TargetIDs {
			if id == "" {
				return outOfScope(scopeTargetIDs, outsideUnrecordedTarget)
			}
			if !s.targets[id] {
				return outOfScope(scopeTargetIDs, id)
			}
		}
	}
	if len(s.labels) == 0 {
		return nil
	}
	rows, err := tx.Targets().ForShare(ctx, tenant, reach.TargetIDs)
	if err != nil {
		return err
	}
	for _, id := range reach.TargetIDs {
		if id == "" {
			return outOfScope(scopeTargetLabels, outsideUnrecordedTarget)
		}
		t, ok := rows[id]
		if !ok || !s.carries(t) {
			return outOfScope(scopeTargetLabels, id)
		}
	}
	return nil
}

// target is a quarantine: one target, and every subject. The target is
// admitted by its id alone unless the scope requires labels, which are read
// from the row, held FOR SHARE so a relabel waits for the act.
func (s *scope) target(ctx context.Context, tx *store.Store, tenant store.Tenant, targetID string) error {
	if s.subjects != nil {
		return outOfScope(scopeSubjectIDs, outsideAllSubjects)
	}
	if s.targets != nil && !s.targets[targetID] {
		return outOfScope(scopeTargetIDs, targetID)
	}
	if len(s.labels) == 0 {
		return nil
	}
	rows, err := tx.Targets().ForShare(ctx, tenant, []string{targetID})
	if err != nil {
		return err
	}
	t, ok := rows[targetID]
	if !ok {
		return unknownOutOfScope(scopeTargetLabels, "target_id", targetID)
	}
	if !s.carries(t) {
		return outOfScope(scopeTargetLabels, targetID)
	}
	return nil
}

// grant is a revocation: the grant's subject, and the targets its selector can
// reach (item 4). The row is held until the act, so the check and the
// revocation see one grant.
func (s *scope) grant(ctx context.Context, tx *store.Store, tenant store.Tenant, grantID string) error {
	g, err := tx.Grants().GetForUpdate(ctx, tenant, grantID)
	switch {
	case store.IsNotFound(err):
		return unknownOutOfScope(s.firstField(), "grant_id", grantID)
	case err != nil:
		return err
	}
	if s.subjects != nil && !s.subjects[g.SubjectID] {
		return outOfScope(scopeSubjectIDs, g.SubjectID)
	}
	if s.targets == nil && len(s.labels) == 0 {
		return nil
	}
	sel, err := resolveSelector(ctx, tx, tenant, g.ScopeTargets)
	if err != nil {
		return err
	}
	if s.targets != nil {
		if outside := sel.outside(func(t store.Target) bool { return s.targets[t.ID] }); outside != "" {
			return outOfScope(scopeTargetIDs, outside)
		}
	}
	for _, k := range s.labelKeys {
		want := s.labels[k]
		if got, ok := g.ScopeLabels[k]; ok && got == want {
			// The selector's own labels are ANDed with the rest: every
			// target it reaches carries this one.
			continue
		}
		if outside := sel.outside(func(t store.Target) bool { return t.Labels[k] == want }); outside != "" {
			return outOfScope(scopeTargetLabels, outside)
		}
	}
	return nil
}

// carries reports whether a target holds every label the scope requires.
func (s *scope) carries(t store.Target) bool {
	for _, k := range s.labelKeys {
		if t.Labels[k] != s.labels[k] {
			return false
		}
	}
	return true
}

// grantSelector is a grant selector's hostnames and the target rows each names,
// read FOR SHARE.
type grantSelector struct {
	hostnames []string
	rows      map[string][]store.Target
}

// resolveSelector reads the targets a grant selector's hostnames name. A
// wildcard names no row: it reaches whatever matches it, a target created
// tomorrow included.
func resolveSelector(ctx context.Context, tx *store.Store, tenant store.Tenant, hostnames []string) (grantSelector, error) {
	sel := grantSelector{hostnames: hostnames, rows: map[string][]store.Target{}}
	var exact []string
	for _, h := range hostnames {
		if !isWildcard(h) {
			exact = append(exact, strings.ToLower(h))
		}
	}
	rows, err := tx.Targets().ForShareByHostnames(ctx, tenant, exact)
	if err != nil {
		return grantSelector{}, err
	}
	for _, t := range rows {
		h := strings.ToLower(t.Hostname)
		sel.rows[h] = append(sel.rows[h], t)
	}
	return sel, nil
}

// outside returns the first thing the selector reaches that inside does not
// admit, or "" when it reaches nothing outside: `all_targets` for a selector
// that lists no hostname (it reaches whatever a rule does, bounded by zones or
// labels at most), the hostname as written for a wildcard or for a hostname
// with no target row, and otherwise the id of the first row outside.
func (sel grantSelector) outside(inside func(store.Target) bool) string {
	if len(sel.hostnames) == 0 {
		return outsideAllTargets
	}
	for _, h := range sel.hostnames {
		if isWildcard(h) {
			return h
		}
		rows := sel.rows[strings.ToLower(h)]
		if len(rows) == 0 {
			return h
		}
		for _, t := range rows {
			if !inside(t) {
				return t.ID
			}
		}
	}
	return ""
}

func isWildcard(hostname string) bool { return strings.HasPrefix(hostname, "*.") }
