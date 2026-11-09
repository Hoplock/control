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

/** decision.MappingFacts: the claim mapping a decision was made under, beside the active one. */
export interface MappingFacts {
  version: number;
  active: number;
  changed: boolean;
}

/** decision.Story (0019): one decision, explained whole (PLAN M4, §5.6). */
export interface Story {
  decision_id: string;
  session_id?: string;
  proxy_id?: string;
  effect: "allow" | "deny" | "unserved" | string;
  decided_at: string;
  rule?: string;
  rule_line?: number;
  mapping: MappingFacts;
  obligations?: string[];
  /** The `inputs.grants[]` entry the explanation names, whole. */
  grant?: RecordedGrant;
  /** The standing denials that decided, whole (M28). */
  standing?: RecordedStanding[];
  inputs: RecordedInputs;
  explanation: RecordedExplanation;
  /** The contract's AuthorizeResponse, as served: absent on a deny. */
  snapshot?: Snapshot;
  unserved?: string;
}

/** north explainSession's answer: a session's newest decisions, bounded (M26.3). */
export interface SessionStories {
  session_id: string;
  decisions: Story[];
  truncated: boolean;
}

/** decision.recordedInputs: what the engine saw. */
export interface RecordedInputs {
  now: string;
  subject: {
    id: string;
    source?: string;
    groups?: string[];
    claims?: Record<string, string>;
    auth_method?: string;
    mfa: boolean;
    known: boolean;
    mapping_version?: number;
    break_glass: boolean;
  };
  target: { hostname: string; port?: number; zone?: string; labels?: Record<string, string>; known: boolean };
  /** Where the question came from: the proxy that asked, and the proxies before it. */
  context: { proxy_id: string; source_addr?: string; hop_trail?: string[] };
  connection?: { session_id?: string; client_version?: string; policy_version?: number };
}

/** decision.recordedGrant */
export interface RecordedGrant {
  id: string;
  origin: string;
  scope?: { name?: string; targets?: string[]; labels?: Record<string, string>; zones?: string[] };
  not_before?: string;
  expires_at?: string;
  reason?: string;
  reason_code?: string;
  created_by?: { subject?: string; principal?: string; break_glass?: boolean };
  request_id?: string;
  workflow_ref?: string;
  approvers?: string[];
  external?: { system?: string; reference?: string; window_start?: string; window_end?: string };
}

/** decision.recordedStanding */
export interface RecordedStanding {
  code: string;
  subject_id?: string;
  target_id?: string;
  placed_at: string;
  placed_by?: { subject?: string; principal?: string };
  reason_code?: string;
  reason?: string;
}

/** decision.recordedExplanation: the engine's account, and the layers above it. */
export interface RecordedExplanation {
  effect: string;
  basis?: string;
  rule?: string;
  rule_line?: number;
  terms?: string[];
  obligations?: string[];
  deny_reason?: string;
  grant?: string;
  bundle_digest?: string;
  rules_considered?: number;
  standing?: string[];
  route_type?: string;
  next_proxy_id?: string;
  unserved?: string;
}

/** contract.AuthorizeResponse, the part Explain reads. */
export interface Snapshot {
  route_type: string;
  target: string;
  target_port?: number;
  permitted_channels?: string[];
  hop?: { next_proxy_id?: string; final_target?: string; max_hops?: number; hop_trail?: string[] };
  require_session_capture?: boolean;
  session_deadline?: string;
}

/** north.recordView (0010, 0019): one audit record, with its place in its chain. */
export interface AuditRecord {
  record_id: string;
  stream: string;
  chain_seq: number;
  prev_hash?: string;
  hash: string;
  session_id: string;
  kind: string;
  severity: "info" | "warn" | "critical" | string;
  subject?: string;
  login?: string;
  target?: string;
  message?: string;
  event?: string;
  decision_id?: string;
  proxy_id?: string;
  /** An open map: its keys are their writer's, and a key the console does not know is skipped. */
  attributes?: Record<string, string>;
  recorded_at: string;
  received_at: string;
  body: string;
}

/** north.auditRecordList */
export interface AuditRecordPage extends Paged {
  records: AuditRecord[];
}

/** north.blockedView: one row of the showcase join. */
export interface BlockedCommand {
  record: AuditRecord;
  command: string;
  action: string;
  target_zone: string;
  target_labels: Record<string, string>;
  decision_found: boolean;
  matched_rule?: string;
  route_type?: string;
  decided_at?: string;
}

/** north.blockedList */
export interface BlockedPage extends Paged {
  blocked: BlockedCommand[];
}

/** north.captureView (version 3): one record's session capture. */
export interface Capture {
  record_id: string;
  session_id: string;
  capture_bytes: number;
  capture_sha256: string;
  retained: boolean;
  payload?: string;
}

/** authoring.Version: one stored policy version, without its source. */
export interface PolicyVersion {
  version: number;
  /** `sha256:<hex>` of the source. */
  digest: string;
  uploaded_by: string;
  uploaded_at: string;
  active: boolean;
}

/** north.versionList (M26): newest first, by version. */
export interface PolicyVersionPage extends Paged {
  versions: PolicyVersion[];
}

/** north.bundleView: one version, whole. */
export interface PolicyBundle extends PolicyVersion {
  source: string;
}

/** authoring.Rejection: one compiler refusal, verbatim (0005). */
export interface Rejection {
  code: string;
  rule?: string;
  line?: number;
  parameters?: Record<string, string>;
  /** English, for policyctl and curl; the console never shows it. */
  message: string;
}

/** A finding's severity (PLAN §5.6). */
export type Severity = "advice" | "warning" | "blocking";

/** authoring.Finding: one thing a publish-time check has to say. */
export interface Finding {
  /** What an author accepts: the code and what it is about, never its message. */
  id: string;
  severity: Severity | string;
  code: string;
  /** "control", or the provider of the validator that said it. */
  source: string;
  rule?: string;
  line?: number;
  parameters?: Record<string, unknown>;
  message: string;
}

/** authoring.Report: whether a bundle compiles, and what to know before publishing it. */
export interface PolicyReport {
  valid: boolean;
  digest: string;
  rejections: Rejection[];
  findings: Finding[];
}

/** authoring.UploadResult */
export interface PolicyUpload {
  version: PolicyVersion;
  /** The identical source was already stored, as this version. */
  existing: boolean;
  report: PolicyReport;
}

/** authoring.ActivateResult */
export interface PolicyActivation {
  version: PolicyVersion;
  previous: number;
  unchanged: boolean;
  rollback: boolean;
  accepted: string[];
  report: PolicyReport;
}

/** authoring.Diff: what separates two bundles, by rule and by line. */
export interface PolicyDiff {
  from: number;
  to: number;
  added: string[];
  removed: string[];
  changed: string[];
  moved: string[];
  document: string[];
  /** A unified diff of the sources. */
  text: string;
  text_omitted?: boolean;
}

/** decision.DryRunResult */
export interface DryRunResult {
  name?: string;
  effect: string;
  rule?: string;
  rule_line?: number;
  terms?: string[];
  deny_reason?: string;
  obligations?: string[];
}

/** decision.Flip: one recorded decision a candidate would answer otherwise. */
export interface Flip {
  decision_id: string;
  session_id?: string;
  subject: string;
  target: string;
  decided_at: string;
  from: string;
  to: string;
  was_rule?: string;
  now_rule?: string;
}

/** decision.FlipGroup */
export interface FlipGroup {
  from: string;
  to: string;
  was_rule?: string;
  now_rule?: string;
  count: number;
  flips: Flip[];
}

/** decision.ReplayReport */
export interface Replay {
  from: string;
  to: string;
  replayed: number;
  unchanged: number;
  truncated: boolean;
  allow_to_deny: FlipGroup[];
  deny_to_allow: FlipGroup[];
  unreplayable?: { decision_id: string; reason: string }[];
}

/** authoring.SimulateResult */
export interface Simulation {
  digest: string;
  version?: number;
  dry_run: DryRunResult[];
  replay?: Replay;
}
