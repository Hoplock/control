// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The federation and session handlers.
//
// THE ANONYMOUS ROUTES RESOLVE A TENANT WITHOUT A PRINCIPAL, and that is not a
// hole in M18. M18's rule is that a caller may not act in a tenant it was not
// granted; these routes act in none — they start a login, or they list the
// sign-in methods a login page has to render before anybody is signed in. What
// they can do is confirm that a tenant exists, which is why a deployment with
// one tenant answers them without a selector at all, and why they disclose only
// the connector names an operator chose to put on a login page.

// anonymousTenant resolves the tenant for a pre-authentication route.
//
// With one tenant configured, no route gains a required parameter and no caller
// has to know tenancy exists (M18). With several, a login page names the one it
// is rendering.
func (s *Server) anonymousTenant(r *http.Request) (store.Tenant, bool) {
	if t, ok := selectedTenant(r); ok {
		return t, true
	}
	if s.defaultTenant != "" {
		return s.defaultTenant, true
	}
	return "", false
}

type signInMethodsResponse struct {
	Tenant  string                      `json:"tenant"`
	Methods []identity.ConnectorSummary `json:"methods"`
	// LocalLogin reports whether the break-glass path is available. A login
	// page renders it differently on purpose: it is not a normal way in
	// (M7).
	LocalLogin bool `json:"local_login"`
}

func (s *Server) handleSignInMethods(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.anonymousTenant(r)
	if !ok {
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeTenantAmbiguous, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "this deployment serves several tenants, so the request must name one",
		})
		return
	}
	methods, err := s.federation.Connectors(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "list connectors", err)
		return
	}
	answer(w, http.StatusOK, signInMethodsResponse{
		Tenant:     tenant.String(),
		Methods:    methods,
		LocalLogin: true,
	})
}

type federationStartResponse struct {
	// RedirectURL is where the browser goes next.
	RedirectURL string `json:"redirect_url"`
	// State is returned so a single-page console can correlate its own
	// navigation. It is not a credential: the flow row is what makes it
	// single use.
	State string `json:"state"`
}

func (s *Server) handleFederationStart(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.anonymousTenant(r)
	if !ok {
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeTenantAmbiguous, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "this deployment serves several tenants, so the request must name one",
		})
		return
	}
	connector := r.PathValue("connector")

	started, err := s.federation.Begin(r.Context(), tenant, connector, r.URL.Query().Get("login_hint"))
	if err != nil {
		if be, refusal := identity.BrokerRefusal(err); refusal {
			s.refused(w, r, be)
			return
		}
		s.fail(w, r, "begin federated login", err)
		return
	}
	answer(w, http.StatusOK, federationStartResponse{
		RedirectURL: started.RedirectURL,
		State:       started.State,
	})
}

// handleFederationCallback completes a login, on either binding.
//
// One handler for the OIDC callback and the SAML ACS, because above the broker
// seam the two are the same operation: parameters in, a session out. The
// parameters arrive in a query string on one and a form body on the other, and
// that difference is the whole of what this function knows about the protocols.
func (s *Server) handleFederationCallback(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.anonymousTenant(r)
	if !ok {
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeTenantAmbiguous, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "this deployment serves several tenants, so the request must name one",
		})
		return
	}
	connector := r.PathValue("connector")

	params, err := callbackParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeInvalidRequest, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "this callback could not be read",
		})
		return
	}

	session, err := s.federation.Complete(r.Context(), tenant, connector, params,
		CorrelationIDFrom(r.Context()))
	if err != nil {
		if be, refusal := identity.BrokerRefusal(err); refusal {
			s.refused(w, r, be)
			return
		}
		s.fail(w, r, "complete federated login", err)
		return
	}
	s.issueSession(w, r, session)
}

func callbackParams(r *http.Request) (url.Values, error) {
	if r.Method == http.MethodGet {
		return r.URL.Query(), nil
	}
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	return r.PostForm, nil
}

func (s *Server) handleFederationMetadata(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.anonymousTenant(r)
	if !ok {
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeTenantAmbiguous, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "this deployment serves several tenants, so the request must name one",
		})
		return
	}
	contentType, body, err := s.federation.Metadata(r.Context(), tenant, r.PathValue("connector"))
	if err != nil {
		if be, refusal := identity.BrokerRefusal(err); refusal {
			s.refused(w, r, be)
			return
		}
		s.fail(w, r, "render connector metadata", err)
		return
	}
	if len(body) == 0 {
		// An OIDC connector publishes nothing: there is nothing an OIDC
		// IdP reads from this server.
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "that sign-in method publishes no metadata",
		})
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

type localLoginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

// handleLocalLogin is the break-glass path (M7).
//
// It is available unconditionally because the case it exists for is "the IdP is
// down": a break-glass login behind a feature flag an operator has to reach the
// console to set is not break-glass. What makes it safe is not that it is hard
// to reach but that it is impossible to hide — the flag is on the principal, in
// the access log, and in the audit record, and the record is written before the
// credential exists to be used.
func (s *Server) handleLocalLogin(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.anonymousTenant(r)
	if !ok {
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeTenantAmbiguous, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "this deployment serves several tenants, so the request must name one",
		})
		return
	}

	var body localLoginRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeInvalidRequest, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "this request body could not be read",
		})
		return
	}
	if body.Login == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeInvalidRequest, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "a login and a password are both required",
		})
		return
	}

	session, err := s.federation.LocalLogin(r.Context(), tenant, body.Login, body.Password,
		CorrelationIDFrom(r.Context()))
	if err != nil {
		if be, refusal := identity.BrokerRefusal(err); refusal {
			s.refused(w, r, be)
			return
		}
		s.fail(w, r, "local login", err)
		return
	}
	s.issueSession(w, r, session)
}

type sessionResponse struct {
	// Credential is returned ONCE, and only to the caller that just
	// authenticated. A browser gets the cookie instead and can ignore it.
	Credential string `json:"credential"`
	Session    whoAmI `json:"session"`
}

// issueSession hands back a new session credential.
//
// Both a cookie and a body field, because both kinds of client exist: the
// console is a browser and wants an HttpOnly cookie it cannot leak through
// script, while `policyctl` and CI want a string. The cookie is HttpOnly,
// SameSite=Lax and (unless a deployment has opted out for local development)
// Secure.
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, session identity.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    session.Credential.String(),
		Path:     "/",
		Expires:  session.ExpiresAt,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	answer(w, http.StatusOK, sessionResponse{
		Credential: session.Credential.String(),
		Session:    describePrincipal(session.Principal, session.ExpiresAt),
	})
}

// whoAmI is what a caller is told about itself.
type whoAmI struct {
	PrincipalID string              `json:"principal_id"`
	Kind        store.PrincipalKind `json:"kind"`
	Subject     string              `json:"subject,omitempty"`
	DisplayName string              `json:"display_name,omitempty"`
	Source      string              `json:"source"`
	// BreakGlass is disclosed so the console can SAY SO. A break-glass
	// session that looks like a normal one in the UI is the same failure as
	// one that looks like a normal one in the audit record.
	BreakGlass bool `json:"break_glass"`
	// MappingVersion names the claim mapping that produced the attributes,
	// so that "why am I in this group" has an answer a person can follow.
	MappingVersion int      `json:"mapping_version,omitempty"`
	Groups         []string `json:"groups,omitempty"`
	// Scopes is tenant -> roles. A console renders it to hide what the
	// caller cannot do, which is a courtesy and never the control: the
	// control is the middleware.
	Scopes    map[string]scopeView `json:"scopes"`
	ExpiresAt time.Time            `json:"expires_at,omitzero"`
}

type scopeView struct {
	Roles       []string              `json:"roles"`
	Permissions []identity.Permission `json:"permissions"`
}

func describePrincipal(p *identity.Principal, expires time.Time) whoAmI {
	out := whoAmI{
		PrincipalID:    p.ID,
		Kind:           p.Kind,
		Subject:        p.Subject,
		DisplayName:    p.DisplayName,
		Source:         p.Source,
		BreakGlass:     p.BreakGlass,
		MappingVersion: p.MappingVersion,
		Groups:         p.Groups,
		Scopes:         map[string]scopeView{},
		ExpiresAt:      expires,
	}
	for _, tenant := range p.ScopedTenants() {
		roles := p.RolesIn(tenant)
		out.Scopes[tenant.String()] = scopeView{
			Roles:       roles.Strings(),
			Permissions: roles.Permissions(),
		}
	}
	return out
}

func (s *Server) handleWhoAmI(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		s.fail(w, r, "who am i", errNoPrincipal)
		return
	}
	answer(w, http.StatusOK, describePrincipal(p, time.Time{}))
}

func (s *Server) handleEndSession(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		s.fail(w, r, "end session", errNoPrincipal)
		return
	}
	if err := s.federation.EndSession(r.Context(), p.Tenant, p.ID); err != nil {
		if store.IsNotFound(err) {
			// The caller's intent is satisfied either way, and a
			// session that is already gone is not an error worth
			// telling a browser about.
			s.clearCookie(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.fail(w, r, "end session", err)
		return
	}
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

// decodeJSON reads a request body strictly. An unknown field is refused rather
// than ignored: a client sending `passwd` instead of `password` should be told
// so, not authenticated with an empty one.
func decodeJSON(r *http.Request, into any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	// A second document in the body is a client this server does not
	// understand.
	if err := dec.Decode(&json.RawMessage{}); err != io.EOF {
		return errTrailingBody
	}
	return nil
}
