// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import "github.com/hoplock/control/internal/identity"

// The route table.
//
// It is ONE function so that the whole of what this surface serves, and what
// each route requires, is readable in one sitting. 0014 appends to it; nothing
// registers a route from anywhere else, because a route registered elsewhere is
// a route the isolation test would have to be told about.
//
// The prefix is `/api/` rather than `/v1/`: `/v1/` is the CONTRACT's namespace
// on the other listener (M1), and two surfaces that never share a port should
// not share a path shape either — an operator reading a log line should be able
// to tell which listener answered it from the path alone.
const (
	// APIPrefix is where this surface lives.
	APIPrefix = "/api/v1"
	// SessionCookie is where a browser keeps its session credential.
	SessionCookie = "hoplock_session"
)

func (s *Server) routes() []Route {
	return []Route{
		// --- federation: the routes that run BEFORE authentication ---
		{
			Method: "GET", Pattern: APIPrefix + "/session/methods",
			Access:  AccessAnonymous,
			Summary: "list a tenant's sign-in methods",
			Handler: s.handleSignInMethods,
		},
		{
			Method: "POST", Pattern: APIPrefix + "/session/federated/{connector}/start",
			Access:  AccessAnonymous,
			Summary: "start a federated login",
			Handler: s.handleFederationStart,
		},
		{
			Method: "GET", Pattern: APIPrefix + "/session/federated/{connector}/callback",
			Access:  AccessAnonymous,
			Summary: "complete an OIDC login",
			Handler: s.handleFederationCallback,
		},
		{
			Method: "POST", Pattern: APIPrefix + "/session/federated/{connector}/acs",
			Access:  AccessAnonymous,
			Summary: "complete a SAML login",
			Handler: s.handleFederationCallback,
		},
		{
			Method: "GET", Pattern: APIPrefix + "/session/federated/{connector}/metadata",
			Access:  AccessAnonymous,
			Summary: "this server's SAML SP metadata",
			Handler: s.handleFederationMetadata,
		},
		{
			Method: "POST", Pattern: APIPrefix + "/session/local",
			Access:  AccessAnonymous,
			Summary: "break-glass local login (M7)",
			Handler: s.handleLocalLogin,
		},

		// --- the caller's own session ---
		{
			Method: "GET", Pattern: APIPrefix + "/session",
			Access:  AccessAuthenticated,
			Summary: "who am I, and what may I do where",
			Handler: s.handleWhoAmI,
		},
		{
			Method: "DELETE", Pattern: APIPrefix + "/session",
			Access:  AccessAuthenticated,
			Summary: "end this session",
			Handler: s.handleEndSession,
		},

		// --- the certificate authority (proxy D6a) ---
		{
			Method: "GET", Pattern: APIPrefix + "/tenants/{tenant}/ca",
			Access:     AccessTenant,
			Permission: identity.PermCARead,
			Summary:    "the CA's active key and the trust bundle to publish",
			Handler:    s.handleCADescribe,
		},
		{
			Method: "POST", Pattern: APIPrefix + "/tenants/{tenant}/ca/rotate",
			Access:     AccessTenant,
			Permission: identity.PermCARotate,
			Summary:    "rotate the CA key, routinely or after a compromise",
			Handler:    s.handleCARotate,
		},
		{
			Method: "GET", Pattern: APIPrefix + "/tenants/{tenant}/ca/certificates",
			Access:     AccessTenant,
			Permission: identity.PermCARead,
			Summary:    "the certificates that are still usable",
			Handler:    s.handleCACertificates,
		},

		// --- the claim mapping (read; authoring is 0014's) ---
		{
			Method: "GET", Pattern: APIPrefix + "/tenants/{tenant}/identity/claim-mapping",
			Access:     AccessTenant,
			Permission: identity.PermIdentityRead,
			Summary:    "the active claim mapping, its version and what it can produce",
			Handler:    s.handleClaimMapping,
		},
	}
}
