// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import "github.com/hoplock/control/internal/identity"

// The route table.
//
// It is ONE function so that the whole of what Control serves here, and what
// each route requires, is readable in one sitting. Later phases append to it.
// The one other source of routes is a host binary's (M15): registerTable
// (host.go) adds them after this table, through the same Router.Register and
// the same middleware, so the isolation test enumerates them without being told
// about them.
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

		// --- just-in-time grants (M10, 0012) ---
		//
		// Creating and revoking are administrative acts (grant:write),
		// audited with the actor; reading is grant:read, which every role
		// holds. With an approval workflow registered, a create is a
		// request it decides, and the request routes follow it.
		{
			Method: "POST", Pattern: APIPrefix + "/tenants/{tenant}/grants",
			Access:     AccessTenant,
			Permission: identity.PermGrantWrite,
			Summary:    "grant time-boxed access, or ask the registered workflow to",
			Handler:    s.handleGrantCreate,
		},
		{
			Method: "GET", Pattern: APIPrefix + "/tenants/{tenant}/grants",
			Access:     AccessTenant,
			Permission: identity.PermGrantRead,
			Summary:    "list grants, by holder and by state",
			Handler:    s.handleGrantList,
		},
		{
			Method: "GET", Pattern: APIPrefix + "/tenants/{tenant}/grants/{grant}",
			Access:     AccessTenant,
			Permission: identity.PermGrantRead,
			Summary:    "one grant, and the decisions it supplied",
			Handler:    s.handleGrantGet,
		},
		{
			Method: "POST", Pattern: APIPrefix + "/tenants/{tenant}/grants/{grant}/revoke",
			Access:     AccessTenant,
			Permission: identity.PermGrantWrite,
			Summary:    "revoke a grant and end the sessions it backed",
			Handler:    s.handleGrantRevoke,
		},
		{
			Method: "GET", Pattern: APIPrefix + "/tenants/{tenant}/grant-requests/{request}",
			Access:     AccessTenant,
			Permission: identity.PermGrantRead,
			Summary:    "a workflow request, asked about again while it is pending",
			Handler:    s.handleGrantRequestGet,
		},
		{
			Method: "POST", Pattern: APIPrefix + "/tenants/{tenant}/grant-requests/{request}/cancel",
			Access:     AccessTenant,
			Permission: identity.PermGrantWrite,
			Summary:    "withdraw a pending workflow request",
			Handler:    s.handleGrantRequestCancel,
		},

		// --- external access context: the push receiver (M16, 0013) ---
		//
		// An external system's credential asserts a window here. It is
		// administrative in E7's sense and more so — granting access is a
		// larger privilege than ending a session — so the route needs
		// `access-context:push`, which only the integration role (and the
		// admin) holds, and the integration's scope binding must then name
		// this very credential. Everything past that is accessctx's.
		{
			Method: "POST", Pattern: APIPrefix + "/tenants/{tenant}/access-context/{provider}/push",
			Access:     AccessTenant,
			Permission: identity.PermAccessContextPush,
			Summary:    "assert an external access window, admitted only through the integration's scope binding",
			Handler:    s.handleAccessContextPush,
		},

		// --- the claim mapping (read; authoring is 0018's) ---
		{
			Method: "GET", Pattern: APIPrefix + "/tenants/{tenant}/identity/claim-mapping",
			Access:     AccessTenant,
			Permission: identity.PermIdentityRead,
			Summary:    "the active claim mapping, its version and what it can produce",
			Handler:    s.handleClaimMapping,
		},
	}
}
