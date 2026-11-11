// EVERY NORTH-BOUND ROUTE THIS CONSOLE CALLS, BY NAME (PLAN M2).
//
// The console is an API client and nothing more: it calls the routes 0019
// serves, with the operator's own session, and RBAC applies to it exactly as
// it does to curl. So a call names one of these routes and never builds a
// path of its own, and each pattern is written exactly as the north-bound
// route table writes it (internal/httpapi/north/routes.go), so that a test
// can hold this list against that table (0050) and offer each only to a role
// that holds its permission. A later phase adds its routes here.

export interface Route {
  method: "GET" | "POST" | "PUT" | "DELETE";
  pattern: string;
  /** The permission the route requires, or null for an anonymous or self route. */
  permission: string | null;
}

export const ROUTES = {
  sessionMethods: { method: "GET", pattern: "/api/v1/session/methods", permission: null },
  federatedStart: { method: "POST", pattern: "/api/v1/session/federated/{connector}/start", permission: null },
  federatedCallback: { method: "GET", pattern: "/api/v1/session/federated/{connector}/callback", permission: null },
  localLogin: { method: "POST", pattern: "/api/v1/session/local", permission: null },
  session: { method: "GET", pattern: "/api/v1/session", permission: null },
  endSession: { method: "DELETE", pattern: "/api/v1/session", permission: null },
  instance: { method: "GET", pattern: "/api/v1/tenants/{tenant}/instance", permission: "instance:read" },
  instanceHealth: { method: "GET", pattern: "/api/v1/tenants/{tenant}/instance/health", permission: "instance:read" },
  instanceSupervisors: {
    method: "GET",
    pattern: "/api/v1/tenants/{tenant}/instance/supervisors",
    permission: "instance:read",
  },
  proxies: { method: "GET", pattern: "/api/v1/tenants/{tenant}/proxies", permission: "fleet:read" },
  proxyWithdraw: { method: "POST", pattern: "/api/v1/tenants/{tenant}/proxies/{proxy}/withdraw", permission: "fleet:write" },
  explainDecision: { method: "GET", pattern: "/api/v1/tenants/{tenant}/decisions/{decision}", permission: "decision:read" },
  explainSession: { method: "GET", pattern: "/api/v1/tenants/{tenant}/sessions/{session}/decisions", permission: "decision:read" },
  auditRecords: { method: "GET", pattern: "/api/v1/tenants/{tenant}/audit/records", permission: "audit:read" },
  auditRecord: { method: "GET", pattern: "/api/v1/tenants/{tenant}/audit/records/{record}", permission: "audit:read" },
  auditCapture: { method: "GET", pattern: "/api/v1/tenants/{tenant}/audit/records/{record}/capture", permission: "audit:read" },
  blockedCommands: { method: "GET", pattern: "/api/v1/tenants/{tenant}/audit/blocked-commands", permission: "audit:read" },
  policyVersions: { method: "GET", pattern: "/api/v1/tenants/{tenant}/policy/bundles", permission: "policy:read" },
  policyVersion: { method: "GET", pattern: "/api/v1/tenants/{tenant}/policy/bundles/{version}", permission: "policy:read" },
  policyActive: { method: "GET", pattern: "/api/v1/tenants/{tenant}/policy/active", permission: "policy:read" },
  policyVersionDiff: { method: "GET", pattern: "/api/v1/tenants/{tenant}/policy/bundles/{version}/diff", permission: "policy:read" },
  policyDiff: { method: "POST", pattern: "/api/v1/tenants/{tenant}/policy/diff", permission: "policy:read" },
  policyValidate: { method: "POST", pattern: "/api/v1/tenants/{tenant}/policy/validate", permission: "policy:read" },
  policySimulate: { method: "POST", pattern: "/api/v1/tenants/{tenant}/policy/simulate", permission: "policy:read" },
  policyUpload: { method: "POST", pattern: "/api/v1/tenants/{tenant}/policy/bundles", permission: "policy:write" },
  policyActivate: {
    method: "POST",
    pattern: "/api/v1/tenants/{tenant}/policy/bundles/{version}/activate",
    permission: "policy:publish",
  },
  // Inventory (0054).
  targets: { method: "GET", pattern: "/api/v1/tenants/{tenant}/targets", permission: "fleet:read" },
  target: { method: "GET", pattern: "/api/v1/tenants/{tenant}/targets/{target}", permission: "fleet:read" },
  targetPut: { method: "PUT", pattern: "/api/v1/tenants/{tenant}/targets/{target}", permission: "target:write" },
  targetDelete: { method: "DELETE", pattern: "/api/v1/tenants/{tenant}/targets/{target}", permission: "target:write" },
  // Standing denials (0055, M28): the levers whose holders can already end every session.
  targetQuarantine: { method: "POST", pattern: "/api/v1/tenants/{tenant}/targets/{target}/quarantine", permission: "session:revoke" },
  targetRelease: { method: "POST", pattern: "/api/v1/tenants/{tenant}/targets/{target}/release", permission: "session:revoke" },
  subjects: { method: "GET", pattern: "/api/v1/tenants/{tenant}/subjects", permission: "identity:read" },
  subject: { method: "GET", pattern: "/api/v1/tenants/{tenant}/subjects/{subject}", permission: "identity:read" },
  subjectPut: { method: "PUT", pattern: "/api/v1/tenants/{tenant}/subjects/{subject}", permission: "identity:write" },
  subjectDelete: { method: "DELETE", pattern: "/api/v1/tenants/{tenant}/subjects/{subject}", permission: "identity:write" },
  subjectLock: { method: "POST", pattern: "/api/v1/tenants/{tenant}/subjects/{subject}/lock", permission: "session:revoke" },
  subjectUnlock: { method: "POST", pattern: "/api/v1/tenants/{tenant}/subjects/{subject}/unlock", permission: "session:revoke" },
  // The claim mapping (0011, 0043; shown from 0055).
  claimMapping: { method: "GET", pattern: "/api/v1/tenants/{tenant}/identity/claim-mapping", permission: "identity:read" },
  claimMappingPut: { method: "PUT", pattern: "/api/v1/tenants/{tenant}/identity/claim-mapping", permission: "identity:write" },
  groups: { method: "GET", pattern: "/api/v1/tenants/{tenant}/groups", permission: "identity:read" },
  group: { method: "GET", pattern: "/api/v1/tenants/{tenant}/groups/{group}", permission: "identity:read" },
  roleBindings: { method: "GET", pattern: "/api/v1/tenants/{tenant}/role-bindings", permission: "identity:read" },
  roleBind: { method: "POST", pattern: "/api/v1/tenants/{tenant}/role-bindings", permission: "identity:write" },
  roleUnbind: { method: "POST", pattern: "/api/v1/tenants/{tenant}/role-bindings/remove", permission: "identity:write" },
  grants: { method: "GET", pattern: "/api/v1/tenants/{tenant}/grants", permission: "grant:read" },
  grant: { method: "GET", pattern: "/api/v1/tenants/{tenant}/grants/{grant}", permission: "grant:read" },
  grantCreate: { method: "POST", pattern: "/api/v1/tenants/{tenant}/grants", permission: "grant:write" },
  grantRevoke: { method: "POST", pattern: "/api/v1/tenants/{tenant}/grants/{grant}/revoke", permission: "grant:write" },
  // Extensions and Reports (0056): what extends this deployment, and the packs registered to report on it.
  extensions: { method: "GET", pattern: "/api/v1/tenants/{tenant}/extensions", permission: "extension:read" },
  reports: { method: "GET", pattern: "/api/v1/tenants/{tenant}/reports", permission: "audit:read" },
  reportGenerate: { method: "POST", pattern: "/api/v1/tenants/{tenant}/reports/generate", permission: "audit:read" },
} as const satisfies Record<string, Route>;

export type RouteName = keyof typeof ROUTES;

/** The path wildcards a pattern names, as {name}. */
export function wildcards(pattern: string): string[] {
  return Array.from(pattern.matchAll(/\{([a-z_]+)\}/g), (m) => m[1] as string);
}

/** Fills a pattern's wildcards, each escaped as one path segment. */
export function fill(pattern: string, params: Record<string, string>): string {
  return pattern.replace(/\{([a-z_]+)\}/g, (_, name: string) => {
    const v = params[name];
    if (v === undefined || v === "") throw new Error(`${pattern} needs {${name}}`);
    return encodeURIComponent(v);
  });
}
