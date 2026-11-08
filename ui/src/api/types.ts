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
  /** One entry per tenant the caller may read (instance.FleetHealth). */
  fleet?: FleetHealth[];
}

/** instance.FleetSummary: one tenant's whole fleet, counted by the server. */
export interface FleetSummary {
  enrolled: number;
  live: number;
  stale: number;
  unreachable: number;
  pending: number;
  revoked: number;
  config_drifted: number;
  contract_versions: Record<string, number>;
  relay_registrations: number;
  recent_errors: number;
  truncated: boolean;
}

/** instance.FleetHealth */
export interface FleetHealth {
  tenant: string;
  state: HealthState;
  observed_at: string | null;
  fresh_for_seconds: number;
  reasons: Reason[];
  policy_version?: number;
  summary: FleetSummary | null;
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

/** The bounded-list envelope every north-bound list answers with (PLAN M26). */
export interface Paged {
  truncated: boolean;
  /** The cursor that continues the list; present only when truncated. */
  next?: string;
}

/** fleet.ConfigRollout: what the proxy REPORTED, beside what is desired (proxy D18). */
export interface ConfigRollout {
  proxy_id: string;
  desired_version?: string;
  desired_hash?: string;
  running_version?: string;
  running_hash?: string;
  /** applied | pending_restart | rejected | fetch_failed; absent: never reported. */
  state?: string;
  restart_required?: string[];
  last_error?: string;
  reported_at?: string;
  last_heard_at?: string;
  caught_up: boolean;
  announced: boolean;
}

/** north.proxyView */
export interface Proxy {
  proxy_id: string;
  zone: string;
  /** pending | enrolled | revoked */
  state: string;
  live: boolean;
  last_heartbeat_at?: string;
  subscription_seen_at?: string;
  contract_version: number;
  capabilities: Record<string, unknown>;
  relay_registrations: string[];
  config: ConfigRollout;
  session_count: number;
  last_error?: string;
  last_error_at?: string;
}

/** north.livenessView */
export interface Liveness {
  heartbeat_ttl_seconds: number;
  relay_registration_ttl_seconds: number;
  target_capability_ttl_seconds: number;
  report_after_seconds: number;
}

/** GET …/proxies */
export interface ProxyPage extends Paged {
  proxies: Proxy[];
  liveness: Liveness;
}

/** POST …/proxies/{proxy}/withdraw */
export interface Withdrawn {
  proxy_id: string;
  state: string;
  tokens_revoked: number;
}
