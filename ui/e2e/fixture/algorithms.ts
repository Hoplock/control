// Algorithm posture's and the runbook's fixture (0058): made through the real
// surfaces and nothing else —
//   - declarations: each proxy states its build's algorithm offer on
//     `/v1/authorize`, as a proxy does on every request, and the server keeps
//     the latest (Hoplock/proxy#72). Only an allow records one, so each asks
//     for a session the seeded policy allows;
//   - key-exchange observations: `/v1/capabilities/report` carrying `kex`
//     (Hoplock/proxy#69), one fresh and one stale;
//   - what sessions negotiated: `target.algorithms_negotiated` records (kind
//     `provisioning`) on `/v1/logs/batch`, which `…/audit/negotiated` finds
//     them by.
// Its builds — a rolling upgrade, mid-way:
//   px-core-01   build 1: modern-kex and pq-hybrid-kex; three ciphers
//   px-core-02   build 2: modern-kex only, without ML-KEM; aes128-ctr alone,
//                so a ban on it empties the cipher axis on this build only
//   px-dmz-03    build 3: build 1's levels, and no profiles (not reported;
//                a ban cannot be judged there)
//   every other proxy declared nothing (not reported)
// Its targets: db-01 last met modern-kex (below pq-hybrid-kex), api-01's
// observation is stale, vault-01 has none.
// Its sessions: five negotiated aes128-ctr, which the runbook reads at two to
// a page, so its list is longer than one page.

import type { APIRequestContext } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { SOUTH, TENANT, UI } from "../harness";
import { POLICY_VERSION } from "./contract";

/** The cipher the runbook's advisory names. */
export const BANNED = "aes128-ctr";
/** The sessions that negotiated it. */
export const NEGOTIATED = ["sess-e2e-neg-1", "sess-e2e-neg-2", "sess-e2e-neg-3", "sess-e2e-neg-4", "sess-e2e-neg-5"];

const KEX_OLD = ["curve25519-sha256", "curve25519-sha256@libssh.org"];
const KEX_NEW = [...KEX_OLD, "mlkem768x25519-sha256"];
const CIPHERS_NEW = ["aes128-ctr", "aes256-gcm@openssh.com", "chacha20-poly1305@openssh.com"];
const MACS = ["hmac-sha2-256-etm@openssh.com", "hmac-sha2-256"];
const HOST_KEYS = ["ssh-ed25519", "rsa-sha2-512"];
const PUBKEY = ["ssh-ed25519", "rsa-sha2-512"];

const profile = (name: string, kex: string[], ciphers: string[], extra: { host_keys?: string[]; public_key_auth?: string[] } = {}) => ({
  profile: name,
  key_exchanges: kex,
  ciphers,
  macs: MACS,
  host_keys: extra.host_keys ?? HOST_KEYS,
  public_key_auth: extra.public_key_auth ?? PUBKEY,
});

const BUILD_NEW = {
  algorithm_floors: [
    { level: "modern-kex", key_exchanges: KEX_NEW },
    { level: "pq-hybrid-kex", key_exchanges: ["mlkem768x25519-sha256"] },
  ],
  algorithm_profiles: [
    profile("default", KEX_NEW, CIPHERS_NEW),
    profile("legacy-rsa-sha1", KEX_NEW, CIPHERS_NEW, { host_keys: [...HOST_KEYS, "ssh-rsa"], public_key_auth: [...PUBKEY, "ssh-rsa"] }),
  ],
  algorithms: { key_exchanges: KEX_NEW, ciphers: CIPHERS_NEW, macs: MACS, host_keys: [...HOST_KEYS, "ssh-rsa"], public_key_auth: [...PUBKEY, "ssh-rsa"] },
};
const BUILD_OLD = {
  algorithm_floors: [{ level: "modern-kex", key_exchanges: KEX_OLD }],
  algorithm_profiles: [profile("default", KEX_OLD, [BANNED])],
  algorithms: { key_exchanges: KEX_OLD, ciphers: [BANNED], macs: MACS, host_keys: HOST_KEYS, public_key_auth: PUBKEY },
};
const BUILD_FLOORS_ONLY = { algorithm_floors: BUILD_NEW.algorithm_floors };

const grace = { subject: "grace", login: "grace", source: "local", groups: ["sre"] };

async function declare(api: APIRequestContext, proxy: string, session: string, target: string, capabilities: object, trail?: string[]): Promise<void> {
  const res = await api.post(`${SOUTH}/v1/authorize`, {
    headers: { authorization: `Bearer ${TENANT}.e2e-${proxy}` },
    data: {
      identity: grace,
      target,
      target_port: 22,
      auth_method: "publickey",
      policy_version: POLICY_VERSION,
      capabilities,
      conn: { session_id: session, proxy_id: proxy, client_addr: "10.20.0.9:52000", hop_trail: trail, timestamp: "2026-11-01T09:00:00Z" },
    },
  });
  if (!res.ok()) throw new Error(`declare ${proxy}: ${res.status()} ${await res.text()}`);
}

async function observe(api: APIRequestContext, target: string, kex: object): Promise<void> {
  const res = await api.post(`${SOUTH}/v1/capabilities/report`, {
    headers: { authorization: `Bearer ${TENANT}.e2e-px-core-01` },
    data: { target, target_port: 22, capabilities: { kex } },
  });
  if (!res.ok()) throw new Error(`kex ${target}: ${res.status()} ${await res.text()}`);
}

/** `admin` is an admin's signed-in API context. */
export async function seedAlgorithms(admin: APIRequestContext): Promise<void> {
  // Once per server: a reused local server already holds these.
  const seen = await admin.get(`/api/v1/tenants/${TENANT}/fleet/algorithms?limit=1`);
  if (((await seen.json()) as { declarations: unknown[] }).declarations.length) return;

  await declare(admin, "px-core-01", "sess-e2e-algo-01", "db-01.core.internal", BUILD_NEW);
  await declare(admin, "px-core-02", "sess-e2e-algo-02", "db-01.core.internal", BUILD_OLD);
  await declare(admin, "px-dmz-03", "sess-e2e-algo-03", "vault-01.dmz.internal", BUILD_FLOORS_ONLY, ["px-core-01"]);

  // Fresh, below pq-hybrid-kex: the floor's impact preview names it.
  await observe(admin, "db-01.core.internal", {
    floor_met: "modern-kex",
    negotiated: "curve25519-sha256",
    offered: ["curve25519-sha256", "ecdh-sha2-nistp256", "diffie-hellman-group14-sha256"],
    observed_at: new Date().toISOString().replace(/\.\d+Z$/, "Z"),
  });
  // Stale: observed long before the server's freshness window, so not reported.
  await observe(admin, "api-01.core.internal", {
    floor_met: "pq-hybrid-kex",
    negotiated: "mlkem768x25519-sha256",
    offered: KEX_NEW,
    observed_at: "2026-01-05T10:00:00Z",
  });

  const records = NEGOTIATED.map((session, i) => ({
    record_id: `rec-e2e-neg-${i + 1}`,
    session_id: session,
    timestamp: `2026-11-01T09:1${i}:00Z`,
    kind: "provisioning",
    severity: "info",
    subject: "grace",
    login: "grace",
    target: "db-01.core.internal",
    attributes: {
      event: "target.algorithms_negotiated",
      target_kex_algorithm: "curve25519-sha256",
      target_cipher_out: BANNED,
      target_cipher_in: BANNED,
      target_mac_out: "hmac-sha2-256",
      target_mac_in: "hmac-sha2-256",
      target_host_key_algorithm: "ssh-ed25519",
    },
  }));
  const res = await admin.post(`${SOUTH}/v1/logs/batch`, {
    headers: { authorization: `Bearer ${TENANT}.e2e-px-core-01` },
    data: { records },
  });
  if (!res.ok()) throw new Error(`negotiated records: ${res.status()} ${await res.text()}`);
}

/**
 * A candidate that raises a floor the fleet cannot meet everywhere and bans
 * the cipher one build offers alone: the seeded policy, plus a rule over every
 * production target with `pq-hybrid-kex`, plus the bundle's own ban.
 */
export function floorCandidate(): string {
  const base = readFileSync(join(UI, "e2e", "fixture", "policy.yaml"), "utf8").replace(/\s*$/, "\n");
  return `${base}
  # 0058: a floor the fleet cannot meet everywhere yet.
  - id: prod-pq-floor
    description: every production target, at the post-quantum floor
    effect: allow
    match:
      subject:
        groups: [sre]
      target:
        labels: {env: [prod]}
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: ephemeral-user
          username: {from: subject-local-part}
          key_type: ed25519
          lifetime_seconds: 900
      algorithm_floor: pq-hybrid-kex

algorithm_bans:
  ciphers: [${BANNED}]
`;
}
