// Standing denials' and the directory's fixture (0055): what neither the seed
// nor the directory section can write, made through the surfaces an operator
// and a proxy use and nothing else —
//   - locks (north-bound, as an administrator): noor, an operator's lock for
//     an incident; sam, a person the directory also deactivated (both stand);
//     ex-contractor, locked and then removed, so the lock alone lists it;
//   - a quarantine: cache-01, for the same incident;
//   - the claim mapping: version 1, whose group entries are scoped to the two
//     directories, `corp` and `lab`, so one group name maps two ways; the
//     fixture waits until every directory identity is on it;
//   - the sign-in connectors (the host's CLI, which is how an operator ties
//     one: there is no route for it): corp-sso, tied to the `corp` directory
//     by the claim employee_number, and partner-sso, untied;
//   - a decision about dana (corp 00u1), so Explain has a provisioned
//     subject's decision to show: session sess-e2e-directory.
// The directory's subjects themselves are the fixture host's (control.yaml,
// `e2e_directory`). Their ids are Control's to allocate, at random, so the
// fixture records each by its directory and id in ui/build/e2e/standing.json,
// and a screen that opens one reads it there, inside a test. Run after
// seedInventory (e2e/screens.ts, SEEDS).

import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import type { APIRequestContext } from "@playwright/test";
import { SOUTH, TENANT, UI } from "../harness";

const API = `/api/v1/tenants/${TENANT}`;
const IDS = join(UI, "build", "e2e", "standing.json");
const WORK = join(UI, "build", "e2e");

/** The directory identities the host provisions, by `<source>/<external id>`. */
export const DIRECTORY = {
  dana: "corp/00u1",
  danaLab: "lab/00u1",
  eli: "corp/00u7",
  sam: "corp/00u9",
  early: "corp/00u4",
} as const;

/** A directory identity's subject id, read inside a test (the fixture has run by then). */
export function fixtureSubject(ref: string): string {
  const ids = JSON.parse(readFileSync(IDS, "utf8")) as Record<string, string>;
  const id = ids[ref];
  if (!id) throw new Error(`the fixture holds no directory identity ${ref}`);
  return id;
}

export const MAPPING = `schema_version: 1
description: The corporate directory and the lab's, scoped apart
claims:
  - claim: department
    attribute: department
groups:
  claim: groups
  map:
    - external: Payments Engineering
      directories: [corp]
      group: payments-eng
    - external: Payments Engineering
      directories: [lab]
      group: payments-lab
    - external: sre
      group: sre
`;

interface Listed {
  id: string;
  locked: boolean;
  directory?: { source: string; external_id: string };
}

async function ok(what: string, res: Awaited<ReturnType<APIRequestContext["post"]>>): Promise<unknown> {
  if (!res.ok()) throw new Error(`${what}: ${res.status()} ${await res.text()}`);
  return res.status() === 204 ? undefined : res.json();
}

/** Every directory identity's id, from the identities and the ones with no record. */
async function directoryIds(admin: APIRequestContext): Promise<Record<string, string>> {
  const ids: Record<string, string> = {};
  for (const query of ["limit=1000", "record=missing&limit=1000"]) {
    const page = (await ok(`list subjects ?${query}`, await admin.get(`${API}/subjects?${query}`))) as { subjects: Listed[] };
    for (const s of page.subjects) if (s.directory) ids[`${s.directory.source}/${s.directory.external_id}`] = s.id;
  }
  for (const ref of Object.values(DIRECTORY)) {
    if (!ids[ref]) throw new Error(`the fixture host did not provision ${ref}: is the server the fixture host (serve.sh)?`);
  }
  mkdirSync(dirname(IDS), { recursive: true });
  writeFileSync(IDS, JSON.stringify(ids, null, 2));
  return ids;
}

async function lock(admin: APIRequestContext, subject: string, reason_code: string, reason: string): Promise<void> {
  await ok(`lock ${subject}`, await admin.post(`${API}/subjects/${encodeURIComponent(subject)}/lock`, { data: { reason_code, reason } }));
}

/** Ties a connector through the host's CLI, as an operator does: there is no route for it (0040). */
function connector(name: string, display: string, tie?: { claim: string; source: string }): void {
  const file = join(WORK, `connector-${name}.json`);
  writeFileSync(
    file,
    JSON.stringify({
      issuer: `https://${name}.idp.example`,
      client_id: `hoplock-${name}`,
      redirect_uri: `http://127.0.0.1:18081/console/sign-in/callback/${name}`,
      scopes: ["openid", "profile", "groups"],
    }),
  );
  const args = ["identity", "connector-put", "--config", join(WORK, "control.yaml"), "--name", name, "--kind", "oidc", "--display-name", display, "--file", file];
  if (tie) args.push("--directory-claim", tie.claim, "--directory-source", tie.source);
  execFileSync(join(WORK, "hoplock-control"), args, { stdio: ["ignore", "ignore", "pipe"] });
}

async function mappingSettled(admin: APIRequestContext): Promise<void> {
  for (let i = 0; i < 120; i++) {
    const m = (await ok("read the claim mapping", await admin.get(`${API}/identity/claim-mapping`))) as { stale_directory_subjects: number };
    if (m.stale_directory_subjects === 0) return;
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error("the directory identities were not brought onto the fixture's claim mapping in a minute");
}

export async function seedStanding(admin: APIRequestContext): Promise<void> {
  const ids = await directoryIds(admin);
  // Once per server: a reused local server already holds these.
  const noor = (await ok("read noor", await admin.get(`${API}/subjects/noor`))) as Listed;
  if (noor.locked) return;

  await lock(admin, "noor", "INC-4821", "Your access is suspended while incident INC-4821 is investigated. Call the SOC on 4821.");
  await lock(admin, ids[DIRECTORY.sam] as string, "SEC-77", "Your access is suspended pending a security review.");
  await lock(admin, "ex-contractor", "offboarding", "This account was closed at the end of your contract.");
  await ok("remove ex-contractor", await admin.delete(`${API}/subjects/ex-contractor`));
  await ok(
    "quarantine cache-01",
    await admin.post(`${API}/targets/cache-01/quarantine`, { data: { reason_code: "INC-4821", reason: "Suspected compromise of the edge cache; forensics under way." } }),
  );

  await ok("write the claim mapping", await admin.put(`${API}/identity/claim-mapping`, { data: { document: MAPPING } }));
  await mappingSettled(admin);

  connector("corp-sso", "Corporate SSO", { claim: "employee_number", source: "corp" });
  connector("partner-sso", "Partner SSO");

  // A decision about a subject the directory provisioned (0043): its record
  // carries the directory its groups came from, which Explain shows.
  const res = await admin.post(`${SOUTH}/v1/authorize`, {
    headers: { authorization: `Bearer ${TENANT}.e2e-px-core-01` },
    data: {
      identity: { subject: ids[DIRECTORY.dana], login: "dana", source: "e2e-directory", groups: [] },
      target: "api-01.core.internal",
      target_port: 22,
      auth_method: "publickey",
      policy_version: 6,
      conn: { session_id: "sess-e2e-directory", proxy_id: "px-core-01", client_addr: "10.20.0.11:52210", timestamp: "2026-11-01T09:00:00Z" },
    },
  });
  if (res.status() !== 200 && res.status() !== 401) throw new Error(`authorize dana: ${res.status()} ${await res.text()}`);
}
