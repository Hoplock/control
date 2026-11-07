// The pseudolocales (DESIGN.md §10.7, §10.8): every catalogue string comes out
// of en-XA accented and padded by at least 40%, with its grammar intact — the
// same arguments, plural and select branches — so it still formats; en-XB
// wraps its text in right-to-left overrides and sets the direction.

import { parse, TYPE, type MessageFormatElement } from "@formatjs/icu-messageformat-parser";
import { IntlMessageFormat } from "intl-messageformat";
import { describe, expect, it } from "vitest";
import en from "../src/locales/en.json";
import { directionOf } from "../src/i18n/locale";
import { accent, EXPANSION, pseudolocalise, textLength } from "../src/i18n/pseudo";

const catalogue = en as Record<string, string>;

function literals(elements: MessageFormatElement[]): string[] {
  return elements.flatMap((e) => {
    if (e.type === TYPE.literal) return [e.value];
    if (e.type === TYPE.tag) return literals(e.children);
    if (e.type === TYPE.plural || e.type === TYPE.select) return Object.values(e.options).flatMap((o) => literals(o.value));
    return [];
  });
}

function args(elements: MessageFormatElement[], out = new Map<string, TYPE>()): Map<string, TYPE> {
  for (const e of elements) {
    if (e.type === TYPE.literal || e.type === TYPE.pound) continue;
    if (e.type === TYPE.tag) args(e.children, out);
    else {
      out.set(e.value, e.type);
      if (e.type === TYPE.plural || e.type === TYPE.select) for (const o of Object.values(e.options)) args(o.value, out);
    }
  }
  return out;
}

function sample(elements: MessageFormatElement[]): Record<string, string | number | Date> {
  const out: Record<string, string | number | Date> = {};
  for (const [name, type] of args(elements)) {
    out[name] = type === TYPE.plural || type === TYPE.number ? 3 : type === TYPE.date || type === TYPE.time ? new Date(0) : "x";
  }
  return out;
}

describe.each(Object.keys(catalogue))("%s", (key) => {
  const source = catalogue[key] as string;
  const parsed = parse(source);

  it("comes out of en-XA accented and padded by 40%", () => {
    const xa = pseudolocalise(source, "en-XA");
    const text = literals(xa).join("");
    const original = literals(parsed).join("");
    if (/[aceinopuyACDEINOPUY]/.test(original)) {
      expect(text, "every letter that has an accent took it").not.toMatch(/[aceinopuyACDEINOPUY]/);
    }
    expect(text.startsWith("[")).toBe(true);
    expect(text.endsWith("]")).toBe(true);
    const padding = text.length - accent(original).length - 2;
    expect(padding).toBeGreaterThanOrEqual(Math.ceil(textLength(parsed) * EXPANSION));
  });

  it("keeps its arguments and still formats", () => {
    const xa = pseudolocalise(source, "en-XA");
    expect([...args(xa).entries()]).toEqual([...args(parsed).entries()]);
    expect(() => new IntlMessageFormat(xa, "en").format(sample(parsed))).not.toThrow();
  });

  it("comes out of en-XB inside right-to-left overrides", () => {
    for (const l of literals(pseudolocalise(source, "en-XB"))) {
      if (l === "") continue;
      expect(l.startsWith("‮") && l.endsWith("‬"), JSON.stringify(l)).toBe(true);
    }
  });
});

describe("the pseudolocales' direction", () => {
  it("is right to left for en-XB and left to right for en-XA and en", () => {
    expect(directionOf("en-XB")).toBe("rtl");
    expect(directionOf("en-XA")).toBe("ltr");
    expect(directionOf("en")).toBe("ltr");
    expect(directionOf("ar")).toBe("rtl");
  });
});
