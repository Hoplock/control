// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"net/http"

	"github.com/hoplock/control/internal/identity"
)

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
	routes := []Route{
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

		// --- a requester's own (M10, 0016) ---
		//
		// A developer asks the registered approval workflow for access for
		// themselves, without holding grant:write — which would also let
		// them revoke any grant and cancel any request in the tenant. The
		// family is AccessSelf: the holder is the caller, resolved from the
		// credential by the middleware, and no path, query or body names
		// one. With no workflow registered the create is refused
		// (grant_workflow_not_registered), because a request nobody decides
		// is a self-grant. The tenant-wide routes above are untouched.
		{
			Method: "POST", Pattern: APIPrefix + "/tenants/{tenant}/me/grant-requests",
			Access:     AccessSelf,
			Permission: identity.PermGrantRequest,
			Summary:    "ask the registered workflow for a grant for yourself",
			Handler:    s.handleOwnRequestCreate,
		},
		{
			Method: "GET", Pattern: APIPrefix + "/tenants/{tenant}/me/grant-requests",
			Access:     AccessSelf,
			Permission: identity.PermGrantRequest,
			Summary:    "your own requests, newest first",
			Handler:    s.handleOwnRequestList,
		},
		{
			Method: "GET", Pattern: APIPrefix + "/tenants/{tenant}/me/grant-requests/{request}",
			Access:     AccessSelf,
			Permission: identity.PermGrantRequest,
			Summary:    "one of your own, asked about again while it is pending",
			Handler:    s.handleOwnRequestGet,
		},
		{
			Method: "POST", Pattern: APIPrefix + "/tenants/{tenant}/me/grant-requests/{request}/cancel",
			Access:     AccessSelf,
			Permission: identity.PermGrantRequest,
			Summary:    "withdraw one of your own pending requests",
			Handler:    s.handleOwnRequestCancel,
		},

		// --- the claim mapping ---
		{
			Method: "GET", Pattern: APIPrefix + "/tenants/{tenant}/identity/claim-mapping",
			Access:     AccessTenant,
			Permission: identity.PermIdentityRead,
			Summary:    "the active claim mapping, its version and what it can produce",
			Handler:    s.handleClaimMapping,
		},
		{
			Method: "PUT", Pattern: APIPrefix + "/tenants/{tenant}/identity/claim-mapping",
			Access:     AccessTenant,
			Permission: identity.PermIdentityWrite,
			Summary:    "write and activate a new claim mapping version",
			Handler:    s.handleClaimMappingPut,
		},
	}
	routes = append(routes, s.policyRoutes()...)
	routes = append(routes, s.inventoryRoutes()...)
	routes = append(routes, s.auditRoutes()...)
	return routes
}

func tenantRoute(method, path string, perm identity.Permission, summary string, h http.HandlerFunc) Route {
	return Route{
		Method: method, Pattern: APIPrefix + "/tenants/{tenant}" + path,
		Access: AccessTenant, Permission: perm, Summary: summary, Handler: h,
	}
}

// policyRoutes are the policy lifecycle and "explain why" (0019).
//
// Reading and simulating are policy:read, which every role that reads the
// tenant holds. Uploading is policy:write; activating — and apply, which
// activates — is policy:publish, a separate permission because the review step
// is the control. Explaining a decision is decision:read.
func (s *Server) policyRoutes() []Route {
	return []Route{
		tenantRoute("POST", "/policy/validate", identity.PermPolicyRead,
			"compile a bundle and report what publishing it would meet", s.handlePolicyValidate),
		tenantRoute("GET", "/policy/bundles", identity.PermPolicyRead,
			"every stored policy version, newest first", s.handlePolicyList),
		tenantRoute("POST", "/policy/bundles", identity.PermPolicyWrite,
			"upload a bundle as a new immutable version", s.handlePolicyUpload),
		tenantRoute("GET", "/policy/bundles/{version}", identity.PermPolicyRead,
			"one stored version and its source", s.handlePolicyGet),
		tenantRoute("GET", "/policy/bundles/{version}/diff", identity.PermPolicyRead,
			"what separates a version from another, by default the active one", s.handlePolicyDiff),
		tenantRoute("POST", "/policy/bundles/{version}/activate", identity.PermPolicyPublish,
			"make a version the active one; rolling back is activating an older one", s.handlePolicyActivate),
		tenantRoute("GET", "/policy/active", identity.PermPolicyRead,
			"the active version and its source", s.handlePolicyActive),
		tenantRoute("POST", "/policy/apply", identity.PermPolicyPublish,
			"upload and activate in one call, for CI", s.handlePolicyApply),
		tenantRoute("POST", "/policy/simulate", identity.PermPolicyRead,
			"dry-run a candidate against synthetic questions and replay it over recorded ones", s.handlePolicySimulate),

		tenantRoute("GET", "/decisions/{decision}", identity.PermDecisionRead,
			"resolve a decision id into the whole story", s.handleExplainDecision),
		tenantRoute("GET", "/sessions/{session}/decisions", identity.PermDecisionRead,
			"resolve a session id — what the user was told — into every decision taken for it", s.handleExplainSession),
	}
}

// inventoryRoutes are what an operator manages (0019, 0006, D18, M15, M16).
func (s *Server) inventoryRoutes() []Route {
	return []Route{
		tenantRoute("GET", "/targets", identity.PermFleetRead,
			"targets, filtered by label", s.handleTargetList),
		tenantRoute("GET", "/targets/{target}", identity.PermFleetRead,
			"one target and its labels", s.handleTargetGet),
		tenantRoute("PUT", "/targets/{target}", identity.PermTargetWrite,
			"create or edit a target; its labels are policy inputs", s.handleTargetPut),
		tenantRoute("DELETE", "/targets/{target}", identity.PermTargetWrite,
			"remove a target", s.handleTargetDelete),
		tenantRoute("POST", "/targets/relabel", identity.PermTargetWrite,
			"diff and simulate a bulk relabel, and apply it when asked", s.handleTargetRelabel),

		tenantRoute("GET", "/subjects", identity.PermIdentityRead,
			"every subject and its groups", s.handleSubjectList),
		tenantRoute("GET", "/subjects/{subject}", identity.PermIdentityRead,
			"one subject", s.handleSubjectGet),
		tenantRoute("PUT", "/subjects/{subject}", identity.PermIdentityWrite,
			"create or edit a subject and its groups", s.handleSubjectPut),
		tenantRoute("DELETE", "/subjects/{subject}", identity.PermIdentityWrite,
			"remove a subject", s.handleSubjectDelete),
		tenantRoute("GET", "/role-bindings", identity.PermIdentityRead,
			"who holds which role in this tenant", s.handleRoleBindingList),
		tenantRoute("POST", "/role-bindings", identity.PermIdentityWrite,
			"grant a role to a subject or a group", s.handleRoleBind),
		tenantRoute("POST", "/role-bindings/remove", identity.PermIdentityWrite,
			"withdraw a role from a subject or a group", s.handleRoleUnbind),

		tenantRoute("GET", "/proxies", identity.PermFleetRead,
			"the fleet: enrollment, liveness, capabilities and configuration rollout", s.handleProxyList),
		tenantRoute("POST", "/proxies/enrollments", identity.PermFleetWrite,
			"approve a proxy's enrollment: the one-time token it presents", s.handleProxyEnrollment),
		tenantRoute("POST", "/proxies/{proxy}/withdraw", identity.PermFleetWrite,
			"remove a proxy from the fleet and revoke its credentials", s.handleProxyWithdraw),
		tenantRoute("PUT", "/fleet/config/{scope}/{id}", identity.PermFleetWrite,
			"publish a zone's or a proxy's configuration document (fleet-owned keys only)", s.handleConfigPublish),
		tenantRoute("POST", "/fleet/config/{scope}/{id}/rollback", identity.PermFleetWrite,
			"publish a scope's previous configuration as a new version", s.handleConfigRollback),
		tenantRoute("GET", "/fleet/config/rollouts", identity.PermFleetRead,
			"every proxy's desired and running configuration, and its reported state", s.handleConfigRollouts),
		tenantRoute("GET", "/fleet/algorithms", identity.PermFleetRead,
			"each proxy's latest algorithm declaration, per build", s.handleAlgorithmDeclarations),
		tenantRoute("GET", "/fleet/target-observations", identity.PermFleetRead,
			"what each target was seen to take: its rungs and its key exchange", s.handleTargetObservations),

		tenantRoute("POST", "/revocations", identity.PermSessionRevoke,
			"end sessions, invalidate cached decisions, resync, or withdraw a host-key decision", s.handleRevocation),

		tenantRoute("GET", "/extensions", identity.PermExtensionRead,
			"what is extending this deployment, one row per extension point", s.handleExtensions),
		tenantRoute("GET", "/access-context/providers", identity.PermAccessContextRead,
			"the external access-context providers this server runs", s.handleAccessContextProviders),
		// One provider's binding is under the provider rather than at
		// `…/bindings/{provider}`, which cannot be mounted beside 0013's
		// `…/access-context/{provider}/push`: both match
		// `…/bindings/push`, and neither is more specific.
		tenantRoute("GET", "/access-context/bindings", identity.PermAccessContextRead,
			"every provider's scope binding in this tenant", s.handleBindingList),
		tenantRoute("GET", "/access-context/providers/{provider}/binding", identity.PermAccessContextRead,
			"one provider's scope binding", s.handleBindingGet),
		tenantRoute("PUT", "/access-context/providers/{provider}/binding", identity.PermAccessContextWrite,
			"create or replace a provider's scope binding", s.handleBindingPut),
		tenantRoute("DELETE", "/access-context/providers/{provider}/binding", identity.PermAccessContextWrite,
			"remove a provider's scope binding", s.handleBindingDelete),
	}
}

// auditRoutes are the audit query and retention (0010, 0019).
func (s *Server) auditRoutes() []Route {
	return []Route{
		tenantRoute("GET", "/audit/records", identity.PermAuditRead,
			"records over every dimension and projection", s.handleAuditRecords),
		tenantRoute("GET", "/audit/records/{record}", identity.PermAuditRead,
			"one record by id, with its chain position and hash", s.handleAuditRecord),
		tenantRoute("GET", "/audit/blocked-commands", identity.PermAuditRead,
			"blocked commands on labelled targets, with the access that permitted them", s.handleBlockedCommands),
		tenantRoute("GET", "/audit/drift", identity.PermAuditRead,
			"every change the proxy made on a device", s.handleDrift),
		tenantRoute("GET", "/audit/algorithm-policy-unmet", identity.PermAuditRead,
			"the targets that reported an unmet algorithm policy", s.handleAlgorithmPolicyUnmet),
		tenantRoute("GET", "/audit/negotiated", identity.PermAuditRead,
			"the sessions that negotiated a value, ran under a floor, or under a ban", s.handleNegotiated),
		tenantRoute("GET", "/audit/gaps", identity.PermAuditRead,
			"what a proxy could not deliver", s.handleGaps),
		tenantRoute("GET", "/audit/sessions/{session}/loss", identity.PermAuditRead,
			"a session's lost records, summed per cause", s.handleSessionLoss),
		tenantRoute("GET", "/audit/sessions/{session}/algorithms", identity.PermAuditRead,
			"what a session negotiated and the algorithm policy in force", s.handleSessionAlgorithms),
		tenantRoute("GET", "/audit/sessions/{session}/certificates", identity.PermAuditRead,
			"the brokered certificates a session presented, joined to their rows", s.handleSessionCertificates),
		tenantRoute("GET", "/audit/certificates/{serial}/records", identity.PermAuditRead,
			"the records naming one brokered certificate", s.handleCertificateSessions),
		tenantRoute("POST", "/audit/retention", identity.PermAuditRetain,
			"delete a stream's oldest records, never the head, and record where verification resumes", s.handleRetention),
		tenantRoute("POST", "/audit/captures/retention", identity.PermAuditRetain,
			"remove old session captures' bytes, keeping their records", s.handleCaptureRetention),
	}
}
