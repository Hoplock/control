// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"net/http"
	"strings"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/accessctx"
	"github.com/hoplock/control/internal/store"
)

// What is extending this deployment (M15), and the external access context's
// management (M16).
//
// THE EXTENSION LISTING IS A RENDERING, NOT A COMPUTATION: ext.Extensions.Status()
// already produces one row per point, including the points where nothing is
// registered and what Control does instead. An invisible extension is
// indistinguishable from a bug in Control, and this is the copy of the start-up
// listing somebody can reach without a shell on the host. It is read-only
// because registration is sealed before the server starts — a route that
// appeared to change it would be lying.

type extensionRegistrationView struct {
	Provider string `json:"provider"`
	Version  string `json:"version,omitempty"`
	// Default marks Control's own implementation rather than an extension.
	Default bool `json:"default"`
}

type extensionPointView struct {
	Point     string `json:"point"`
	Interface string `json:"interface"`
	Multiple  bool   `json:"multiple"`
	// Registered is false when nothing is registered; Absent then says what
	// Control does instead.
	Registered    bool                        `json:"registered"`
	Registrations []extensionRegistrationView `json:"registrations"`
	WhenAbsent    string                      `json:"when_absent"`
	Absent        string                      `json:"absent"`
	ControlShips  string                      `json:"control_ships,omitempty"`
}

func (s *Server) handleExtensions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireTenant(w, r); !ok {
		return
	}
	rows := s.extensions.Status()
	out := make([]extensionPointView, 0, len(rows))
	for _, st := range rows {
		v := extensionPointView{
			Point: st.Info.Point.String(), Interface: st.Info.Interface, Multiple: st.Info.Multiple,
			Registered: st.Registered(), Registrations: []extensionRegistrationView{},
			WhenAbsent: st.Info.WhenAbsent.String(), Absent: st.Info.Absent, ControlShips: st.Info.ControlShips,
		}
		for _, reg := range st.Registrations {
			v.Registrations = append(v.Registrations, extensionRegistrationView{
				Provider: reg.Provider, Version: reg.Version, Default: reg.Default,
			})
		}
		out = append(out, v)
	}
	answer(w, http.StatusOK, map[string]any{"points": out})
}

// ---------------------------------------------------------------------------
// external access context: providers and scope bindings (0013, M16)
// ---------------------------------------------------------------------------

type providerView struct {
	// Name is the external system's name: what a binding is filed under.
	Name     string                    `json:"name"`
	Probes   bool                      `json:"probes"`
	Pushes   bool                      `json:"pushes"`
	Registry extensionRegistrationView `json:"registration"`
}

func (s *Server) handleAccessContextProviders(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireTenant(w, r); !ok {
		return
	}
	var providers []accessctx.Provider
	if s.context != nil {
		providers = s.context.Providers().List()
	}
	out := make([]providerView, 0, len(providers))
	for _, p := range providers {
		out = append(out, providerView{
			Name: p.Info.Name, Probes: p.Info.Probes, Pushes: p.Info.Pushes,
			Registry: extensionRegistrationView{
				Provider: p.Registration.Provider, Version: p.Registration.Version, Default: p.Registration.Default,
			},
		})
	}
	answer(w, http.StatusOK, map[string]any{"providers": out})
}

// bindingView is a binding rendered whole, push principals by principal id.
type bindingView struct {
	Provider       string            `json:"provider"`
	Mode           string            `json:"mode"`
	Scope          string            `json:"scope"`
	Subjects       []string          `json:"subjects"`
	SubjectGroups  []string          `json:"subject_groups"`
	Targets        []string          `json:"targets"`
	TargetLabels   map[string]string `json:"target_labels"`
	TargetZones    []string          `json:"target_zones"`
	MaxWindow      string            `json:"max_window"`
	Privileged     bool              `json:"privileged"`
	PushPrincipals []string          `json:"push_principals"`
	Enabled        bool              `json:"enabled"`
	Description    string            `json:"description,omitempty"`
	UpdatedBy      actorView         `json:"updated_by"`
	CreatedAt      time.Time         `json:"created_at,omitzero"`
	UpdatedAt      time.Time         `json:"updated_at,omitzero"`
}

func viewBinding(b store.AccessContextBinding) bindingView {
	return bindingView{
		Provider: b.Provider, Mode: string(b.Mode), Scope: b.Scope,
		Subjects: nonNilStrings(b.Subjects), SubjectGroups: nonNilStrings(b.SubjectGroups),
		Targets: nonNilStrings(b.Targets), TargetLabels: nonNilLabels(b.TargetLabels),
		TargetZones: nonNilStrings(b.TargetZones), MaxWindow: b.MaxWindow.String(), Privileged: b.Privileged,
		PushPrincipals: nonNilStrings(b.PushPrincipals), Enabled: b.Enabled, Description: b.Description,
		UpdatedBy: viewActor(b.UpdatedBy), CreatedAt: b.CreatedAt.UTC(), UpdatedAt: b.UpdatedAt.UTC(),
	}
}

type bindingBody struct {
	Mode           string            `json:"mode"`
	Scope          string            `json:"scope"`
	Subjects       []string          `json:"subjects"`
	SubjectGroups  []string          `json:"subject_groups"`
	Targets        []string          `json:"targets"`
	TargetLabels   map[string]string `json:"target_labels"`
	TargetZones    []string          `json:"target_zones"`
	MaxWindow      string            `json:"max_window"`
	Privileged     bool              `json:"privileged"`
	PushPrincipals []string          `json:"push_principals"`
	Enabled        *bool             `json:"enabled"`
	Description    string            `json:"description"`
}

// requireContext answers a binding route on a deployment that runs no
// external-context integration exactly as an unknown provider is answered.
func (s *Server) requireContext(w http.ResponseWriter, r *http.Request) bool {
	if s.context != nil {
		return true
	}
	writeError(w, http.StatusNotFound, Error{
		Code: CodeProviderNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
		Message: "this deployment runs no external access-context provider",
	})
	return false
}

func (s *Server) handleBindingList(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	if !s.requireContext(w, r) {
		return
	}
	rows, err := s.context.Bindings(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "list bindings", err)
		return
	}
	out := make([]bindingView, 0, len(rows))
	for _, b := range rows {
		out = append(out, viewBinding(b))
	}
	answer(w, http.StatusOK, map[string]any{"bindings": out})
}

func (s *Server) handleBindingGet(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	if !s.requireContext(w, r) {
		return
	}
	b, err := s.context.Binding(r.Context(), tenant, r.PathValue("provider"))
	if err != nil {
		s.bindingFailure(w, r, "read binding", err)
		return
	}
	answer(w, http.StatusOK, viewBinding(b))
}

// handleBindingPut writes a provider's binding over accessctx.Service.PutBinding,
// which validates it against the providers this server runs and audits it in
// its own transaction; its refusals are answered field by field (M21).
func (s *Server) handleBindingPut(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorFrom(w, r)
	if !ok {
		return
	}
	if !s.requireContext(w, r) {
		return
	}
	var body bindingBody
	if err := decodeJSON(r, &body); err != nil {
		s.invalid(w, r, "body", access.ProblemInvalid, "")
		return
	}
	b := store.AccessContextBinding{
		Provider: r.PathValue("provider"), Mode: store.ExternalMode(body.Mode), Scope: body.Scope,
		Subjects: body.Subjects, SubjectGroups: body.SubjectGroups, Targets: body.Targets,
		TargetLabels: body.TargetLabels, TargetZones: body.TargetZones, Privileged: body.Privileged,
		PushPrincipals: body.PushPrincipals, Enabled: true, Description: body.Description,
	}
	if body.Enabled != nil {
		b.Enabled = *body.Enabled
	}
	if raw := strings.TrimSpace(body.MaxWindow); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			s.invalid(w, r, "max_window", access.ProblemInvalid, "")
			return
		}
		b.MaxWindow = d
	}
	stored, err := s.context.PutBinding(r.Context(), tenant, actor, b)
	if err != nil {
		s.bindingFailure(w, r, "write binding", err)
		return
	}
	answer(w, http.StatusOK, viewBinding(stored))
}

func (s *Server) handleBindingDelete(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorFrom(w, r)
	if !ok {
		return
	}
	if !s.requireContext(w, r) {
		return
	}
	deleted, err := s.context.DeleteBinding(r.Context(), tenant, actor, r.PathValue("provider"))
	if err != nil {
		s.bindingFailure(w, r, "delete binding", err)
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, Error{
			Code: CodeBindingNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "this tenant has no binding for that provider",
		})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) bindingFailure(w http.ResponseWriter, r *http.Request, op string, err error) {
	if store.IsNotFound(err) {
		writeError(w, http.StatusNotFound, Error{
			Code: CodeBindingNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "this tenant has no binding for that provider",
		})
		return
	}
	s.grantFailure(w, r, op, err)
}
