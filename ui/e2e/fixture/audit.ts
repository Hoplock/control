// Explain's and Audit's fixture (0051): the sessions, decisions and records
// those screens show, made through the real surfaces and nothing else —
//   - decisions: each proxy asks `/v1/authorize` south-bound with its own
//     credential, as a proxy would, under the seeded policy (policy.yaml);
//   - records: each proxy delivers its batch to `/v1/logs/batch`, a recorded
//     session's terminal among them as `stream` records with captures;
//   - a supervisor's acts (M19): the seeded `sup-acme-ops` token kills a
//     session naming the operator it acts for (`Hoplock-Acting-Operator`,
//     0048), and invalidates cached decisions naming nobody.
// Its sessions:
//   sess-e2e-allow      grace → db-01, direct, allowed by ops-db-shell, recorded
//   sess-e2e-deny       mallory → db-01, the default deny (no rule matched)
//   sess-e2e-rule-deny  grace → build-01, denied by no-shell-on-build-hosts
//   sess-e2e-hops       grace → vault-01 through px-dmz-03: two decisions,
//                       under an administrator's just-in-time grant
//   sess-e2e-gaps       grace → app-01, recorded, with holes (0057): two
//                       `logging.gap` reports of `evicted` (summed) and one
//                       of `refused` that hid a critical record, each sent
//                       after the records around its hole
// And one gap that belongs to no session, on px-dmz-03's stream (0057).
// The decision ids are the server's own, minted at random, so the screens
// mask them in screenshots. Record ids and instants are the fixture's.

import { request, type APIRequestContext } from "@playwright/test";
import { NORTH, SOUTH, TENANT } from "../harness";

const SUPERVISOR = `ht.${TENANT}.e2e-supervisor-token`;

interface Ask {
  proxy: string;
  session: string;
  subject: string;
  groups?: string[];
  target: string;
  client: string;
  trail?: string[];
  at: string;
}

async function authorize(api: APIRequestContext, a: Ask): Promise<string | null> {
  const res = await api.post(`${SOUTH}/v1/authorize`, {
    headers: { authorization: `Bearer ${TENANT}.e2e-${a.proxy}` },
    data: {
      identity: { subject: a.subject, login: a.subject, source: "local", groups: a.groups ?? [] },
      target: a.target,
      target_port: 22,
      auth_method: "publickey",
      policy_version: 6,
      conn: { session_id: a.session, proxy_id: a.proxy, client_addr: a.client, hop_trail: a.trail, timestamp: a.at },
    },
  });
  // A deny is a 401 and an answer: the decision is recorded all the same.
  if (res.status() === 401) return null;
  if (!res.ok()) throw new Error(`authorize ${a.session} at ${a.proxy}: ${res.status()} ${await res.text()}`);
  return ((await res.json()) as { decision_id: string }).decision_id;
}

interface Rec {
  record_id: string;
  session_id: string;
  timestamp: string;
  kind: string;
  severity: "info" | "warn" | "critical";
  message?: string;
  subject?: string;
  login?: string;
  target?: string;
  attributes?: Record<string, string>;
  payload?: string;
}

async function deliver(api: APIRequestContext, proxy: string, records: Rec[]): Promise<void> {
  const res = await api.post(`${SOUTH}/v1/logs/batch`, {
    headers: { authorization: `Bearer ${TENANT}.e2e-${proxy}` },
    data: { records },
  });
  if (!res.ok()) throw new Error(`logs from ${proxy}: ${res.status()} ${await res.text()}`);
}

const b64 = (s: string) => Buffer.from(s, "utf8").toString("base64");
const PROMPT = "\x1b[1;32mgrace@db-01\x1b[0m:~$ ";

export async function seedAudit(admin: APIRequestContext): Promise<void> {
  // Once per server: a reused local server already holds these records.
  const seen = await admin.get(`/api/v1/tenants/${TENANT}/audit/records?session=sess-e2e-allow&limit=1`);
  if (seen.ok() && ((await seen.json()) as { records: unknown[] }).records.length) return;

  // The grant the vault's rule requires (M10): an administrator's, for a day.
  const grant = await admin.post(`/api/v1/tenants/${TENANT}/grants`, {
    data: {
      subject: "grace",
      scope: { name: "vault-maintenance", targets: [], labels: {}, zones: ["dmz"] },
      duration: "24h",
      reason: "CHG-1042: rotate the vault's certificates",
    },
  });
  if (!grant.ok()) throw new Error(`grant: ${grant.status()} ${await grant.text()}`);

  const grace = { subject: "grace", groups: ["sre"] };
  const allow = await authorize(admin, {
    proxy: "px-core-01", session: "sess-e2e-allow", ...grace, target: "db-01.core.internal", client: "10.20.0.7:51522", at: "2026-11-01T08:00:00Z",
  });
  await authorize(admin, {
    proxy: "px-core-01", session: "sess-e2e-deny", subject: "mallory", target: "db-01.core.internal", client: "10.20.0.9:40112", at: "2026-11-01T08:05:00Z",
  });
  await authorize(admin, {
    proxy: "px-core-01", session: "sess-e2e-rule-deny", ...grace, target: "build-01.core.internal", client: "10.20.0.7:51700", at: "2026-11-01T08:20:00Z",
  });
  const edge = await authorize(admin, {
    proxy: "px-core-01", session: "sess-e2e-hops", ...grace, target: "vault-01.dmz.internal", client: "10.20.0.7:51600", at: "2026-11-01T08:10:00Z",
  });
  const inner = await authorize(admin, {
    proxy: "px-dmz-03", session: "sess-e2e-hops", ...grace, target: "vault-01.dmz.internal", client: "10.30.0.3:40001", trail: ["px-core-01"], at: "2026-11-01T08:10:01Z",
  });
  if (!allow || !edge || !inner) throw new Error("the fixture's allowed sessions were not allowed: is policy.yaml seeded?");

  const db = { subject: "grace", login: "grace", target: "db-01.core.internal" };
  await deliver(admin, "px-core-01", [
    { record_id: "rec-e2e-allow-01", session_id: "sess-e2e-allow", timestamp: "2026-11-01T08:00:00Z", kind: "session_start", severity: "info", message: "session opened from 10.20.0.7", ...db },
    { record_id: "rec-e2e-allow-02", session_id: "sess-e2e-allow", timestamp: "2026-11-01T08:00:01Z", kind: "authorize", severity: "info", message: "authorized by ops-db-shell", ...db, attributes: { decision_id: allow } },
    { record_id: "rec-e2e-allow-03", session_id: "sess-e2e-allow", timestamp: "2026-11-01T08:00:02Z", kind: "stream", severity: "info", ...db, payload: b64(PROMPT) },
    { record_id: "rec-e2e-allow-04", session_id: "sess-e2e-allow", timestamp: "2026-11-01T08:00:20Z", kind: "command", severity: "info", ...db, attributes: { command: "psql -c 'select count(*) from orders'", action: "allow", decision_id: allow } },
    { record_id: "rec-e2e-allow-05", session_id: "sess-e2e-allow", timestamp: "2026-11-01T08:00:21Z", kind: "stream", severity: "info", ...db, payload: b64("psql -c 'select count(*) from orders'\r\n count \r\n-------\r\n 48213\r\n(1 row)\r\n\r\n" + PROMPT) },
    { record_id: "rec-e2e-allow-06", session_id: "sess-e2e-allow", timestamp: "2026-11-01T08:01:40Z", kind: "command", severity: "warn", message: "command blocked by policy", ...db, attributes: { command: "systemctl stop postgresql", action: "block_command", decision_id: allow } },
    { record_id: "rec-e2e-allow-07", session_id: "sess-e2e-allow", timestamp: "2026-11-01T08:01:41Z", kind: "stream", severity: "info", ...db, payload: b64("sudo systemctl stop postgresql\r\nhoplock: this command is not permitted on this route\r\n" + PROMPT + "exit\r\nlogout\r\n") },
    { record_id: "rec-e2e-allow-08", session_id: "sess-e2e-allow", timestamp: "2026-11-01T08:02:00Z", kind: "session_end", severity: "info", message: "session closed", ...db },
    { record_id: "rec-e2e-deny-01", session_id: "sess-e2e-deny", timestamp: "2026-11-01T08:05:00Z", kind: "authorize", severity: "warn", message: "access denied", subject: "mallory", login: "mallory", target: "db-01.core.internal" },
    { record_id: "rec-e2e-rule-deny-01", session_id: "sess-e2e-rule-deny", timestamp: "2026-11-01T08:20:00Z", kind: "authorize", severity: "warn", message: "access denied", subject: "grace", login: "grace", target: "build-01.core.internal" },
    { record_id: "rec-e2e-hops-01", session_id: "sess-e2e-hops", timestamp: "2026-11-01T08:10:00Z", kind: "authorize", severity: "info", message: "routed to px-dmz-03", subject: "grace", login: "grace", target: "vault-01.dmz.internal", attributes: { decision_id: edge } },
  ]);
  await deliver(admin, "px-dmz-03", [
    { record_id: "rec-e2e-hops-02", session_id: "sess-e2e-hops", timestamp: "2026-11-01T08:10:01Z", kind: "authorize", severity: "info", message: "authorized by vault-through-dmz", subject: "grace", login: "grace", target: "vault-01.dmz.internal", attributes: { decision_id: inner } },
    { record_id: "rec-e2e-hops-03", session_id: "sess-e2e-hops", timestamp: "2026-11-01T08:12:30Z", kind: "session_end", severity: "info", message: "session closed", subject: "grace", login: "grace", target: "vault-01.dmz.internal" },
  ]);

  await seedGaps(admin);

  // What a supervisor did from above (M19): one act naming the person it
  // acted for, one naming nobody. Its own context: its token, no session.
  const sup = await request.newContext({ baseURL: NORTH });
  const kill = await sup.post(`/api/v1/tenants/${TENANT}/revocations`, {
    headers: { authorization: `Bearer ${SUPERVISOR}`, "hoplock-acting-operator": "Dana Ruiz" },
    data: { type: "session_kill", session_ids: ["sess-e2e-allow"], reason: "incident INC-4821: stop the database session" },
  });
  if (!kill.ok()) throw new Error(`supervisor's session kill: ${kill.status()} ${await kill.text()}`);
  const flush = await sup.post(`/api/v1/tenants/${TENANT}/revocations`, {
    headers: { authorization: `Bearer ${SUPERVISOR}` },
    data: { type: "cache_invalidate", all: true },
  });
  if (!flush.ok()) throw new Error(`supervisor's cache invalidation: ${flush.status()} ${await flush.text()}`);
  await sup.dispose();
}

/**
 * A proxy's own report of records this server does not hold (PLAN §7,
 * `Hoplock/proxy#71`; upstream `api/README.md`, "When records do not
 * arrive"): `kind: error`, `event: logging.gap`, one per session and cause,
 * its keys as attributes. Its timestamp is when the proxy wrote it, after the
 * hole, so a timeline places it by its span.
 */
function gap(g: {
  id: string;
  session: string;
  at: string;
  cause: "evicted" | "refused";
  records: number;
  bytes: number;
  kinds: string;
  critical: number;
  first: string;
  last: string;
  extra?: Record<string, string>;
}): Rec {
  return {
    record_id: g.id,
    session_id: g.session,
    timestamp: g.at,
    kind: "error",
    severity: g.critical > 0 ? "critical" : "warn",
    ...(g.session ? { subject: "grace", login: "grace", target: "app-01.core.internal" } : {}),
    attributes: {
      event: "logging.gap",
      gap_cause: g.cause,
      gap_records: String(g.records),
      gap_bytes: String(g.bytes),
      gap_kinds: g.kinds,
      gap_critical: String(g.critical),
      gap_first_at: g.first,
      gap_last_at: g.last,
      ...g.extra,
    },
  };
}

async function seedGaps(admin: APIRequestContext): Promise<void> {
  const app = { subject: "grace", login: "grace", target: "app-01.core.internal" };
  const s = "sess-e2e-gaps";
  const prompt = "\x1b[1;32mgrace@app-01\x1b[0m:~$ ";
  await deliver(admin, "px-core-01", [
    { record_id: "rec-e2e-gaps-01", session_id: s, timestamp: "2026-11-01T09:00:00Z", kind: "session_start", severity: "info", message: "session opened from 10.20.0.7", ...app },
    { record_id: "rec-e2e-gaps-02", session_id: s, timestamp: "2026-11-01T09:00:01Z", kind: "stream", severity: "info", ...app, payload: b64(prompt) },
    { record_id: "rec-e2e-gaps-03", session_id: s, timestamp: "2026-11-01T09:00:10Z", kind: "command", severity: "info", ...app, attributes: { command: "tail -n 3 /var/log/app/worker.log", action: "allow" } },
    { record_id: "rec-e2e-gaps-04", session_id: s, timestamp: "2026-11-01T09:00:11Z", kind: "stream", severity: "info", ...app, payload: b64("tail -n 3 /var/log/app/worker.log\r\nworker 1 started\r\nworker 2 started\r\n") },
    // The proxy was cut off from Control here: what it wrote meanwhile was evicted from its disk.
    { record_id: "rec-e2e-gaps-05", session_id: s, timestamp: "2026-11-01T09:04:00Z", kind: "stream", severity: "info", ...app, payload: b64("worker 9 started\r\n" + prompt) },
    // Two records Control refused (rec-px-0411, rec-px-0412), then a second outage.
    { record_id: "rec-e2e-gaps-06", session_id: s, timestamp: "2026-11-01T09:07:00Z", kind: "stream", severity: "info", ...app, payload: b64("exit\r\nlogout\r\n") },
    { record_id: "rec-e2e-gaps-07", session_id: s, timestamp: "2026-11-01T09:07:30Z", kind: "session_end", severity: "info", message: "session closed", ...app },
    // The refused report is sent when the refusal happens; the evicted ones once delivery resumes.
    gap({
      id: "rec-e2e-gap-refused", session: s, at: "2026-11-01T09:04:32Z", cause: "refused", records: 2, bytes: 1730, kinds: "command,policy_decision", critical: 1,
      first: "2026-11-01T09:04:30.120000000Z", last: "2026-11-01T09:04:31.004000000Z",
      extra: { gap_record_ids: "rec-px-0411,rec-px-0412", refusal_code: "invalid_record", refusal_message: "record 2 (rec-px-0412) is malformed: attributes.command exceeds 4096 bytes" },
    }),
    gap({ id: "rec-e2e-gap-evicted-1", session: s, at: "2026-11-01T09:20:00Z", cause: "evicted", records: 40, bytes: 61440, kinds: "command,stream", critical: 0, first: "2026-11-01T09:00:30Z", last: "2026-11-01T09:03:10Z" }),
    gap({ id: "rec-e2e-gap-evicted-2", session: s, at: "2026-11-01T09:20:01Z", cause: "evicted", records: 12, bytes: 18200, kinds: "stream", critical: 0, first: "2026-11-01T09:05:00Z", last: "2026-11-01T09:06:20Z" }),
  ]);
  // Records that belonged to no session: the proxy's own, evicted during an outage.
  await deliver(admin, "px-dmz-03", [
    gap({ id: "rec-e2e-gap-sessionless", session: "", at: "2026-11-01T07:45:00Z", cause: "evicted", records: 120, bytes: 204800, kinds: "host_key,provisioning", critical: 0, first: "2026-11-01T07:00:00Z", last: "2026-11-01T07:30:00Z" }),
  ]);
}
