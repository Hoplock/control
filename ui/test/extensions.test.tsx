// The Extensions screen's rules (0056; PLAN M15, M8, M11), held without a
// browser: what each point reads as, each sink's lag on its own row, and
// that nothing a downstream consumer or a failed Start does reads as an
// outage. The browser checks hold the same against the fixture host.

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { ExtensionPoint, FeedLag } from "../src/api/types";
import { lagStatus } from "../src/components/FeedLag";
import { STATUS } from "../src/components/Status";
import { I18nProvider } from "../src/i18n/I18n";
import { Point } from "../src/screens/Extensions";
import { extendedBy, gaps, POINTS, pointStatus, START_ERRORS } from "../src/screens/extensionsModel";

function point(code: string, over: Partial<ExtensionPoint> = {}): ExtensionPoint {
  return {
    point: code,
    interface: code,
    multiple: false,
    registered: false,
    registrations: [],
    when_absent: "disabled",
    absent: "the server's English, which the console never shows",
    ...over,
  };
}

const ext = (provider: string, lag?: FeedLag) => ({ provider, version: "1", default: false, lag });

function render(p: ExtensionPoint): string {
  return renderToStaticMarkup(
    <I18nProvider locale="en">
      <Point point={p} name={p.point} />
    </I18nProvider>,
  );
}

/** The tones a rendered point shows: outage must never be among them. */
const tones = (html: string) => Array.from(html.matchAll(/data-tone="([a-z]+)"/g), (m) => m[1]);

const lifecycle = ["ActionHandler", "IdentitySync"] as const;

describe("a handler or a sync (0039, 0040)", () => {
  for (const code of lifecycle) {
    it(`${code}: registered and started reads as Running`, () => {
      const p = point(code, { registered: true, registrations: [ext("acme")], started: true });
      expect(pointStatus(p)).toBe("running");
      const html = render(p);
      expect(html).toContain('data-point-status="running"');
      expect(html).toContain("Running");
      expect(tones(html)).not.toContain("outage");
    });

    it(`${code}: registered and not started reads as Not started with the error's kind, never an outage`, () => {
      const p = point(code, { registered: true, registrations: [ext("acme")], started: false, start_error: "unavailable" });
      expect(pointStatus(p)).toBe("notStarted");
      const html = render(p);
      expect(html).toContain('data-point-status="notStarted"');
      expect(html).toContain('data-start-error="unavailable"');
      expect(html).toContain("Not started");
      expect(html).toContain(">unavailable</code>");
      expect(tones(html)).not.toContain("outage");
      expect(STATUS.notStarted.tone).toBe("warn");
    });

    it(`${code}: none registered shows what Control does instead, and is not broken`, () => {
      const p = point(code);
      const html = render(p);
      expect(pointStatus(p)).toBe("notRegistered");
      expect(html).toContain('data-absent="disabled"');
      expect(html).not.toContain("server&#x27;s English");
      expect(html).not.toContain("data-start-error");
      expect(new Set(tones(html))).toEqual(new Set(["unknown"]));
    });
  }

  it("a kind this console does not know is still named, by its code", () => {
    const html = render(point("ActionHandler", { registered: true, registrations: [ext("acme")], started: false, start_error: "exploded" }));
    expect(html).toContain('data-start-error="exploded"');
    expect(html).toContain(">exploded</code>");
  });

  it("every kind ext reports has a sentence", () => {
    expect(Object.keys(START_ERRORS).sort()).toEqual(["conflict", "denied", "disabled", "internal", "invalid", "malformed", "not_found", "unavailable"]);
  });
});

describe("the sinks' lag (0026, M8)", () => {
  const behindFailing: FeedLag = { streams_behind: 4, oldest_unconfirmed_at: "2026-11-01T08:00:00Z", failing: true, skipped_by_retention: 0 };
  const overtaken: FeedLag = { streams_behind: 1, oldest_unconfirmed_at: "2026-11-01T08:00:00Z", failing: false, skipped_by_retention: 42 };
  const sinks = point("AuditSink", {
    multiple: true,
    when_absent: "core",
    registered: true,
    registrations: [ext("siem", behindFailing), ext("lake", overtaken)],
  });

  it("shows each sink's lag on its own row: the failing one as failing, the overtaken one with its count", () => {
    const html = render(sinks);
    const rows = html.split('data-registration="').slice(1);
    expect(rows).toHaveLength(2);
    expect(rows[0]).toMatch(/^siem"/);
    expect(rows[0]).toContain('data-lag-state="failing"');
    expect(rows[0]).not.toContain("data-retention-gap");
    expect(rows[1]).toMatch(/^lake"/);
    expect(rows[1]).toContain('data-lag-state="behind"');
    expect(rows[1]).toContain('data-skipped="42"');
    expect(rows[1]).toContain("42 records were removed by a retention pass");
    expect(rows[1]).toContain('href="/console/audit/records?kind=operator.audit.retained"');
  });

  it("a failing or overtaken sink is never an outage or a deny", () => {
    const html = render(sinks);
    expect(tones(html)).not.toContain("outage");
    expect(tones(html)).not.toContain("deny");
    expect(STATUS.failing.tone).toBe("warn");
  });

  it("a caught-up sink says so plainly", () => {
    const html = render(point("AuditSink", { registered: true, registrations: [ext("lake", { streams_behind: 0, failing: false, skipped_by_retention: 0 })] }));
    expect(html).toContain('data-lag-state="caughtUp"');
    expect(html).toContain("Caught up: every record in this tenant is confirmed.");
  });

  it("with none registered, no sink lag is shown and the point says what Control does instead", () => {
    const html = render(point("AuditSink", { when_absent: "core" }));
    expect(html).not.toContain("data-lag-state");
    expect(html).not.toContain("data-sink");
    expect(html).toContain('data-absent="core"');
    expect(html).toContain("exported nowhere else");
  });

  it("a sink whose lag was not reported is Not reported and a named gap, never caught up", () => {
    const p = point("AuditSink", { registered: true, registrations: [ext("siem")] });
    expect(render(p)).toContain('data-lag-state="notReported"');
    expect(gaps([p])).toEqual([{ kind: "sinkLag", provider: "siem" }]);
  });

  it("reads failing before behind before caught up", () => {
    expect(lagStatus({ streams_behind: 3, failing: true })).toBe("failing");
    expect(lagStatus({ streams_behind: 3, failing: false })).toBe("behind");
    expect(lagStatus({ streams_behind: 0, failing: false })).toBe("caughtUp");
  });
});

describe("the listing as a whole (M15)", () => {
  it("names every point ext declares", () => {
    expect(Object.keys(POINTS)).toEqual([
      "AuditSink",
      "ArchiveStore",
      "GrantWorkflow",
      "IdentitySync",
      "KeyStore",
      "Notifier",
      "ClusterCoordinator",
      "ActionHandler",
      "ReportProvider",
      "PolicyValidator",
      "AccessContextProvider",
    ]);
  });

  it("says what is in play from outside Control, and not Control's own defaults", () => {
    const points = [
      point("ClusterCoordinator", { registered: true, registrations: [{ provider: "hoplock/control", default: true }] }),
      point("IdentitySync", { registered: true, registrations: [ext("acme-scim")], started: true }),
    ];
    expect(extendedBy(points)).toEqual([{ provider: "acme-scim", point: "IdentitySync" }]);
  });

  it("names a point this console does not know as a gap, and the archive's missing lag", () => {
    expect(gaps([point("Telepathy"), point("ArchiveStore", { registered: true, registrations: [ext("vault")] })])).toEqual([
      { kind: "unknownPoint", point: "Telepathy" },
      { kind: "archiveLag" },
    ]);
  });
});
