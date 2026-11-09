// Reads src/styles/tokens.css the way a browser resolves it, for one theme.
//
// Only what the token file uses is understood: hex colours, var() references
// and light-dark(light, dark). Anything else is a test failure rather than a
// guess, so a token written in a form this reader cannot check is noticed.

import { readFileSync } from "node:fs";
import { resolve as resolvePath } from "node:path";
import { fileURLToPath } from "node:url";

export type Theme = "light" | "dark";

// CONTRAST_TOKENS names another token file to read instead — a candidate
// direction (0052), checked before anyone is shown it. Relative to ui/.
const ui = fileURLToPath(new URL("..", import.meta.url));
const path = resolvePath(ui, process.env.CONTRAST_TOKENS || "src/styles/tokens.css");

/** The declarations of the first :root block that declares the ramps. */
export function rootTokens(): Map<string, string> {
  const css = readFileSync(path, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
  const out = new Map<string, string>();
  for (const block of css.matchAll(/(^|\})\s*:root\s*\{([^}]*)\}/g)) {
    for (const decl of (block[2] ?? "").split(";")) {
      const m = /^\s*(--[\w-]+)\s*:\s*([\s\S]+?)\s*$/.exec(decl);
      if (m && m[1] && m[2]) out.set(m[1], m[2]);
    }
  }
  return out;
}

/** The declarations a selector's block sets, such as a stripe's. */
export function blockTokens(selector: string): Map<string, string> {
  const css = readFileSync(path, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
  const out = new Map<string, string>();
  const at = css.indexOf(selector + " {");
  if (at < 0) return out;
  const body = css.slice(css.indexOf("{", at) + 1, css.indexOf("}", at));
  for (const decl of body.split(";")) {
    const m = /^\s*(--[\w-]+)\s*:\s*([\s\S]+?)\s*$/.exec(decl);
    if (m && m[1] && m[2]) out.set(m[1], m[2]);
  }
  return out;
}

/** Splits light-dark()'s two arguments at the top-level comma. */
function lightDarkArgs(inner: string): [string, string] {
  let depth = 0;
  for (let i = 0; i < inner.length; i++) {
    const c = inner[i];
    if (c === "(") depth++;
    else if (c === ")") depth--;
    else if (c === "," && depth === 0) return [inner.slice(0, i).trim(), inner.slice(i + 1).trim()];
  }
  throw new Error(`light-dark(${inner}) does not have two arguments`);
}

/** Resolves a token (or a value) to a #rrggbb colour in one theme. */
export function resolve(tokens: Map<string, string>, value: string, theme: Theme, seen: string[] = []): string {
  const v = value.trim();
  const ref = /^var\((--[\w-]+)\)$/.exec(v);
  if (ref && ref[1]) {
    if (seen.includes(ref[1])) throw new Error(`token cycle: ${[...seen, ref[1]].join(" -> ")}`);
    const next = tokens.get(ref[1]);
    if (next === undefined) throw new Error(`${ref[1]} is not declared`);
    return resolve(tokens, next, theme, [...seen, ref[1]]);
  }
  const ld = /^light-dark\(([\s\S]*)\)$/.exec(v);
  if (ld && ld[1] !== undefined) {
    const [light, dark] = lightDarkArgs(ld[1]);
    return resolve(tokens, theme === "light" ? light : dark, theme, seen);
  }
  if (/^#[0-9a-f]{6}$/i.test(v)) return v.toLowerCase();
  throw new Error(`cannot read ${JSON.stringify(v)} as a colour`);
}

function channel(hex: string, i: number): number {
  const c = parseInt(hex.slice(1 + 2 * i, 3 + 2 * i), 16) / 255;
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

/** WCAG 2.x relative luminance. */
export function luminance(hex: string): number {
  return 0.2126 * channel(hex, 0) + 0.7152 * channel(hex, 1) + 0.0722 * channel(hex, 2);
}

/** WCAG 2.x contrast ratio. */
export function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}
