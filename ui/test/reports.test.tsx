// The Reports screen's rules (0056; PLAN §7 "Reports", M15, M21, M27, M11),
// held without a browser: the form is the descriptor's and posts provider
// and report exactly as listed; a pack listed with an error is unavailable
// and never "no reports"; a report is named through its host's catalogue and
// by its code where none names it; and a body that breaks is a failed
// generation, never a file. The browser checks hold the same against the
// fixture host's packs.

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ApiClient, attachmentName, type Fetch } from "../src/api/client";
import type { ReportDescriptor, ReportProvider } from "../src/api/types";
import { STATUS } from "../src/components/Status";
import { localInput } from "../src/i18n/format";
import { HostCatalogueContext, hostText, reportNameKey, reportParameterKey, type HostCatalogue } from "../src/i18n/host";
import { I18nProvider } from "../src/i18n/I18n";
import { Provider } from "../src/screens/Reports";
import { check, faultOf, initialForm, requestBody, unavailable } from "../src/screens/reportsModel";

const PACK = "example.com/acme/compliance";
const review: ReportDescriptor = {
  report: "access-review",
  formats: ["csv", "json"],
  parameters: [
    { name: "scope", required: true, values: ["all", "privileged"] },
    { name: "owner", required: false, values: [] },
  ],
};

function render(p: ReportProvider, host: HostCatalogue | null = null): string {
  return renderToStaticMarkup(
    <I18nProvider locale="en">
      <HostCatalogueContext.Provider value={host}>
        <Provider provider={p} open={null} onOpen={() => {}} />
      </HostCatalogueContext.Provider>
    </I18nProvider>,
  );
}

describe("the form is the descriptor's (0044)", () => {
  const now = Date.parse("2026-11-11T12:34:56Z");

  it("opens on the last seven days to the minute, every parameter empty, the first format chosen", () => {
    const f = initialForm(review, now);
    expect(f.to).toBe(localInput(new Date(Date.parse("2026-11-11T12:34:00Z"))));
    expect(f.from).toBe(localInput(new Date(Date.parse("2026-11-04T12:34:00Z"))));
    expect(f.parameters).toEqual({ scope: "", owner: "" });
    expect(f.format).toBe("csv");
  });

  it("refuses a missing required parameter and a period that does not start before it ends, at their fields", () => {
    const f = { ...initialForm(review, now), from: "2026-11-02T00:00:00", to: "2026-11-01T00:00:00" };
    expect(check(review, f)).toEqual([
      { field: "to", problem: "not_after_start" },
      { field: "parameters.scope", problem: "required" },
    ]);
    expect(check(review, { ...f, from: "", to: "" })).toEqual([
      { field: "from", problem: "required" },
      { field: "to", problem: "required" },
      { field: "parameters.scope", problem: "required" },
    ]);
  });

  it("posts provider and report exactly as listed, the period in UTC, and leaves empty parameters out", () => {
    const f = { from: "2026-11-01T00:00:00", to: "2026-11-02T00:00:00", parameters: { scope: "privileged", owner: "" }, format: "json" as const };
    const body = requestBody(PACK, review, f);
    expect(body).toEqual({
      provider: PACK,
      report: "access-review",
      from: new Date("2026-11-01T00:00:00").toISOString(),
      to: new Date("2026-11-02T00:00:00").toISOString(),
      parameters: { scope: "privileged" },
      format: "json",
    });
  });

  it("puts a server's refusal at the field it names", () => {
    expect(faultOf({ code: "invalid_request", parameters: { field: "parameters.scope", problem: "invalid" } })).toEqual({
      field: "parameters.scope",
      problem: "invalid",
    });
    expect(faultOf({ code: "not_found" })).toBeNull();
  });
});

describe("a pack and its reports (M15, M11)", () => {
  it("a pack listed with an error is Unavailable, an outage, and never a pack with no reports", () => {
    const html = render({ provider: "example.com/acme/flaky", reports: [], error: "report_provider_unavailable" });
    expect(html).toContain('data-provider-state="unavailable"');
    expect(html).toContain('data-provider-error="report_provider_unavailable"');
    expect(html).toContain("Unavailable");
    expect(html).not.toContain("data-no-reports");
    expect(html).not.toContain("offers no report");
    expect(STATUS.unavailable.tone).toBe("outage");
    expect(unavailable({ providers: [{ provider: "a", reports: [] }, { provider: "b", reports: [], error: "report_provider_failed" }] })).toHaveLength(1);
  });

  it("a pack that answered with no reports says so, and is not unavailable", () => {
    const html = render({ provider: PACK, reports: [] });
    expect(html).toContain("data-no-reports");
    expect(html).not.toContain("Unavailable");
  });

  it("names a report by its code, untranslated, where no catalogue names it (M21)", () => {
    const html = render({ provider: PACK, version: "1.4.0", reports: [review] });
    expect(html).toContain('data-named-by="code"');
    expect(html).toContain('translate="no">access-review</code>');
    expect(html).toContain("CSV");
    expect(html).toContain("JSON");
  });

  it("names a report through the catalogue of the host that registered it, under its <host>: keys (M27)", () => {
    const host: HostCatalogue = { host: "hoplock/enterprise", messages: { [reportNameKey("hoplock/enterprise", PACK, "access-review")]: "Quarterly access review" } };
    const html = render({ provider: PACK, reports: [review] }, host);
    expect(html).toContain("Quarterly access review");
    expect(html).not.toContain('data-named-by="code"');
    expect(reportNameKey("hoplock/enterprise", PACK, "access-review")).toBe(`hoplock/enterprise:reports.${PACK}.access-review`);
    expect(reportParameterKey("hoplock/enterprise", PACK, "access-review", "scope")).toBe(`hoplock/enterprise:reports.${PACK}.access-review.parameters.scope`);
  });

  it("never reads a key that is not the host's own, or one Control could hold", () => {
    const host: HostCatalogue = { host: "hoplock/enterprise", messages: { "reports.title": "x", "other/host:reports.a.b": "y" } };
    expect(hostText(host, "reports.title", "en")).toBeUndefined();
    expect(hostText(host, "other/host:reports.a.b", "en")).toBeUndefined();
    expect(hostText(null, reportNameKey("hoplock/enterprise", PACK, "x"), "en")).toBeUndefined();
  });
});

describe("a generated file (0044)", () => {
  const respond = (r: () => Response): Fetch => async () => r();

  it("takes the filename the server gave, and nothing else", () => {
    expect(attachmentName('attachment; filename="access-review_20261101T000000Z_20261102T000000Z.csv"')).toBe(
      "access-review_20261101T000000Z_20261102T000000Z.csv",
    );
    expect(attachmentName('attachment; filename="../etc/passwd"')).toBeNull();
    expect(attachmentName(null)).toBeNull();
  });

  it("a body that breaks after its first bytes is broken, never a file", async () => {
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        c.enqueue(new TextEncoder().encode("session,subject\n"));
        c.error(new TypeError("network error"));
      },
    });
    const api = new ApiClient(respond(() => new Response(body, { status: 200, headers: { "Content-Disposition": 'attachment; filename="r.csv"' } })));
    const got = await api.download("reportGenerate", { params: { tenant: "t" }, body: {} });
    expect(got.kind).toBe("broken");
  });

  it("a whole body is a file with its name; a 502 is an outage; a 400 is a refusal", async () => {
    const ok = new ApiClient(respond(() => new Response("a,b\n", { status: 200, headers: { "Content-Disposition": 'attachment; filename="r.csv"' } })));
    const got = await ok.download("reportGenerate", { params: { tenant: "t" }, body: {} });
    expect(got.kind === "ok" && got.filename).toBe("r.csv");
    const failed = new ApiClient(respond(() => Response.json({ code: "report_provider_failed" }, { status: 502 })));
    expect((await failed.download("reportGenerate", { params: { tenant: "t" }, body: {} })).kind).toBe("outage");
    const refused = new ApiClient(respond(() => Response.json({ code: "invalid_request", parameters: { field: "format" } }, { status: 400 })));
    expect((await refused.download("reportGenerate", { params: { tenant: "t" }, body: {} })).kind).toBe("refused");
  });
});
