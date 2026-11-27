// In a pseudolocale build the host's catalogue is pseudolocalised as
// Control's is (0069; DESIGN.md §10.7, §10.8), so a host's name that came out
// unaccented would fail the browser check as a hardcoded string would. The
// build's forced locale is a constant the bundler folds, so it is mocked here.

import { describe, expect, it, vi } from "vitest";

vi.mock("../src/i18n/catalogue", async (actual) => ({
  ...(await actual<typeof import("../src/i18n/catalogue")>()),
  FORCED_LOCALE: "en-XA",
}));

const { hostMessages, hostText } = await import("../src/i18n/host");

const HOST = "hoplock/enterprise";
const KEY = `${HOST}:reports.example.com/pack.access-review`;

describe("a host's catalogue under en-XA", () => {
  it("is accented, bracketed and padded, and still formats", () => {
    const messages = hostMessages(HOST, { [KEY]: "Access review", [`${HOST}:broken`]: "{unclosed" }, "en-XA");
    expect(Object.keys(messages)).toEqual([KEY]);
    const text = hostText({ host: HOST, state: "ok", messages }, KEY, "en-XA") ?? "";
    expect(text.startsWith("[") && text.endsWith("]")).toBe(true);
    expect(text).not.toContain("Access review");
    expect(/[À-ÿ]/.test(text)).toBe(true);
    expect(text.length).toBeGreaterThanOrEqual(Math.ceil("Access review".length * 1.4));
  });

  it("is English's, unrewritten, for any other locale", () => {
    expect(hostMessages(HOST, { [KEY]: "Access review" }, "en")).toEqual({ [KEY]: "Access review" });
  });
});
