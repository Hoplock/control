// The north-bound shapes the shell reads. Each mirrors its Go type, named
// beside it; a field the console does not read is not declared.

/** identity.ConnectorSummary */
export interface Connector {
  name: string;
  kind: "oidc" | "saml" | string;
  display_name: string;
  directory_claim?: string;
  directory_source?: string;
}

/** north.signInMethodsResponse */
export interface SignInMethods {
  tenant: string;
  methods: Connector[];
  local_login: boolean;
}

/** north.federationStartResponse */
export interface FederationStart {
  redirect_url: string;
  state: string;
}

/** north.scopeView */
export interface Scope {
  roles: string[];
  permissions: string[];
}

/** north.whoAmI */
export interface Session {
  principal_id: string;
  kind: string;
  subject?: string;
  display_name?: string;
  source: string;
  break_glass: boolean;
  groups?: string[];
  scopes: Record<string, Scope>;
  expires_at?: string;
}

/** north.sessionResponse. The credential is the cookie's; a browser ignores it. */
export interface SessionIssued {
  session: Session;
}

/** instance.Versions */
export interface Versions {
  software: string;
  contract: string;
  contract_commit: string;
  north_api: number;
  north_api_min: number;
}

/** instance.Identity */
export interface Identity {
  instance_id: string;
  display_name: string;
  created_at: string;
  versions: Versions;
  tenants: { tenant: string; active: boolean; last_active: string }[];
}

/** instance.State */
export type HealthState = "healthy" | "degraded" | "in_progress" | "unknown" | "stale" | "left";

/** instance.Reason */
export interface Reason {
  code: string;
  node?: string;
  tenant?: string;
  check?: string;
  count?: number;
}

/** instance.NodeHealth */
export interface NodeHealth {
  node_id: string;
  version: string;
  member: boolean;
  state: HealthState;
  reasons: Reason[];
  reported_at: string | null;
}

/** instance.Health */
export interface Health {
  state: HealthState;
  reasons: Reason[];
  served_at: string;
  nodes: {
    state: HealthState;
    members: NodeHealth[];
    upgrade: { in_progress: boolean; versions: string[] };
  };
}

/** north.supervisorView */
export interface Supervisor {
  principal_id: string;
  display_name: string;
  live: boolean;
}

/** north.supervisorsView */
export interface Supervisors {
  supervisors: Supervisor[];
}
