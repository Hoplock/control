// What the audit view decides about a stream's holes and its retention (0057;
// PLAN M8, M11, §7), held without a browser: a gap is placed at its span and
// never at its report's timestamp, its cause is its status, two reports of
// one cause are summed, and a retention pass's record says whether the
// archive held it back.

import { describe, expect, it } from "vitest";
import type { AuditRecord, Gap } from "../src/api/types";
import { STATUS } from "../src/components/Status";
import {
  gapCritical,
  gapOfRecord,
  gapsByStream,
  gapStatus,
  isGapRecord,
  lossByCause,
  placeGaps,
  retainedOf,
} from "../src/screens/gapModel";

function record(id: string, at: string, over: Partial<AuditRecord> = {}): AuditRecord {
  return {
    record_id: id,
    stream: "proxy:px-1",
    chain_seq: 1,
    hash: "h",
    session_id: "s-1",
    kind: "stream",
    severity: "info",
    recorded_at: at,
    received_at: at,
    body: "{}",
    ...over,
  };
}

function gap(id: string, cause: string, first: string | undefined, over: Partial<Gap> = {}): Gap {
  return {
    record_id: id,
    stream: "proxy:px-1",
    chain_seq: 9,
    session_id: "s-1",
    severity: "warn",
    gap_cause: cause,
    gap_records: 3,
    gap_first_at: first,
    gap_last_at: first,
    reported_at: "2026-11-01T10:00:00Z",
    ...over,
  };
}

describe("a gap's cause is its status", () => {
  it("evicted is an outage, refused is refused at ingest, anything else is not reported", () => {
    expect(gapStatus("evicted")).toBe("evicted");
    expect(STATUS[gapStatus("evicted")].tone).toBe("outage");
    expect(gapStatus("refused")).toBe("refusedAtIngest");
    expect(STATUS[gapStatus("refused")].tone).not.toBe("deny");
    expect(STATUS[gapStatus("refused")].tone).not.toBe("outage");
    expect(gapStatus("dropped")).toBe("notReported");
    expect(gapStatus("")).toBe("notReported");
  });
  it("is never drawn as a deny", () => {
    for (const c of ["evicted", "refused", "other"]) expect(STATUS[gapStatus(c)].tone).not.toBe("deny");
  });
  it("is critical when it hid a critical record", () => {
    expect(gapCritical(gap("g", "refused", undefined, { gap_critical: 1 }))).toBe(true);
    expect(gapCritical(gap("g", "refused", undefined, { severity: "critical" }))).toBe(true);
    expect(gapCritical(gap("g", "evicted", undefined, { gap_critical: 0 }))).toBe(false);
  });
});

describe("a gap is placed at its span, never at its report's timestamp", () => {
  const records = [
    record("r1", "2026-11-01T09:00:00Z"),
    record("r2", "2026-11-01T09:01:00Z"),
    record("r3", "2026-11-01T09:05:00Z"),
    // The report itself, written long after: not a step.
    record("g-report", "2026-11-01T10:00:00Z", { kind: "error", event: "logging.gap", attributes: { event: "logging.gap" } }),
  ];
  it("puts each gap before the first record after its span starts, and the report record nowhere", () => {
    const { entries } = placeGaps(records, [gap("g-report", "evicted", "2026-11-01T09:02:00Z")]);
    expect(entries.map((e) => e.id)).toEqual(["r1", "r2", "g-report", "r3"]);
    expect(entries.find((e) => e.id === "g-report")?.kind).toBe("gap");
  });
  it("orders gaps by span, whatever order they were reported in, records first at one instant", () => {
    const { entries } = placeGaps(records, [
      gap("late", "evicted", "2026-11-01T09:06:00Z"),
      gap("early", "refused", "2026-11-01T09:00:30.5Z"),
      gap("tie", "evicted", "2026-11-01T09:01:00Z"),
    ]);
    expect(entries.map((e) => e.id)).toEqual(["r1", "early", "r2", "tie", "r3", "late"]);
  });
  it("lists a gap without a span apart, rather than at a time it does not have", () => {
    const { entries, unplaced } = placeGaps(records, [gap("nospan", "evicted", undefined)]);
    expect(entries.map((e) => e.id)).toEqual(["r1", "r2", "r3"]);
    expect(unplaced.map((g) => g.record_id)).toEqual(["nospan"]);
  });
  it("places a gap only on the page its span starts in", () => {
    const g = [gap("before", "evicted", "2026-11-01T08:00:00Z"), gap("after", "evicted", "2026-11-01T09:30:00Z")];
    expect(placeGaps(records, g, { from: "2026-11-01T09:00:00Z", to: "2026-11-01T09:05:00Z" }).entries.map((e) => e.id)).toEqual(["r1", "r2", "r3"]);
    expect(placeGaps(records, g, { to: "2026-11-01T09:05:00Z" }).entries.map((e) => e.id)).toEqual(["before", "r1", "r2", "r3"]);
    expect(placeGaps(records, g, { from: "2026-11-01T09:00:00Z" }).entries.map((e) => e.id)).toEqual(["r1", "r2", "r3", "after"]);
  });
  it("recognises a gap report by its event", () => {
    expect(isGapRecord(records[3] as AuditRecord)).toBe(true);
    expect(isGapRecord(records[0] as AuditRecord)).toBe(false);
  });
});

describe("a gap read from its record", () => {
  it("reads its keys leniently: absent or unparseable is absent, never zero", () => {
    const g = gapOfRecord(
      record("g", "2026-11-01T10:00:00Z", {
        kind: "error",
        severity: "critical",
        event: "logging.gap",
        session_id: "",
        attributes: {
          event: "logging.gap",
          gap_cause: "refused",
          gap_records: "2",
          gap_bytes: "many",
          gap_kinds: "command, policy_decision",
          gap_critical: "1",
          gap_first_at: "2026-11-01T09:04:30.12Z",
          gap_last_at: "2026-11-01T09:04:31Z",
          gap_record_ids: "rec-a,rec-b",
          gap_record_ids_truncated: "true",
          refusal_code: "invalid_record",
          refusal_message: "record 2 is malformed",
        },
      }),
    );
    expect(g).toMatchObject({
      gap_cause: "refused",
      gap_records: 2,
      gap_bytes: undefined,
      gap_kinds: ["command", "policy_decision"],
      gap_critical: 1,
      gap_record_ids: ["rec-a", "rec-b"],
      gap_record_ids_truncated: true,
      refusal_code: "invalid_record",
      session_id: undefined,
      reported_at: "2026-11-01T10:00:00Z",
    });
  });
});

describe("a session's loss is the sum of its reports per cause", () => {
  it("keeps the server's sums, in upstream's order, with the critical count of the reports held", () => {
    const rows = lossByCause(
      [
        { cause: "refused", reports: 1, records: 2, bytes: 10 },
        { cause: "evicted", reports: 2, records: 52, bytes: 100 },
      ],
      [gap("a", "evicted", "2026-11-01T09:00:00Z", { gap_critical: 0 }), gap("b", "evicted", "2026-11-01T09:05:00Z", { gap_critical: 2 }), gap("c", "refused", undefined, { gap_critical: 1 })],
    );
    expect(rows).toEqual([
      { cause: "evicted", reports: 2, records: 52, bytes: 100, critical: 2 },
      { cause: "refused", reports: 1, records: 2, bytes: 10, critical: 1 },
    ]);
  });
  it("says nothing about critical records when it does not hold every report", () => {
    expect(lossByCause([{ cause: "evicted", reports: 2, records: 52, bytes: 100 }], null)[0]?.critical).toBeUndefined();
  });
});

describe("the per-proxy view", () => {
  it("groups by stream and places each stream's gaps by span", () => {
    const by = gapsByStream([
      gap("b2", "evicted", "2026-11-01T09:05:00Z", { stream: "proxy:b", proxy_id: "b" }),
      gap("a1", "evicted", "2026-11-01T07:00:00Z", { stream: "proxy:a", proxy_id: "a", session_id: undefined }),
      gap("b1", "refused", "2026-11-01T09:00:00Z", { stream: "proxy:b", proxy_id: "b" }),
      gap("b0", "evicted", undefined, { stream: "proxy:b", proxy_id: "b" }),
    ]);
    expect(by.map((s) => [s.stream, s.proxy, s.placed.map((g) => g.record_id), s.unplaced.map((g) => g.record_id)])).toEqual([
      ["proxy:a", "a", ["a1"], []],
      ["proxy:b", "b", ["b1", "b2"], ["b0"]],
    ]);
  });
});

describe("a retention pass's record", () => {
  const pass = (attrs: Record<string, string>) =>
    record("ret", "2026-11-01T10:00:00Z", { stream: "control", kind: "policy_decision", event: "operator.audit.retained", attributes: { event: "operator.audit.retained", ...attrs } });
  it("says whether the archive held it back, and how far the archive holds the stream", () => {
    expect(
      retainedOf(pass({ after: "40", "operator.stream": "proxy:px-1", "operator.deleted": "40", "operator.held_by_archive": "true", "operator.archived_through_seq": "40" })),
    ).toEqual({ stream: "proxy:px-1", through: 40, deleted: 40, heldByArchive: true, archivedThrough: 40 });
  });
  it("says nothing about an archive when none was registered", () => {
    const r = retainedOf(pass({ after: "7", "operator.stream": "proxy:px-1", "operator.deleted": "7" }));
    expect(r.heldByArchive).toBeUndefined();
    expect(r.archivedThrough).toBeUndefined();
  });
});
