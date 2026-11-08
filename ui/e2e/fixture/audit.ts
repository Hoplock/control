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
