// The emergency runbook's rules (0058; PLAN §5.2): the ban is written into the
// bundle's own top-level bans; each step waits for the one before it, so the
// runbook cannot finish without `cache_invalidate`; every listed session is
// ended, in batches.

import { describe, expect, it } from "vitest";
import type { RevocationReceipt } from "../src/api/types";
import { batches, finished, remaining, START, stepStates, validIdentifier, withBan } from "../src/screens/runbookModel";

const BASE = "schema_version: 1\ntenant: default\nrules:\n  - id: r\n    effect: deny\n    reason: x\n";
const receipt: RevocationReceipt = { type: "cache_invalidate", event_id: "ev-1", delivered: 2, covers_host_key_decisions: true };

describe("withBan", () => {
  it("adds a top-level algorithm_bans when the bundle has none", () => {
    const b = withBan(BASE, "ciphers", "aes128-ctr");
    expect(b).toMatchObject({ already: false, edited: true });
    expect(b.source).toBe(`${BASE}\nalgorithm_bans:\n  ciphers: [aes128-ctr]\n`);
  });

  it("adds to an axis's flow list, keeping the others", () => {
    const src = `${BASE}algorithm_bans:\n  macs: [hmac-sha1]\n  ciphers: [3des-cbc] # old\n`;
    const b = withBan(src, "ciphers", "aes128-ctr");
    expect(b.edited).toBe(true);
    expect(b.source).toContain("  ciphers: [3des-cbc, aes128-ctr] # old\n");
    expect(b.source).toContain("  macs: [hmac-sha1]\n");
  });

  it("adds an axis the block does not have, at the block's indentation", () => {
    const src = `algorithm_bans:\n    macs: [hmac-sha1]\nrules: []\n`;
    expect(withBan(src, "ciphers", "aes128-ctr").source).toBe("algorithm_bans:\n    ciphers: [aes128-ctr]\n    macs: [hmac-sha1]\nrules: []\n");
  });

  it("adds to a block list", () => {
    const src = "algorithm_bans:\n  ciphers:\n    - 3des-cbc\nrules: []\n";
    expect(withBan(src, "ciphers", "aes128-ctr").source).toBe("algorithm_bans:\n  ciphers:\n    - 3des-cbc\n    - aes128-ctr\nrules: []\n");
  });

  it("says the ban is already in force when the name is there", () => {
    expect(withBan("algorithm_bans:\n  ciphers: [aes128-ctr]\n", "ciphers", "aes128-ctr")).toMatchObject({ already: true, edited: false });
    expect(withBan("algorithm_bans:\n  ciphers:\n    - 'aes128-ctr'\n", "ciphers", "aes128-ctr").already).toBe(true);
    // A route's own ban is not a ban everywhere.
    expect(withBan(`${BASE}    algorithm_bans:\n      ciphers: [aes128-ctr]\n`, "ciphers", "aes128-ctr").already).toBe(false);
  });

  it("leaves a shape it does not edit to the operator", () => {
    const src = "algorithm_bans: {ciphers: [3des-cbc]}\n";
    expect(withBan(src, "ciphers", "aes128-ctr")).toEqual({ source: src, already: false, edited: false });
  });
});

it("accepts identifiers as SSH spells them, and nothing that would change the YAML", () => {
  for (const ok of ["aes128-ctr", "chacha20-poly1305@openssh.com", "hmac-sha2-256-etm@openssh.com", "ssh-rsa"]) expect(validIdentifier(ok)).toBe(true);
  for (const bad of ["", " aes", "a b", "x]", "-x", "@x", "x,y", "a#b"]) expect(validIdentifier(bad)).toBe(false);
});

describe("the steps", () => {
  it("each waits for the one before it", () => {
    expect(stepStates(START)).toEqual(["ready", "waiting", "waiting"]);
    const banned = { ...START, ban: { version: 4, via: "applied" as const } };
    expect(stepStates(banned)).toEqual(["done", "ready", "waiting"]);
    expect(stepStates({ ...banned, invalidated: receipt })).toEqual(["done", "done", "ready"]);
  });

  it("cannot finish without cache_invalidate, even with every session ended", () => {
    const sessions = [{ session_id: "s1", latest_recorded_at: "2026-11-01T08:00:00Z" }];
    const skipped = {
      ban: { version: 4, via: "applied" as const },
      found: { sessions, pages: 1 },
      killed: { receipts: [{ ...receipt, type: "session_kill" }], ended: ["s1"] },
    };
    expect(stepStates(skipped)[2]).toBe("waiting");
    expect(finished(skipped)).toBe(false);
    expect(finished({ ...skipped, invalidated: receipt })).toBe(true);
  });

  it("is done at step 3 only once every listed session is ended", () => {
    const sessions = ["s1", "s2", "s3"].map((id) => ({ session_id: id, latest_recorded_at: "2026-11-01T08:00:00Z" }));
    const p = { ban: { version: 4, via: "active" as const }, invalidated: receipt, found: { sessions, pages: 2 }, killed: { receipts: [], ended: ["s2"] } };
    expect(remaining(p)).toEqual(["s1", "s3"]);
    expect(finished(p)).toBe(false);
    expect(finished({ ...p, killed: { receipts: [], ended: ["s2", "s1", "s3"] } })).toBe(true);
    // Finding none is done too: there was nothing running to end.
    expect(finished({ ...p, found: { sessions: [], pages: 1 } })).toBe(true);
  });
});

it("batches every session, none dropped", () => {
  const ids = Array.from({ length: 1201 }, (_, i) => `s${i}`);
  const b = batches(ids);
  expect(b.map((x) => x.length)).toEqual([500, 500, 201]);
  expect(b.flat()).toEqual(ids);
});
