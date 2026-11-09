// The decisions the status vocabulary encodes (DESIGN.md §6), held without a
// browser: a deny and an outage are different tones, glyphs, colours and
// words (M11); a rolling upgrade is in progress, never a fault (M19); stale,
// undated and absent are Not reported, never healthy (M17). And an error
// whose code the catalogue does not know still says something true.

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { ConfigRollout, Proxy } from "../src/api/types";
import { ErrorMessage, ErrorState } from "../src/components/States";
import { STATUS, type Tone } from "../src/components/Status";
import { I18nProvider } from "../src/i18n/I18n";
import { HEALTH_STATUS } from "../src/shell/chrome";
import { configStatus, proxyHealth } from "../src/screens/fleetModel";
import en from "../src/locales/en.json";
import { resolve, rootTokens } from "./tokens";

const TONE_TOKEN: Record<Tone, string> = {
  allow: "--s-allow",
  deny: "--s-deny",
  warn: "--s-warn",
  outage: "--s-outage",
  info: "--s-info",
  unknown: "--s-unknown",
};

const render = (node: React.ReactNode) => renderToStaticMarkup(<I18nProvider locale="en">{node}</I18nProvider>);

describe("M11: a deny and an outage are never drawn alike", () => {
  const deny = [STATUS.denied, STATUS.revoked, STATUS.expired];
  const outage = [STATUS.unreachable, STATUS.fetchFailed, STATUS.unserved, HEALTH_STATUS.unreachable];
  it("have different tones and different words", () => {
    for (const d of deny) {
      for (const o of outage) {
        expect(d.tone).not.toBe(o.tone);
        expect(en[d.label]).not.toBe(en[o.label]);
      }
    }
  });
  it.each(["light", "dark"] as const)("have different colours in the %s theme", (theme) => {
    const tokens = rootTokens();
    expect(resolve(tokens, `var(${TONE_TOKEN.deny})`, theme)).not.toBe(resolve(tokens, `var(${TONE_TOKEN.outage})`, theme));
  });
  it("say outage for an outage and refused for a refusal, never denied for either", () => {
    const outageHtml = render(<ErrorState error={{ code: "internal", correlation_id: "c-1" }} kind="outage" />);
    const refusedHtml = render(<ErrorState error={{ code: "forbidden" }} kind="refused" />);
    expect(outageHtml).toContain('data-tone="outage"');
    expect(outageHtml).toContain(">Outage<");
    expect(outageHtml).not.toMatch(/denied|refused/i);
    expect(refusedHtml).toContain('data-tone="deny"');
    expect(refusedHtml).toContain(">Refused<");
  });
});

describe("M19: a rolling upgrade is in progress, never a fault", () => {
  it("reads as info", () => {
    expect(HEALTH_STATUS.in_progress.tone).toBe("info");
    const rollout: ConfigRollout = { proxy_id: "p", desired_version: "v2", running_version: "v1", state: "applied", caught_up: false, announced: true };
    expect(STATUS[configStatus(rollout)].tone).toBe("info");
  });
});

describe("M17: absent is Not reported, never healthy or applied", () => {
  it("a proxy that never reported its configuration is Not reported, even with a document desired", () => {
    const never: ConfigRollout = { proxy_id: "p", desired_version: "v1", caught_up: false, announced: true };
    expect(configStatus(never)).toBe("notReported");
    expect(STATUS.notReported.tone).toBe("unknown");
    expect(HEALTH_STATUS.unknown.tone).toBe("unknown");
  });
  it("a proxy never heard from is not healthy", () => {
    const base: Proxy = {
      proxy_id: "p", zone: "z", state: "enrolled", live: false, contract_version: 0, capabilities: {},
      relay_registrations: [], session_count: 0, config: { proxy_id: "p", caught_up: false, announced: false },
    };
    expect(proxyHealth(base)).toBe("unreachable");
    expect(proxyHealth({ ...base, last_heartbeat_at: "2026-01-01T00:00:00Z" })).toBe("stale");
    expect(proxyHealth({ ...base, live: true })).toBe("healthy");
    expect(proxyHealth({ ...base, live: true, state: "revoked" })).toBe("revoked");
  });
});

describe("an error the catalogue has no sentence for", () => {
  it("names its code and its correlation id rather than rendering nothing", () => {
    const html = render(<ErrorMessage error={{ code: "quota_exhausted", correlation_id: "corr-9" }} kind="outage" />);
    expect(html).toContain("quota_exhausted");
    expect(html).toContain("corr-9");
  });
  it("survives an envelope with no correlation id and no parameters", () => {
    expect(render(<ErrorState error={{ code: "something_new" }} kind="refused" />)).toContain("something_new");
  });
});
