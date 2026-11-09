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
