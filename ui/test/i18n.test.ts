import { describe, expect, it } from "vitest";
import { english, type MessageKey } from "../src/i18n/catalogue";
import { errorMessage, ERROR_MESSAGES } from "../src/i18n/errors";
import { formatters, isoUTC } from "../src/i18n/format";
import { DEFAULT_LOCALE, resolveLocale } from "../src/i18n/locale";
import { translator } from "../src/i18n/messages";

const t = translator("en", english);

describe("locale resolution, in the browser (M21)", () => {
  it("takes the stored preference, then the browser's, then the default", () => {
    expect(resolveLocale("en", ["fr"], ["en", "fr"])).toBe("en");
    expect(resolveLocale(null, ["de-CH", "fr-FR"], ["en", "fr"])).toBe("fr");
    expect(resolveLocale(undefined, ["de"], ["en"])).toBe(DEFAULT_LOCALE);
    expect(resolveLocale("en-GB", [])).toBe("en");
  });
});

describe("messages", () => {
  it("own their plurals: the caller never concatenates", () => {
    expect(t("chrome.deployment.nodes", { count: 1, versions: "v1" })).toBe("1 node: v1");
    expect(t("chrome.deployment.nodes", { count: 3, versions: "v1" })).toBe("3 nodes: v1");
  });
});

describe("an API error, from its code and parameters (0019)", () => {
  it("is the catalogue's sentence for a code the console knows, never the server's English", () => {
    const r = errorMessage(t, { code: "forbidden", message: "the server's English", correlation_id: "c-1" });
    expect(r).toEqual({ text: english["error.forbidden"], known: true, correlationId: "c-1" });
  });
  it("names a code the console does not know, rather than an empty string", () => {
    const r = errorMessage(t, { code: "a_code_from_a_newer_server", message: "x" });
    expect(r.known).toBe(false);
    expect(r.text).toContain("a_code_from_a_newer_server");
  });
  it("calls an outage an outage, never a denial (M11)", () => {
    for (const code of ["internal", "unavailable", "network"]) {
      expect(errorMessage(t, { code }).text).toMatch(/outage, not a denial/);
    }
  });
  it("has a message for every code it maps", () => {
    for (const key of Object.values(ERROR_MESSAGES)) expect(english[key as MessageKey]).toBeTruthy();
  });
});

describe("the locale helpers", () => {
  it("copy an instant as ISO-8601 UTC whatever the zone", () => {
    expect(isoUTC("2026-11-07T10:00:00+02:00")).toBe("2026-11-07T08:00:00.000Z");
    const f = formatters("en", "Asia/Tokyo");
    expect(f.utc(new Date(0))).toBe("1970-01-01T00:00:00.000Z");
    expect(f.dateTime(new Date(0))).toMatch(/1970/);
  });
  it("format numbers and lists through Intl", () => {
    const f = formatters("en");
    expect(f.number(12345)).toBe("12,345");
    expect(f.list(["a", "b", "c"])).toBe("a, b, and c");
  });
});
