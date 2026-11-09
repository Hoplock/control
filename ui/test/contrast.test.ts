// DESIGN.md §10.2: every semantic foreground/background pair asserted against
// the threshold that applies to it, in both themes, over the token file
// itself. A token change that breaks contrast fails here, before review.

import { describe, expect, it } from "vitest";
import { blockTokens, contrast, resolve, rootTokens, type Theme } from "./tokens";

const TEXT = 4.5; // WCAG 1.4.3: text.
const NON_TEXT = 3; // WCAG 1.4.11: a control's boundary, a focus ring, a glyph.

const TEXT_GROUNDS = [
  "--bg-canvas",
  "--bg-surface",
  "--bg-raised",
  "--bg-sunken",
  "--bg-hover",
  "--bg-selected",
  "--diff-add-bg",
  "--diff-remove-bg",
];
const CONTROL_GROUNDS = ["--bg-canvas", "--bg-surface", "--bg-raised"];
const STATUS = ["--s-allow", "--s-deny", "--s-warn", "--s-outage", "--s-info"];
const STRIPES = ["accent", "magenta", "teal", "orange", "slate"];

interface Pair {
  fg: string;
  bg: string;
  min: number;
  why: string;
}

function pairs(): Pair[] {
  const out: Pair[] = [];
  for (const bg of TEXT_GROUNDS) {
    out.push({ fg: "--text-primary", bg, min: TEXT, why: "body text" });
    out.push({ fg: "--text-secondary", bg, min: TEXT, why: "labels and metadata" });
  }
  // A placeholder is text (1.4.3); it sits in an input, on a surface.
  for (const bg of ["--bg-surface", "--bg-raised"]) {
    out.push({ fg: "--text-tertiary", bg, min: TEXT, why: "placeholder" });
  }
  out.push({ fg: "--text-on-accent", bg: "--accent", min: TEXT, why: "text on the filled accent" });
  out.push({ fg: "--text-on-accent", bg: "--a-600", min: TEXT, why: "a primary button, hovered" });
  out.push({ fg: "--n-0", bg: "--a-900", min: TEXT, why: "a primary button, pressed" });
  out.push({ fg: "--text-on-accent", bg: "--s-deny", min: TEXT, why: "a danger button, hovered" });
  out.push({ fg: "--text-inverse", bg: "--bg-inverse", min: TEXT, why: "a tooltip" });
  out.push({ fg: "--a-600", bg: "--bg-sunken", min: TEXT, why: "a JSON viewer's numbers and literals" });
  for (const bg of CONTROL_GROUNDS) {
    out.push({ fg: "--border-default", bg, min: NON_TEXT, why: "an input's boundary" });
    out.push({ fg: "--border-strong", bg, min: NON_TEXT, why: "emphasis boundary" });
    out.push({ fg: "--focus-ring", bg, min: NON_TEXT, why: "the focus ring" });
    out.push({ fg: "--accent", bg, min: NON_TEXT, why: "the active navigation mark" });
    out.push({ fg: "--s-unknown", bg, min: NON_TEXT, why: "the Not reported glyph" });
    for (const s of STATUS) {
      // A status label may be set in its hue, so it is held to text.
      out.push({ fg: s, bg, min: TEXT, why: "a status glyph and label" });
    }
  }
  for (const s of STRIPES) {
    out.push({ fg: `--stripe-${s}`, bg: "--bg-canvas", min: NON_TEXT, why: `the ${s} stripe` });
    out.push({ fg: "--text-primary", bg: `--stripe-${s}-tint`, min: TEXT, why: `the deployment name on the ${s} chip` });
    out.push({ fg: "--text-secondary", bg: `--stripe-${s}-tint`, min: TEXT, why: `the instance id on the ${s} chip` });
  }
  return out;
}

// --border-subtle is decorative (DESIGN.md §3.2): a table rule identifies no
// control, so WCAG 1.4.11 does not apply and it is not asserted. Listed so
// that dropping it from the exemption is a decision, not an accident.
const DECORATIVE = ["--border-subtle"];

describe.each<Theme>(["light", "dark"])("the %s theme", (theme) => {
  const tokens = rootTokens();
  it.each(pairs().map((p) => [`${p.fg} on ${p.bg} (${p.why})`, p] as const))("%s", (_, p) => {
    const ratio = contrast(resolve(tokens, `var(${p.fg})`, theme), resolve(tokens, `var(${p.bg})`, theme));
    expect(ratio, `${p.fg} on ${p.bg} is ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(p.min);
  });

  it("resolves every colour token, so none is in a form this test cannot read", () => {
    for (const [name, value] of tokens) {
      if (!/^--(n|a|s|stripe|bg|text|border|accent|focus)(-|$)/.test(name) || /-(width|height|offset)$/.test(name)) continue;
      expect(() => resolve(tokens, value, theme), name).not.toThrow();
    }
  });
});

describe("the token set", () => {
  const tokens = rootTokens();
  it("declares the decorative exemption's tokens, and nothing asserts them", () => {
    for (const d of DECORATIVE) {
      expect(tokens.has(d), d).toBe(true);
      expect(pairs().some((p) => p.fg === d)).toBe(false);
    }
  });
  it("gives every configurable stripe a block that selects it", () => {
    for (const s of STRIPES.filter((s) => s !== "accent")) {
      const block = blockTokens(`:root[data-stripe="${s}"]`);
      expect(block.get("--stripe"), s).toBe(`var(--stripe-${s})`);
      expect(block.get("--stripe-tint"), s).toBe(`var(--stripe-${s}-tint)`);
    }
  });
  it("keeps the deny and the outage different colours in both themes (M11)", () => {
    for (const theme of ["light", "dark"] as const) {
      expect(resolve(tokens, "var(--s-deny)", theme)).not.toBe(resolve(tokens, "var(--s-outage)", theme));
    }
  });
});

export const STRIPE_NAMES = STRIPES;
