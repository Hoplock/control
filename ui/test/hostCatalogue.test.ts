// A host's catalogue (0069; PLAN M27, M21; i18n/host.ts), held without a
// browser: the shell takes a host only from what the server writes on <html>,
// and only in the shape the server writes it; it requests the host's
// catalogue for its locale from inside the host's own prefix; it keeps only
// the host's own keys; and a catalogue that cannot be loaded — refused, not
// JSON, no answer, or no answer in time — is failed, never a throw, so every
// name falls back to its code and the screen names the gap. The browser
// checks hold the same against the fixture host's console.

import { afterEach, describe, expect, it, vi } from "vitest";
import {
  declaredHost,
  HOST_CATALOGUE_TIMEOUT_MS,
  hostCatalogueURL,
  hostMessages,
  hostText,
  loadHostCatalogue,
  reportNameKey,
  type DeclaredHost,
  type HostCatalogue,
  type HostFetch,
} from "../src/i18n/host";
import { unnamedBy } from "../src/screens/reportsModel";

const HOST = "hoplock/enterprise";
const CATALOGUE = `/host-console/${HOST}/locales/`;
const declared: DeclaredHost = { host: HOST, catalogue: CATALOGUE };
const page = (dataset: Record<string, string>) => ({ dataset: dataset as DOMStringMap });

describe("the page names the host", () => {
  it("as the server writes it, and nothing else", () => {
    expect(declaredHost(page({ stripe: "accent", host: HOST, hostCatalogue: CATALOGUE }))).toEqual(declared);
  });

  it.each([
    ["no host at all", {}],
    ["a host and no catalogue", { host: HOST }],
    ["a catalogue and no host", { hostCatalogue: CATALOGUE }],
    ["a host of one segment", { host: "enterprise", hostCatalogue: "/host-console/enterprise/locales/" }],
    ["a host in capitals", { host: "Hoplock/Enterprise", hostCatalogue: "/host-console/Hoplock/Enterprise/locales/" }],
    ["a catalogue under another host", { host: HOST, hostCatalogue: "/host-console/other/host/locales/" }],
    ["a catalogue inside Control's console", { host: HOST, hostCatalogue: "/console/locales/" }],
    ["a catalogue on another origin", { host: HOST, hostCatalogue: "https://cdn.example.com/host-console/hoplock/enterprise/" }],
    ["a catalogue relative to the scheme", { host: HOST, hostCatalogue: `//cdn.example.com${CATALOGUE}` }],
    ["a catalogue that climbs out", { host: HOST, hostCatalogue: `/host-console/${HOST}/../../../console/` }],
    ["a catalogue with an empty segment", { host: HOST, hostCatalogue: `/host-console/${HOST}//` }],
    ["a catalogue that is a file", { host: HOST, hostCatalogue: `${CATALOGUE}en.json` }],
  ])("is no host when the page carries %s", (_, dataset) => {
    expect(declaredHost(page(dataset))).toBeNull();
  });

  it("is no host outside a page", () => {
    expect(declaredHost(null)).toBeNull();
  });
});

describe("the host's catalogue is requested", () => {
  it("for the active locale, a pseudolocale reading English's", () => {
    expect(hostCatalogueURL(declared, "en")).toBe(`${CATALOGUE}en.json`);
    expect(hostCatalogueURL(declared, "en-XA")).toBe(`${CATALOGUE}en.json`);
    expect(hostCatalogueURL(declared, "en-XB")).toBe(`${CATALOGUE}en.json`);
  });
});

describe("only the host's own keys are kept", () => {
  it("dropping every key that is not <host>:<key>, and every value that is not a message", () => {
    const kept = hostMessages(
      HOST,
      {
        [`${HOST}:reports.example.com/pack.access-review`]: "Access review",
        "reports.title": "A key of Control's",
        "reports.example.com/pack.privileged": "No provider",
        "other/host:reports.example.com/pack.privileged": "Another host's",
        "hoplock/enterprisex:reports.a.b": "A longer provider",
        [`${HOST}:`]: "Only the provider",
        [`${HOST}:empty`]: "",
        [`${HOST}:number`]: 7,
        [`${HOST}:object`]: { en: "x" },
      },
      "en",
    );
    expect(kept).toEqual({ [`${HOST}:reports.example.com/pack.access-review`]: "Access review" });
  });

  it.each([["an array", ["x"]], ["a string", "x"], ["null", null], ["a number", 1]])("keeping nothing from %s", (_, body) => {
    expect(hostMessages(HOST, body, "en")).toEqual({});
  });
});

function answer(status: number, body: string, type = "application/json"): HostFetch {
  return async () => new Response(body, { status, headers: { "Content-Type": type } });
}

describe("loading the host's catalogue", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("requests it from this origin, with this page's cookie and nothing else", async () => {
    const calls: [string, RequestInit][] = [];
    const fetcher: HostFetch = async (input, init) => {
      calls.push([input, init]);
      return new Response(JSON.stringify({ [`${HOST}:a`]: "A" }), { status: 200 });
    };
    const got = await loadHostCatalogue(declared, "en", fetcher);
    expect(got).toEqual({ host: HOST, state: "ok", messages: { [`${HOST}:a`]: "A" } });
    expect(calls).toHaveLength(1);
    expect(calls[0]?.[0]).toBe(`${CATALOGUE}en.json`);
    expect(calls[0]?.[1].method).toBe("GET");
    expect(calls[0]?.[1].credentials).toBe("same-origin");
  });

  it.each([
    ["a 404", answer(404, '{"code":"not_found"}')],
    ["a 500", answer(500, '{"code":"internal"}')],
    ["a body that is not JSON", answer(200, "<!doctype html>", "text/html")],
    ["no answer", (async () => Promise.reject(new TypeError("Failed to fetch"))) as HostFetch],
  ])("is failed, never a throw, on %s", async (_, fetcher) => {
    await expect(loadHostCatalogue(declared, "en", fetcher)).resolves.toEqual({ host: HOST, state: "failed", messages: {} });
  });

  it("is failed when no answer comes in time", async () => {
    vi.useFakeTimers();
    const held: HostFetch = (_, init) =>
      new Promise((_, reject) => init.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError"))));
    const got = loadHostCatalogue(declared, "en", held);
    await vi.advanceTimersByTimeAsync(HOST_CATALOGUE_TIMEOUT_MS);
    await expect(got).resolves.toMatchObject({ state: "failed" });
  });

  it("rejects only when the page that asked has moved on", async () => {
    const abort = new AbortController();
    const held: HostFetch = (_, init) =>
      new Promise((_, reject) => init.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError"))));
    const got = loadHostCatalogue(declared, "en", held, abort.signal);
    abort.abort();
    await expect(got).rejects.toThrow();
  });
});

describe("a name from the host's catalogue", () => {
  const key = reportNameKey(HOST, "example.com/pack", "access-review");
  const ok: HostCatalogue = { host: HOST, state: "ok", messages: { [key]: "Access review" } };

  it("is read once the catalogue is loaded, and never before or after it failed", () => {
    expect(hostText(ok, key, "en")).toBe("Access review");
    expect(hostText({ ...ok, state: "loading" }, key, "en")).toBeUndefined();
    expect(hostText({ ...ok, state: "failed" }, key, "en")).toBeUndefined();
  });

  it("is undefined for a message that will not format, so its code stands", () => {
    expect(hostText({ ...ok, messages: { [key]: "{unclosed" } }, key, "en")).toBeUndefined();
    expect(hostText({ ...ok, messages: { [key]: "Needs {argument}" } }, key, "en")).toBeUndefined();
  });
});

describe("the Reports screen's gap (0069)", () => {
  const listed = { providers: [{ provider: "example.com/pack", reports: [{ report: "access-review", formats: ["csv" as const], parameters: [] }] }] };
  const none = { providers: [{ provider: "example.com/pack", reports: [] }] };

  it("names the host whose catalogue failed while it had a report to name", () => {
    expect(unnamedBy(listed, { host: HOST, state: "failed", messages: {} })).toBe(HOST);
  });

  it("is no gap with no host, a host loaded or loading, or nothing to name", () => {
    expect(unnamedBy(listed, null)).toBeNull();
    expect(unnamedBy(listed, { host: HOST, state: "ok", messages: {} })).toBeNull();
    expect(unnamedBy(listed, { host: HOST, state: "loading", messages: {} })).toBeNull();
    expect(unnamedBy(none, { host: HOST, state: "failed", messages: {} })).toBeNull();
  });
});
