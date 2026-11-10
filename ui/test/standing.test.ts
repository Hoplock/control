// STANDING DENIALS AND THE CLAIM MAPPING'S CODES (0055; PLAN M28, M11, M21).
// Held without a browser:
//   1. a lock, a quarantine and the directory's deactivation are deny-class
//      statuses — a deny's tone and words, never an outage's (M11) — and a
//      subject holding a lock and a deactivation shows both, the lock first,
//      as the record holds them;
//   2. the text a lock's holder is shown is held to the server's rule as it
//      is typed (one line, at most 512 bytes), and left empty it is the
//      server's own words;
//   3. who placed a denial reads as a person, an inbound action's handler,
//      the directory's sync, or a credential;
//   4. every code a claim-mapping write can be refused with has a sentence,
//      held to ./mapping-codes.json, which a Go test keeps equal to the
//      server's (internal/identity/console_codes_test.go), and a code the
//      console does not know renders the named fallback.

import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { STATUS } from "../src/components/Status";
import { translator } from "../src/i18n/messages";
import en from "../src/locales/en.json";
import { MAPPING_REJECTIONS, rejectionKey } from "../src/screens/claimMappingCodes";
import {
  DEFAULT_LOCK_TEXT,
  holderText,
  isUndelivered,
  lineProblem,
  MAX_HOLDER_TEXT_BYTES,
  placerOf,
  subjectDenials,
  targetDenials,
} from "../src/screens/standingModel";

const OUTAGE = [STATUS.unreachable, STATUS.fetchFailed, STATUS.unserved];
const STANDING = [STATUS.locked, STATUS.quarantined, STATUS.deactivated];

describe("M11: a standing denial is a deny made on purpose, never an outage", () => {
  it.each(STANDING.map((s) => [s.label, s] as const))("%s is deny-class", (_, s) => {
    expect(s.tone).toBe("deny");
    for (const o of OUTAGE) {
      expect(s.tone).not.toBe(o.tone);
      expect(en[s.label]).not.toBe(en[o.label]);
    }
    expect(en[s.label]).not.toMatch(/outage|unavailable|unreachable|failed/i);
  });
  it("names three different states: a lock is not a deactivation", () => {
    expect(new Set(STANDING.map((s) => en[s.label])).size).toBe(3);
  });
  it("shows a lock and a deactivation both, the lock first", () => {
    expect(subjectDenials({ locked: true, deactivated: true })).toEqual(["locked", "deactivated"]);
    expect(subjectDenials({ locked: false, deactivated: true })).toEqual(["deactivated"]);
    expect(subjectDenials({ locked: false, deactivated: false })).toEqual([]);
    expect(targetDenials({ quarantined: true })).toEqual(["quarantined"]);
  });
  it("reads an undelivered denial as in force, by its code", () => {
    expect(isUndelivered({ code: "standing_denial_undelivered" })).toBe(true);
    expect(isUndelivered({ code: "unavailable" })).toBe(false);
    expect(en["error.standing_denial_undelivered"]).toMatch(/^In force/);
  });
});

describe("a lock's holder text", () => {
  it("is one line of at most 512 bytes, counted in UTF-8", () => {
    expect(lineProblem("Suspended while INC-4821 is investigated.", MAX_HOLDER_TEXT_BYTES)).toBeNull();
    expect(lineProblem("two\nlines", MAX_HOLDER_TEXT_BYTES)).toBe("control");
    expect(lineProblem("a\u001b[2Jclear", MAX_HOLDER_TEXT_BYTES)).toBe("control");
    expect(lineProblem("x".repeat(512), MAX_HOLDER_TEXT_BYTES)).toBeNull();
    expect(lineProblem("x".repeat(513), MAX_HOLDER_TEXT_BYTES)).toBe("too_long");
    // 171 three-byte characters are 513 bytes and 171 characters.
    expect(lineProblem("€".repeat(171), MAX_HOLDER_TEXT_BYTES)).toBe("too_long");
    expect(lineProblem("  padded  ", MAX_HOLDER_TEXT_BYTES)).toBeNull();
  });
  it("is the server's own words when left empty, and the trimmed text otherwise", () => {
    expect(holderText("   ")).toEqual({ text: DEFAULT_LOCK_TEXT, isDefault: true });
    expect(holderText("  Call the SOC.  ")).toEqual({ text: "Call the SOC.", isDefault: false });
    // internal/standing.LockedByAdministrator, verbatim.
    const service = readFileSync(new URL("../../internal/standing/service.go", import.meta.url), "utf8");
    expect(service).toContain(`LockedByAdministrator = ${JSON.stringify(DEFAULT_LOCK_TEXT)}`);
  });
});

describe("who placed a denial", () => {
  it("is an inbound action's handler, the directory's sync, a person or a credential", () => {
    expect(placerOf({ principal_id: "action-handler:hoplock/enterprise", break_glass: false })).toEqual({ kind: "handler", provider: "hoplock/enterprise" });
    expect(placerOf({ principal_id: "identity-sync:okta", break_glass: false })).toEqual({ kind: "sync", provider: "okta" });
    expect(placerOf({ subject: "ada", principal_id: "sess-1", break_glass: true })).toEqual({ kind: "person", subject: "ada", principal: "sess-1", breakGlass: true });
    expect(placerOf({ principal_id: "tok-1", break_glass: false })).toEqual({ kind: "credential", principal: "tok-1", breakGlass: false });
  });
});

describe("the claim mapping's rejection codes", () => {
  const server = (JSON.parse(readFileSync(new URL("./mapping-codes.json", import.meta.url), "utf8")) as { codes: string[] }).codes;
  const t = translator("en", en);
  it("are every code the server refuses a mapping with, and no other", () => {
    expect(Object.keys(MAPPING_REJECTIONS).sort()).toEqual([...server].sort());
  });
  it.each(server)("%s has a sentence", (code) => {
    const { key, known } = rejectionKey(code);
    expect(known).toBe(true);
    expect(t(key, { code })).not.toContain(code);
  });
  it("renders a code it does not know by naming it", () => {
    const { key, known } = rejectionKey("mapping.something_new");
    expect(known).toBe(false);
    expect(t(key, { code: "mapping.something_new" })).toContain("mapping.something_new");
  });
});
