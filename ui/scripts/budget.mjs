#!/usr/bin/env node
// THE BUNDLE BUDGET (ui/DESIGN.md §11): at most 350 KB of gzipped JavaScript
// on first load for the active locale, and at most 120 KB of fonts. The
// binary is shipped to self-hosters who may serve it over a VPN from a small
// instance.
//
// First load is what index.html loads: its module scripts, and every module
// they import statically (the modulepreload graph), followed through the
// build's own manifest-free output by reading each chunk's imports. Fonts are
// every font file the build emits, since each is in the binary whether or not
// a page loads it.
//
// node scripts/budget.mjs [dist]   (default: ../internal/console/dist)

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

export const JS_BUDGET = 350 * 1024;
export const FONT_BUDGET = 120 * 1024;

const dist = process.argv[2] ?? fileURLToPath(new URL("../../internal/console/dist", import.meta.url));

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    return statSync(p).isDirectory() ? files(p) : [p];
  });
}

/** The scripts index.html loads, and their static imports, transitively. */
function firstLoad() {
  const html = readFileSync(join(dist, "index.html"), "utf8");
  const queue = Array.from(html.matchAll(/<script[^>]+src="\/console\/([^"]+\.js)"/g), (m) => m[1]);
  queue.push(...Array.from(html.matchAll(/<link[^>]+rel="modulepreload"[^>]+href="\/console\/([^"]+\.js)"/g), (m) => m[1]));
  const seen = new Set();
  while (queue.length > 0) {
    const rel = queue.shift();
    if (seen.has(rel)) continue;
    seen.add(rel);
    const code = readFileSync(join(dist, rel), "utf8");
    // A static import between chunks: `import … from "./x.js"` or `import "./x.js"`.
    for (const m of code.matchAll(/(?:^|[;\s}])import\s*(?:[^"'()]*?from\s*)?["'](\.\/[^"']+\.js)["']/g)) {
      queue.push(join(rel, "..", m[1]).replaceAll("\\", "/"));
    }
  }
  return [...seen];
}

const js = firstLoad();
const jsBytes = js.reduce((n, f) => n + gzipSync(readFileSync(join(dist, f)), { level: 9 }).length, 0);
const fonts = files(dist).filter((f) => /\.(woff2?|ttf|otf)$/.test(f));
const fontBytes = fonts.reduce((n, f) => n + statSync(f).size, 0);

const kb = (n) => (n / 1024).toFixed(1) + " KB";
console.log(`first-load JS (gzip): ${kb(jsBytes)} of ${kb(JS_BUDGET)} — ${js.join(", ")}`);
console.log(`fonts: ${kb(fontBytes)} of ${kb(FONT_BUDGET)} — ${fonts.map((f) => relative(dist, f)).join(", ")}`);
let fail = false;
if (js.length === 0) {
  console.error("budget: index.html loads no script, so this measured nothing");
  fail = true;
}
if (jsBytes > JS_BUDGET) {
  console.error(`budget: first-load JS is ${kb(jsBytes)}, over ${kb(JS_BUDGET)}`);
  fail = true;
}
if (fontBytes > FONT_BUDGET) {
  console.error(`budget: fonts are ${kb(fontBytes)}, over ${kb(FONT_BUDGET)}`);
  fail = true;
}
process.exit(fail ? 1 : 0);
