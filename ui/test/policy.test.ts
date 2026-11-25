// POLICY'S CODES AND RULES (0053; PLAN M21, §5.6). A rejection and a finding
// are rendered from a code and its parameters, never from the English beside
// them, so
//   1. every code the server can answer has a sentence in the catalogue, and
//      each sentence names only parameters that code carries — the list is
//      ./policy-codes.json, which a Go test keeps equal to the server's
//      (internal/authoring/console_codes_test.go);
//      A parameter drawn from a closed set is said by a select with an option
//      for every value the server names; an unreplayable record's cause and
//      a refused simulation's problem are held the same way (0065);
//   2. a code the catalogue does not know renders the named fallback;
//   3. the screens' rules — versions, severities, the activation gate, the
//      lists a finding names — hold;
//   4. the diff view reads a unified diff as the server writes it.

import { parse, TYPE, type MessageFormatElement } from "@formatjs/icu-messageformat-parser";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Finding, Rejection } from "../src/api/types";
import { diffCounts, parseUnified } from "../src/components/Diff";
import { I18nProvider } from "../src/i18n/I18n";
import { translator } from "../src/i18n/messages";
import en from "../src/locales/en.json";
import {
  FINDING,
  findingText,
  REJECTION,
  rejectionText,
  SIMULATION_PROBLEM,
  simulationRefusalText,
  UNREPLAYABLE,
  unreplayableText,
  WORDED,
} from "../src/screens/policyCodes";
import { sourceNotes } from "../src/screens/PolicyParts";
import {
  findingLists,
  findingValues,
  inReportOrder,
  isRollback,
  mergeFindings,
  readVersion,
  replayWindow,
  severityStatus,
  unaccepted,
  versionName,
  versionStatus,
} from "../src/screens/policyModel";
import codes from "./policy-codes.json";

const t = translator("en", en);

/** Every argument a message names, however deep inside a plural or select. */
function argumentsOf(elements: MessageFormatElement[], out = new Set<string>()): Set<string> {
  for (const e of elements) {
    if (e.type === TYPE.argument || e.type === TYPE.number || e.type === TYPE.date || e.type === TYPE.time) out.add(e.value);
    if (e.type === TYPE.plural || e.type === TYPE.select) {
      out.add(e.value);
      for (const o of Object.values(e.options)) argumentsOf(o.value, out);
    }
    if (e.type === TYPE.tag) argumentsOf(e.children, out);
  }
  return out;
}

const message = (key: string) => (en as Record<string, string>)[key] as string;

describe("the compiler's rejections", () => {
  const server = codes.rejections as Record<string, string[]>;

  it("have a sentence for every code the server can answer, and none for a code it cannot", () => {
    expect(Object.keys(REJECTION).sort()).toEqual(Object.keys(server).sort());
  });

  it.each(Object.entries(server))("%s's sentence names only its parameters", (code, params) => {
    const key = REJECTION[code] as string;
    expect(message(key), key).toBeDefined();
    const used = argumentsOf(parse(message(key)));
    expect([...used].filter((a) => !params.includes(a)), `${key} names a parameter ${code} does not carry`).toEqual([]);
  });

  it("render from the code and parameters, never the message", () => {
    const r: Rejection = {
      code: "match.unknown_group",
      rule: "ops-db-shell",
      line: 12,
      parameters: { group: "sre-typo" },
      message: "ENGLISH THE CONSOLE NEVER SHOWS",
    };
    const out = rejectionText(t, r);
    expect(out.known).toBe(true);
    expect(out.text).toContain("sre-typo");
    expect(out.text).not.toContain("ENGLISH");
  });

  it("render a code the catalogue does not know as the named fallback", () => {
    const out = rejectionText(t, { code: "route.from_a_newer_server", message: "x" });
    expect(out.known).toBe(false);
    expect(out.text).toContain("route.from_a_newer_server");
  });

  it("fall back, naming the code, when a parameter the sentence needs did not arrive", () => {
    const out = rejectionText(t, { code: "match.unknown_group", message: "x" });
    expect(out.known).toBe(false);
    expect(out.text).toContain("match.unknown_group");
  });
});

/** The options of every select over this argument, however deep. */
function selectOptions(elements: MessageFormatElement[], name: string, out: string[][] = []): string[][] {
  for (const e of elements) {
    if (e.type === TYPE.select && e.value === name) out.push(Object.keys(e.options));
    if (e.type === TYPE.plural || e.type === TYPE.select) for (const o of Object.values(e.options)) selectOptions(o.value, name, out);
    if (e.type === TYPE.tag) selectOptions(e.children, name, out);
  }
  return out;
}

describe("a parameter drawn from a closed set", () => {
  const server = codes.enumerations as Record<string, Record<string, string[]>>;

  it("is worded for exactly the codes and parameters the server names", () => {
    const named = Object.fromEntries(Object.entries(server).map(([c, ps]) => [c, Object.keys(ps).sort()]));
    const worded = Object.fromEntries(Object.entries(WORDED).map(([c, ps]) => [c, [...ps].sort()]));
    expect(worded).toEqual(named);
  });

  it.each(Object.entries(server).flatMap(([c, ps]) => Object.entries(ps).map(([p, vs]) => [c, p, vs] as const)))(
    "%s says each %s in words, never interpolating it",
    (code, param, values) => {
      const parsed = parse(message(REJECTION[code] as string));
      const selects = selectOptions(parsed, param);
      expect(selects.length, `${code} has no select over ${param}`).toBeGreaterThan(0);
      for (const options of selects) {
        for (const v of values) expect(options, `${code}'s ${param} has no option for ${v}`).toContain(v);
        expect(options).toContain("other");
      }
      // Never a plain argument as well: the value is an identifier, not a word.
      const plain = parsed.filter((e) => e.type === TYPE.argument && e.value === param);
      expect(plain).toEqual([]);
    },
  );

  it("renders as words, not as the identifier", () => {
    const r: Rejection = { code: "policy.document_malformed", line: 4, parameters: { problem: "tab_indentation" }, message: "x" };
    const out = rejectionText(t, r);
    expect(out.known).toBe(true);
    expect(out.text).toContain("spaces only");
    expect(out.text).not.toContain("tab_indentation");
    const shape = rejectionText(t, { code: "policy.value_wrong_type", parameters: { found: "string", expected: "list" }, message: "x" });
    expect(shape.text).toContain("a string");
    expect(shape.text).toContain("a list");
  });
});

describe("an unreplayable record's cause", () => {
  const server = codes.unreplayable as Record<string, string[]>;

  it("has a sentence for every cause the server can name, and none for one it cannot", () => {
    expect(Object.keys(UNREPLAYABLE).sort()).toEqual(Object.keys(server).sort());
  });

  it.each(Object.entries(server))("%s's sentence names only its parameters", (code, params) => {
    const key = UNREPLAYABLE[code] as string;
    expect(message(key), key).toBeDefined();
    const used = argumentsOf(parse(message(key)));
    expect([...used].filter((a) => !params.includes(a))).toEqual([]);
  });

  it("renders from the cause and parameters, never the reason", () => {
    const out = unreplayableText(t, {
      decision_id: "dec-1",
      code: "unreplayable.source_address_invalid",
      parameters: { address: "office" },
      reason: "ENGLISH THE CONSOLE NEVER SHOWS",
    });
    expect(out.known).toBe(true);
    expect(out.text).toContain("office");
    expect(out.text).not.toContain("ENGLISH");
  });

  it("renders a cause the console does not know, or none, as the named fallback", () => {
    const newer = unreplayableText(t, { decision_id: "d", code: "unreplayable.from_a_newer_server", reason: "x" });
    expect(newer.known).toBe(false);
    expect(newer.text).toContain("unreplayable.from_a_newer_server");
    expect(unreplayableText(t, { decision_id: "d", reason: "ENGLISH" }).text).not.toContain("ENGLISH");
  });
});

describe("a refused simulation", () => {
  it("has a sentence for every problem the server can name, each naming only the field", () => {
    expect(Object.keys(SIMULATION_PROBLEM).sort()).toEqual([...codes.simulation_problems].sort());
    for (const key of Object.values(SIMULATION_PROBLEM)) {
      expect(message(key), key).toBeDefined();
      expect([...argumentsOf(parse(message(key)))].filter((a) => a !== "field")).toEqual([]);
    }
  });

  it("says its field and problem, never the reason", () => {
    const text = simulationRefusalText(t, {
      code: "invalid_request",
      parameters: { field: "replay.to", problem: "not_after_start", reason: "ENGLISH" },
    });
    expect(text).toContain("replay.to");
    expect(text).not.toContain("ENGLISH");
  });

  it("leaves another refusal, or a problem it does not know, to the error's own sentence", () => {
    expect(simulationRefusalText(t, { code: "policy_invalid" })).toBeNull();
    expect(simulationRefusalText(t, { code: "invalid_request", parameters: { field: "x", problem: "from_a_newer_server" } })).toBeNull();
  });
});

describe("the publish-time findings", () => {
  it("have a sentence for every one of Control's codes", () => {
    expect(Object.keys(FINDING).sort()).toEqual([...codes.findings].sort());
    for (const key of Object.values(FINDING)) expect(message(key), key).toBeDefined();
  });

  const finding = (over: Partial<Finding>): Finding => ({
    id: "x",
    severity: "warning",
    code: "satisfiability.target_cannot_take",
    source: "control",
    message: "ENGLISH",
    ...over,
  });

  it("count a list from its whole number, not from the 100 it names (0035)", () => {
    const f = finding({
      parameters: {
        rule: "r",
        targets: Array.from({ length: 100 }, (_, i) => ({ hostname: `h${i}`, missing: ["account-restricted"] })),
        targets_count: 250,
        targets_truncated: true,
      },
    });
    expect(findingText(t, f).text).toContain("250 targets");
    const [list] = findingLists(f);
    expect(list).toMatchObject({ name: "targets", count: 250, truncated: true });
    expect(list?.items[0]).toEqual({ name: "h0", missing: ["account-restricted"], offered: [] });
  });

  it("say a rule-level finding that names no builds", () => {
    const f = finding({ severity: "advice", code: "algorithm.declaration_unknown", parameters: { rule: "r", profile: "default" } });
    expect(findingValues(f).proxies_count).toBe(0);
    expect(findingText(t, f).text).toMatch(/No build has declared/);
  });

  it("read a nested record's fields (algorithm.policy_unmet)", () => {
    const f = finding({
      code: "algorithm.policy_unmet",
      parameters: { rule: "r", unmet: { target: "db-01", axis: "kex", cause: "floor", record_id: "rec" } },
    });
    expect(findingText(t, f)).toMatchObject({ known: true });
    expect(findingText(t, f).text).toContain("db-01");
  });

  it("render a validator's code by the fallback, naming its provider", () => {
    const f = finding({ code: "acme.second_author", source: "acme/governance" });
    const out = findingText(t, f);
    expect(out.known).toBe(false);
    expect(out.text).toContain("acme/governance");
    expect(out.text).toContain("acme.second_author");
  });

  // The server places a validator's finding at the line of the rule its Path
  // names (0067); the source view sets every finding that carries a line, and
  // only those. This holds what already worked, so that it keeps working.
  it("set a validator's finding beneath the line the report gives it, and one with no line nowhere", () => {
    const placed = finding({
      id: "acme.reviewed/acme/governance/sre",
      code: "acme.reviewed",
      source: "acme/governance",
      rule: "sre",
      line: 7,
    });
    const unplaced = finding({
      id: "acme.pointer/acme/governance//rules/0",
      code: "acme.pointer",
      source: "acme/governance",
      rule: "/rules/0",
    });
    const notes = sourceNotes([], [placed, unplaced]);
    expect(notes).toHaveLength(1);
    const [note] = notes;
    expect(note).toMatchObject({ line: 7, key: `f:${placed.id}`, tone: "warn" });
    const html = renderToStaticMarkup(createElement(I18nProvider, { locale: "en", children: note?.content }));
    expect(html).toContain(`data-finding-line="${placed.id}"`);
    // Its text is the fallback that names its provider and its code.
    const text = /<p(?:\s[^>]*)?>([\s\S]*?)<\/p>/.exec(html)?.[1] ?? "";
    const fallback = findingText(t, placed);
    expect(fallback.known).toBe(false);
    expect(text).toBe(
      fallback.text
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll('"', "&quot;")
        .replaceAll("'", "&#x27;"),
    );
    expect(text).toContain("acme/governance");
    expect(text).toContain("acme.reviewed");
    expect(html).not.toContain("acme.pointer");
  });
});

describe("policy's rules", () => {
  it("names a version as the identifier an author types", () => {
    expect(versionName(12)).toBe("v12");
    expect(readVersion("12")).toBe(12);
    for (const bad of ["0", "-1", "1.5", "v2", "", undefined]) expect(readVersion(bad)).toBeNull();
  });

  it("reads one version as active and every other as superseded (§5.6)", () => {
    expect(versionStatus({ active: true })).toBe("active");
    expect(versionStatus({ active: false })).toBe("superseded");
  });

  it("calls activating an older version a rollback, and nothing else", () => {
    expect(isRollback(1, 2)).toBe(true);
    expect(isRollback(3, 2)).toBe(false);
    expect(isRollback(1, null)).toBe(false);
  });

  it("reads each severity in the status vocabulary, and an unknown one as not reported", () => {
    expect(severityStatus("blocking")).toBe("blocking");
    expect(severityStatus("warning")).toBe("warning");
    expect(severityStatus("advice")).toBe("advice");
    expect(severityStatus("catastrophic")).toBe("notReported");
  });

  const f = (id: string, severity: string): Finding => ({ id, severity, code: "c", source: "control", message: "" });

  it("orders a report by severity, then id", () => {
    expect(inReportOrder([f("b", "advice"), f("z", "warning"), f("a", "warning"), f("m", "blocking")]).map((x) => x.id)).toEqual(["m", "a", "z", "b"]);
  });

  it("gates an activation on each warning not accepted by its id, and on nothing else", () => {
    const report = [f("w1", "warning"), f("w2", "warning"), f("a1", "advice"), f("b1", "blocking")];
    expect(unaccepted(report, new Set(["w1"])).map((x) => x.id)).toEqual(["w2"]);
    expect(unaccepted(report, new Set(["w1", "w2"]))).toEqual([]);
  });

  it("joins what a 409 names to what was shown, once each", () => {
    expect(mergeFindings([f("w1", "warning")], [f("w1", "warning"), f("w3", "warning")]).map((x) => x.id)).toEqual(["w1", "w3"]);
  });

  it("asks replay for both ends of a window, all time included", () => {
    expect(replayWindow({ from: "2026-01-01T00:00:00Z" }, "2026-02-01T00:00:00Z")).toEqual({ from: "2026-01-01T00:00:00Z", to: "2026-02-01T00:00:00Z" });
    expect(replayWindow({}, "2026-02-01T00:00:00Z").from).toBe("1970-01-01T00:00:00Z");
  });
});

describe("the diff view", () => {
  it("numbers each line from its hunk, on both sides", () => {
    const lines = parseUnified("@@ -3,3 +3,4 @@\n a\n-b\n+B\n+B2\n c\n");
    expect(lines).toEqual([
      { kind: "hunk", text: "@@ -3,3 +3,4 @@" },
      { kind: "context", old: 3, new: 3, text: "a" },
      { kind: "remove", old: 4, text: "b" },
      { kind: "add", new: 4, text: "B" },
      { kind: "add", new: 5, text: "B2" },
      { kind: "context", old: 5, new: 6, text: "c" },
    ]);
    expect(diffCounts(lines)).toEqual({ added: 2, removed: 1 });
  });

  it("reads a whole bundle added against nothing, and no diff as no lines", () => {
    expect(parseUnified("@@ -0,0 +1,2 @@\n+a\n+b\n").filter((l) => l.kind === "add").map((l) => l.new)).toEqual([1, 2]);
    expect(parseUnified("")).toEqual([]);
  });

  it("keeps a line that is itself blank or starts with a marker's character", () => {
    const lines = parseUnified("@@ -1,2 +1,2 @@\n \n--- not a header\n++x\n");
    expect(lines.slice(1)).toEqual([
      { kind: "context", old: 1, new: 1, text: "" },
      { kind: "remove", old: 2, text: "-- not a header" },
      { kind: "add", new: 2, text: "+x" },
    ]);
  });
});
