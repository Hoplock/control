import { describe, expect, it } from "vitest";
import { consoleHref, hostConsoleHref, insideConsole, safeReturn } from "../src/router/paths";
import { match } from "../src/router/router";

describe("every URL the console writes is under /console/ (M27)", () => {
  it("writes deep links and filters inside the prefix", () => {
    expect(consoleHref("audit/records", { subject: "alice" })).toBe("/console/audit/records?subject=alice");
    expect(consoleHref("/deployment")).toBe("/console/deployment");
    expect(consoleHref("")).toBe("/console/");
  });
  it("refuses a path that climbs out", () => {
    expect(() => consoleHref("../api/v1/session")).toThrow();
    expect(() => consoleHref("a/./b")).toThrow();
  });
  it("names a host's prefix for a host's screens, never its own", () => {
    expect(hostConsoleHref("hoplock/enterprise", "approvals")).toBe("/host-console/hoplock/enterprise/approvals");
    expect(() => hostConsoleHref("Hoplock", "x")).toThrow();
  });
  it("reads only locations inside the prefix", () => {
    expect(insideConsole("/console/deployment")).toBe("deployment");
    expect(insideConsole("/console")).toBe("");
    expect(insideConsole("/api/v1/session")).toBeNull();
    expect(insideConsole("/consoles/x")).toBeNull();
  });
  it("returns a sign-in only to a console URL: no open redirect", () => {
    expect(safeReturn("/console/audit?x=1")).toBe("/console/audit?x=1");
    for (const bad of ["https://evil.example/", "//evil.example/console/", "/api/v1/session", "/console\\..\\x", null, ""]) {
      expect(safeReturn(bad)).toBe("/console/");
    }
  });
  it("matches a route's params", () => {
    expect(match("sign-in/callback/:connector", "sign-in/callback/corp")).toEqual({ connector: "corp" });
    expect(match("deployment", "deployment/x")).toBeNull();
  });
});
