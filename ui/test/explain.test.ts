// What Explain decides from a story (0051; PLAN M4, M11, M28): held without a
// browser, against the shapes `GET …/decisions/{id}` answers.

import { describe, expect, it } from "vitest";
import type { Story } from "../src/api/types";
import { STATUS } from "../src/components/Status";
import {
  basis,
  effectStatus,
  hopPath,
  inHopOrder,
  lookupOf,
  openingDecision,
  sessionOutcome,
} from "../src/screens/explainModel";

function story(over: Partial<Story> & { proxy: string; trail?: string[]; at?: string }): Story {
  const { proxy, trail, at, ...rest } = over;
  return {
    decision_id: `d_${proxy}`,
    session_id: "sess-1",
    proxy_id: proxy,
    effect: "allow",
    decided_at: at ?? "2026-11-01T08:00:00Z",
    rule: "ops",
    rule_line: 3,
    mapping: { version: 0, active: 0, changed: false },
    inputs: {
      now: at ?? "2026-11-01T08:00:00Z",
      subject: { id: "grace", mfa: true, known: true, break_glass: false },
      target: { hostname: "db-01.internal", known: true },
      context: { proxy_id: proxy, hop_trail: trail },
    },
    explanation: { effect: rest.effect ?? "allow" },
    ...rest,
  };
}

describe("an effect in the status vocabulary (M11)", () => {
  it("draws a deny and an unserved allow apart", () => {
    expect(effectStatus("deny")).toBe("denied");
    expect(effectStatus("unserved")).toBe("unserved");
    expect(STATUS[effectStatus("deny")].tone).toBe("deny");
    expect(STATUS[effectStatus("unserved")].tone).toBe("outage");
    expect(effectStatus("allow")).toBe("allowed");
  });
  it("never reads an effect it does not know as allowed or denied", () => {
    expect(effectStatus("maybe")).toBe("notReported");
  });
});

describe("why a decision came out as it did", () => {
  it("is the rule when one answered", () => {
    expect(basis(story({ proxy: "p" }))).toBe("rule");
  });
  it("is the default deny when none did", () => {
    expect(basis(story({ proxy: "p", effect: "deny", rule: undefined }))).toBe("default");
  });
  it("is a standing denial when one decided above the engine (M28)", () => {
    const s = story({ proxy: "p", effect: "deny", rule: undefined });
    s.explanation.standing = ["subject_locked"];
    expect(basis(s)).toBe("standing");
  });
  it("is an outage when an allow went unserved", () => {
    expect(basis(story({ proxy: "p", effect: "unserved", unserved: "no route" }))).toBe("unserved");
  });
});

describe("a session's hops", () => {
  const edge = story({ proxy: "px-core-01", at: "2026-11-01T08:00:01Z" });
  edge.explanation.next_proxy_id = "px-dmz-03";
  edge.explanation.route_type = "nexthop";
  const inner = story({ proxy: "px-dmz-03", trail: ["px-core-01"], at: "2026-11-01T08:00:00Z", effect: "deny", rule: undefined });

  it("are read edge first, whatever order the API answers in", () => {
    expect(inHopOrder([inner, edge]).map((s) => s.proxy_id)).toEqual(["px-core-01", "px-dmz-03"]);
  });
  it("come to a deny when any hop denied", () => {
    expect(sessionOutcome([edge, inner])).toBe("denied");
    expect(sessionOutcome([edge])).toBe("allowed");
    expect(sessionOutcome([edge, story({ proxy: "x", effect: "unserved" })])).toBe("unserved");
  });
  it("open on the decision that denied", () => {
    expect(openingDecision([edge, inner])).toBe("d_px-dmz-03");
    expect(openingDecision([edge])).toBe("d_px-core-01");
  });
  it("draw a path from the edge to the target, each proxy with its decision", () => {
    const path = hopPath([inner, edge]);
    expect(path.map((h) => `${h.kind}:${h.id}`)).toEqual(["proxy:px-core-01", "proxy:px-dmz-03", "target:db-01.internal"]);
    expect(path[0]?.story?.decision_id).toBe("d_px-core-01");
    expect(path[1]?.story?.effect).toBe("deny");
  });
  it("name the proxy a lone edge decision routed to next, which took no decision here", () => {
    const path = hopPath([edge]);
    expect(path.map((h) => `${h.kind}:${h.id}:${h.next ? "next" : ""}`)).toEqual([
      "proxy:px-core-01:",
      "proxy:px-dmz-03:next",
      "target:db-01.internal:",
    ]);
  });
  it("keep the proxies a hop trail names even when this server holds no decision of theirs", () => {
    const path = hopPath([inner]);
    expect(path.map((h) => h.id)).toEqual(["px-core-01", "px-dmz-03", "db-01.internal"]);
    expect(path[0]?.story).toBeUndefined();
  });
});

it("looks an id up as a decision only when it is one this server mints", () => {
  expect(lookupOf("d_2c9q5k")).toBe("decisions");
  expect(lookupOf("sess-7f3a")).toBe("sessions");
  expect(lookupOf("4f1d2c")).toBe("sessions");
});
