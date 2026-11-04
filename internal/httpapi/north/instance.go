// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The deployment's own surface (0046, PLAN M19): who it is, how it is, and who
// supervises the tenant.
//
// The routes are under the tenant like every other (M18), and what they show
// across tenants is filtered to the caller: the tenant set and each node's
// per-tenant event-plane lag list only the tenants the caller may read this
// surface in, and the fleet is the path's tenant's alone. A tenant-scoped
// supervisor token therefore cannot learn another tenant's rows — nor that
// another tenant exists.

func (s *Server) instanceRoutes() []Route {
	return []Route{
		tenantRoute("GET", "/instance", identity.PermInstanceRead,
			"this deployment's identity and version set", s.handleInstance),
		tenantRoute("GET", "/instance/health", identity.PermInstanceRead,
			"health at every level: the deployment, its nodes, and this tenant's fleet", s.handleInstanceHealth),
		tenantRoute("GET", "/instance/supervisors", identity.PermInstanceRead,
			"the supervisor credentials that may act in this tenant, and their scope", s.handleSupervisors),
		tenantRoute("POST", "/instance/supervisors/{principal}/revoke", identity.PermIdentityWrite,
			"revoke a supervisor's credential in this tenant", s.handleSupervisorRevoke),
	}
}

// visibleTo is the set of tenants a caller may read this surface in.
func visibleTo(r *http.Request) func(store.Tenant) bool {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		return func(store.Tenant) bool { return false }
	}
	// Asked per tenant named in the answer, never as "every tenant the
	// caller holds": the set a request covers is the middleware's to resolve
	// (M24), and this only filters names out of an answer about one tenant.
	return func(t store.Tenant) bool { return p.Can(t, identity.PermInstanceRead) }
}

func (s *Server) instanceUnavailable(w http.ResponseWriter, r *http.Request) bool {
	if s.instance != nil {
		return false
	}
	writeError(w, http.StatusServiceUnavailable, Error{
		Code: CodeUnavailable, CorrelationID: CorrelationIDFrom(r.Context()),
		Message: "this deployment was started without its instance service",
	})
	return true
}

func (s *Server) handleInstance(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireTenant(w, r); !ok || s.instanceUnavailable(w, r) {
		return
	}
	id, err := s.instance.Identity(r.Context(), visibleTo(r))
	if err != nil {
		s.fail(w, r, "instance identity", err)
		return
	}
	answer(w, http.StatusOK, id)
}

func (s *Server) handleInstanceHealth(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok || s.instanceUnavailable(w, r) {
		return
	}
	// Health is an answer even when parts of it are unknown: a failure to
	// observe is a state in the body, never a 5xx — an operator mid-outage
	// needs the half that could be read.
	answer(w, http.StatusOK, s.instance.Health(r.Context(), []store.Tenant{tenant}, visibleTo(r)))
}

// supervisorView is one supervisor credential, as its tenant sees it.
type supervisorView struct {
	PrincipalID string `json:"principal_id"`
	DisplayName string `json:"display_name"`
	// Roles are what it holds in this tenant: its scope, in the grammar
	// every token's is.
	Roles      []string   `json:"roles"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	Live       bool       `json:"live"`
}

type supervisorsView struct {
	Supervisors []supervisorView `json:"supervisors"`
}

func (s *Server) handleSupervisors(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	rows, err := s.st.NorthPrincipals().List(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "list supervisors", err)
		return
	}
	out := supervisorsView{Supervisors: []supervisorView{}}
	now := s.now()
	for _, p := range rows {
		if p.Kind != store.PrincipalToken || !identity.IsSupervisor(p.ID) {
			continue
		}
		roles := slices.Clone(p.Scopes[string(tenant)])
		if roles == nil {
			roles = []string{}
		}
		slices.Sort(roles)
		out.Supervisors = append(out.Supervisors, supervisorView{
			PrincipalID: p.ID, DisplayName: p.DisplayName, Roles: roles,
			CreatedAt: p.CreatedAt.UTC(), LastUsedAt: instant(p.LastUsedAt),
			ExpiresAt: instant(p.ExpiresAt), RevokedAt: instant(p.RevokedAt), Live: p.Live(now),
		})
	}
	answer(w, http.StatusOK, out)
}

func (s *Server) handleSupervisorRevoke(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	id := r.PathValue("principal")
	if !identity.IsSupervisor(id) {
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "there is no such supervisor in this tenant",
		})
		return
	}
	err := s.st.InTx(r.Context(), func(ctx context.Context, tx *store.Store) error {
		if _, err := tx.NorthPrincipals().Get(ctx, tenant, id); err != nil {
			return err
		}
		if err := tx.NorthPrincipals().Revoke(ctx, tenant, id, s.now()); err != nil {
			return err
		}
		return s.emitter.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
			Event: audit.EventSupervisorRevoked, Actor: actor,
			Fields:  map[string]string{"supervisor": id},
			Message: "supervisor credential " + id + " revoked",
		})
	})
	if store.IsNotFound(err) {
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "there is no such supervisor in this tenant",
		})
		return
	}
	if err != nil {
		s.fail(w, r, "revoke supervisor", err)
		return
	}
	answer(w, http.StatusOK, map[string]any{"principal_id": id, "revoked": true})
}
