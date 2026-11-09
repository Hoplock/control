// Policy's fixture (0053): the versions the Policy screens show, made through
// the north-bound API an author uses and nothing else. After the decisions
// Explain and Audit show were taken under the seeded policy (v1, policy.yaml):
//   v2  policy-v2.yaml, uploaded and ACTIVATED, accepting its one warning by
//       its id (a cache hint on a rule gated by the client's address)
//   v3  policy-v3.yaml, uploaded and left stored: the candidate an author
//       would activate next, which flips recorded decisions both ways
// So the version list holds a superseded version older than the active one
// (a rollback) and one newer (an activation), and the recorded traffic is
// what a replay reads. Run after seedAudit (e2e/screens.ts, SEEDS).

import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { APIRequestContext } from "@playwright/test";
import { TENANT, UI } from "../harness";

const API = `/api/v1/tenants/${TENANT}/policy`;

function source(file: string): string {
  return readFileSync(join(UI, "e2e", "fixture", file), "utf8");
}

async function upload(admin: APIRequestContext, file: string): Promise<{ version: number; warnings: string[] }> {
  const res = await admin.post(`${API}/bundles`, { data: { source: source(file) } });
  if (!res.ok()) throw new Error(`upload ${file}: ${res.status()} ${await res.text()}`);
  const body = (await res.json()) as { version: { version: number }; report: { findings: { id: string; severity: string }[] } };
  return { version: body.version.version, warnings: body.report.findings.filter((f) => f.severity === "warning").map((f) => f.id) };
}

export async function seedPolicy(admin: APIRequestContext): Promise<void> {
  const seen = await admin.get(`${API}/bundles?limit=10`);
  if (!seen.ok()) throw new Error(`list versions: ${seen.status()}`);
  if (((await seen.json()) as { versions: unknown[] }).versions.length > 1) return; // already seeded (a reused server)

  const v2 = await upload(admin, "policy-v2.yaml");
  if (v2.warnings.length === 0) throw new Error("policy-v2.yaml carries no warning: the activation dialog's accept-by-id has nothing to show");
  const res = await admin.post(`${API}/bundles/${v2.version}/activate`, { data: { accept: v2.warnings } });
  if (!res.ok()) throw new Error(`activate v${v2.version}: ${res.status()} ${await res.text()}`);
  await upload(admin, "policy-v3.yaml");
}

/**
 * The candidates an author writes in the browser checks: one the compiler
 * refuses twice — a group the bundle does not declare, and a rule id used
 * twice — with the line each is reported at (the compiler names a rule's
 * line, `- id:`), and one it accepts, which is v3 reworded (so uploading it
 * stores a new version rather than finding v3).
 */
export function candidates(): { invalid: string; lines: { group: number; duplicate: number }; valid: string } {
  const v1 = source("policy.yaml");
  const lines = v1.split("\n");
  // The second `groups: [sre]` is the first rule's subject (the first declares the bundle's groups).
  const declared = lines.findIndex((l) => l.trim() === "groups: [sre]");
  const group = lines.findIndex((l, i) => i > declared && l.trim() === "groups: [sre]");
  const duplicate = lines.findIndex((l) => l.trim() === "- id: vault-through-dmz");
  lines[group] = (lines[group] as string).replace("[sre]", "[sre-oncall]");
  lines[duplicate] = (lines[duplicate] as string).replace("vault-through-dmz", "ops-db-shell");
  const valid = source("policy-v3.yaml").replace(
    "description: the console browser checks' fixture policy, with an office path",
    "description: the console browser checks' fixture policy, as an author rewrote it",
  );
  const rule = lines.findIndex((l) => l.trim() === "- id: ops-db-shell");
  return { invalid: lines.join("\n"), lines: { group: rule + 1, duplicate: duplicate + 1 }, valid };
}
