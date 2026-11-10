// Inventory's fixture (0054): what seed.yaml cannot write, made through the
// north-bound API an administrator uses and nothing else.
//   role bindings  group sre → fleet-admin and auditor, group payments →
//                  grant-admin, lin → policy-author directly
//   grants         grace: prod-db, active for four hours (INC-4021);
//                  lin: payments-deploy, scheduled two days out;
//                  noor: vault-read, granted and then revoked;
//                  grace: build-cache, active for eight hours
// Grants are listed newest first, so they are made in the order the list
// should not show them. Run after seedPolicy (e2e/screens.ts, SEEDS).

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import type { APIRequestContext } from "@playwright/test";
import { TENANT, UI } from "../harness";

const API = `/api/v1/tenants/${TENANT}`;

/** Where the fixture leaves the ids the server chose, for the screens that open one. */
const IDS = join(UI, "build", "e2e", "inventory.json");

/**
 * A seeded grant's id, by its scope. The server mints grant ids, so a screen
 * that opens one reads it here, inside a test — never while the tests are
 * being collected, which is before the fixture has run.
 */
export function fixtureGrant(scope: string): string {
  const ids = JSON.parse(readFileSync(IDS, "utf8")) as Record<string, string>;
  const id = ids[scope];
  if (!id) throw new Error(`the fixture holds no grant for scope ${scope}`);
  return id;
}

/** Records every seeded grant's id by its scope, whether this run made them or found them. */
async function recordIds(admin: APIRequestContext): Promise<void> {
  const res = await admin.get(`${API}/grants?limit=100`);
  if (!res.ok()) throw new Error(`list grants: ${res.status()}`);
  const ids: Record<string, string> = {};
  for (const g of ((await res.json()) as { grants: { id: string; scope: { name: string } }[] }).grants) ids[g.scope.name] ??= g.id;
  mkdirSync(dirname(IDS), { recursive: true });
  writeFileSync(IDS, JSON.stringify(ids, null, 2));
}

async function ok(what: string, res: Awaited<ReturnType<APIRequestContext["post"]>>): Promise<unknown> {
  if (!res.ok()) throw new Error(`${what}: ${res.status()} ${await res.text()}`);
  return res.status() === 204 ? undefined : res.json();
}

async function grant(admin: APIRequestContext, body: Record<string, unknown>): Promise<string> {
  const out = (await ok(`grant ${String(body.subject)}`, await admin.post(`${API}/grants`, { data: body }))) as { grant?: { id: string } };
  if (!out.grant) throw new Error("a grant was asked for and a workflow request came back: the fixture registers no workflow");
  return out.grant.id;
}

export async function seedInventory(admin: APIRequestContext): Promise<void> {
  // Already seeded (a reused server) when its own grants are there: the audit
  // fixture's grant (0051) is in the list whatever this one did.
  const seen = await admin.get(`${API}/grants?subject=grace&limit=100`);
  if (!seen.ok()) throw new Error(`list grants: ${seen.status()}`);
  if (((await seen.json()) as { grants: { scope: { name: string } }[] }).grants.some((g) => g.scope.name === "prod-db")) return recordIds(admin);

  for (const b of [
    { group_id: "sre", role: "fleet-admin" },
    { group_id: "sre", role: "auditor" },
    { group_id: "payments", role: "grant-admin" },
    { subject_id: "lin", role: "policy-author" },
  ]) {
    await ok(`bind ${b.role}`, await admin.post(`${API}/role-bindings`, { data: b }));
  }

  const day = 24 * 60 * 60 * 1000;
  const from = new Date(Math.ceil((Date.now() + 2 * day) / 60_000) * 60_000);
  const revoked = await grant(admin, {
    subject: "noor",
    scope: { name: "vault-read", zones: ["dmz"] },
    duration: "1h",
    reason: "Rotate the vault's audit key with the vendor on the call",
  });
  await ok(
    "revoke vault-read",
    await admin.post(`${API}/grants/${revoked}/revoke`, { data: { reason: "Rotation finished early; access no longer needed" } }),
  );
  await grant(admin, {
    subject: "lin",
    scope: { name: "payments-deploy", labels: { team: "payments", env: "prod" } },
    not_before: from.toISOString(),
    expires_at: new Date(from.getTime() + 2 * 60 * 60 * 1000).toISOString(),
    reason: "Quarterly payments release window",
    external_ref: "CHG-1180",
  });
  await grant(admin, {
    subject: "grace",
    scope: { name: "build-cache", targets: ["build-01.core.internal"] },
    duration: "8h",
    reason: "Clear the build cache after the toolchain bump",
  });
  await grant(admin, {
    subject: "grace",
    scope: { name: "prod-db", targets: ["db-01.core.internal"] },
    duration: "4h",
    reason: "Database failover during incident INC-4021",
    external_ref: "INC-4021",
  });
  await recordIds(admin);
}
