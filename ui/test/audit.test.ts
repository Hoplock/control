// What the audit view decides from its URL and from a record (0051; PLAN M19,
// M26, §7), held without a browser.

import { describe, expect, it } from "vitest";
import type { AuditRecord } from "../src/api/types";
import { readRange, rangeQuery, resolveRange } from "../src/components/TimeRange";
import {
  actorOf,
  countByKind,
  countBySeverity,
  knownAttributes,
  readLimit,
  readRecordFilters,
  recordsQuery,
  terminalText,
} from "../src/screens/auditModel";

function record(over: Partial<AuditRecord> = {}): AuditRecord {
  return {
    record_id: "rec-1",
    stream: "control",
    chain_seq: 1,
    hash: "h",
    session_id: "",
    kind: "policy_decision",
    severity: "warn",
    recorded_at: "2026-11-01T08:00:00Z",
    received_at: "2026-11-01T08:00:00Z",
    body: "{}",
    ...over,
  };
}

describe("the record filters are the URL's (DESIGN.md §4)", () => {
  it("reads each filter the query takes, and drops a kind or severity outside the closed sets", () => {
    const f = readRecordFilters(new URLSearchParams("subject=grace&kind=command&severity=loud&principal=sup-acme&x=1"));
    expect(f).toEqual({ subject: "grace", kind: "command", principal: "sup-acme" });
    expect(readRecordFilters(new URLSearchParams("kind=bogus"))).toEqual({});
  });
  it("asks the API for exactly those filters, the window, the order and the page", () => {
    const q = recordsQuery({ session: "sess-1", principal: "sup-acme" }, { from: "2026-11-01T00:00:00.000Z" }, { limit: 100, after: "c1", ascending: true });
    expect(Object.fromEntries(q)).toEqual({
      session: "sess-1",
      principal: "sup-acme",
      from: "2026-11-01T00:00:00.000Z",
      order: "asc",
      limit: "100",
      after: "c1",
    });
  });
  it("keeps a limit within the API's bounds, and refuses to invent one above them (M26.1)", () => {
    expect(readLimit(new URLSearchParams("limit=250"))).toBe(250);
    expect(readLimit(new URLSearchParams("limit=10001"))).toBe(100);
    expect(readLimit(new URLSearchParams("limit=0"))).toBe(100);
    expect(readLimit(new URLSearchParams(""), 500)).toBe(500);
  });
});

describe("the time range", () => {
  it("is relative, absolute or all time, by the URL", () => {
    expect(readRange(new URLSearchParams("range=24h"))).toEqual({ kind: "relative", last: "24h" });
    expect(readRange(new URLSearchParams("from=2026-11-01T08:00:00Z"))).toEqual({
      kind: "absolute",
      from: "2026-11-01T08:00:00.000Z",
      to: undefined,
    });
    expect(readRange(new URLSearchParams("range=3y"))).toEqual({ kind: "all" });
    expect(readRange(new URLSearchParams("from=yesterday"))).toEqual({ kind: "all" });
  });
  it("writes every key it owns, so switching kinds leaves nothing behind", () => {
    expect(rangeQuery({ kind: "relative", last: "1h" })).toEqual({ range: "1h", from: undefined, to: undefined });
    expect(rangeQuery({ kind: "all" })).toEqual({ range: undefined, from: undefined, to: undefined });
  });
  it("resolves a relative range against the instant it was asked at", () => {
    const now = Date.parse("2026-11-08T12:00:00Z");
    expect(resolveRange({ kind: "relative", last: "1h" }, now)).toEqual({ from: "2026-11-08T11:00:00.000Z" });
    expect(resolveRange({ kind: "all" }, now)).toEqual({});
  });
});

describe("who acted (M19, 0048)", () => {
  it("shows a supervisor's word beside its principal", () => {
    const r = record({ attributes: { principal_id: "sup-acme", acting_operator: "Dana Ruiz" } });
    expect(actorOf(r)).toEqual({ principal: "sup-acme", acting: "Dana Ruiz", supervisor: true });
  });
  it("shows the principal alone when the record carries no word", () => {
    expect(actorOf(record({ attributes: { principal_id: "sup-acme" } }))).toEqual({ principal: "sup-acme", acting: undefined, supervisor: true });
  });
  it("never shows a word beside a principal that is not a supervisor's", () => {
    expect(actorOf(record({ attributes: { principal_id: "tok-1", acting_operator: "Dana" } })).acting).toBeUndefined();
  });
  it("renders the attribute keys it knows and skips one it does not (docs/API-VERSIONING.md)", () => {
    const r = record({ attributes: { event: "operator.target.put", principal_id: "sup-acme", acting_operator: "Dana", "operator.zone": "core", x_new: "1" } });
    expect(knownAttributes(r).map(([k]) => k)).toEqual(["event", "principal_id", "acting_operator"]);
  });
});

it("counts the records on the page by kind and severity", () => {
  const rs = [record({ kind: "command" }), record({ kind: "command", severity: "critical" }), record({ kind: "auth", severity: "info" })];
  expect(countByKind(rs)).toEqual([
    { kind: "command", count: 2 },
    { kind: "auth", count: 1 },
  ]);
  expect(countBySeverity(rs)).toEqual({ info: 1, warn: 1, critical: 1 });
});

describe("a capture read as a terminal", () => {
  const bytes = (s: string) => new TextEncoder().encode(s);
  it("joins chunks as bytes, so a character split across two reads survives", () => {
    const e = bytes("é");
    expect(terminalText([e.slice(0, 1), e.slice(1)])).toBe("é");
  });
  it("drops colour and cursor escapes, and applies carriage return and backspace", () => {
    expect(terminalText([bytes("\x1b[1;32mok\x1b[0m\r\n")])).toBe("ok\n");
    expect(terminalText([bytes("10%\r55%\r100%\n")])).toBe("100%\n");
    expect(terminalText([bytes("lss\b -l\n")])).toBe("ls -l\n");
    expect(terminalText([bytes("\x1b]0;title\x07$ whoami\r\n")])).toBe("$ whoami\n");
  });
});
