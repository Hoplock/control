// THE CONTACT SHEET (0060). Lays the committed screenshot set
// (ui/e2e/baseline/) out side by side — one sheet per area of the console and
// theme, every screen, density and state of that area in it — so the console
// can be reviewed whole, by eye, for what no per-screen check sees: spacing
// rhythm, the type scale, where the primary action sits, whether two screens
// that each pass agree (DESIGN.md §10). It reads only the baseline, so it
// shows exactly what CI diffs, and needs no server.
//
//   npm run contact-sheet            → ui/build/contact-sheet/<area>-<theme>.png
//   CONTACT_AREAS=fleet,audit npm run contact-sheet
//
// Never a CI job: its output is evidence for a reviewer, attached to the PR
// of a phase that changes how the console looks (docs/PROTOCOL.md §8).

import { mkdirSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright-core";

const ui = fileURLToPath(new URL("..", import.meta.url));
const baseline = join(ui, "e2e", "baseline");
const out = join(ui, "build", "contact-sheet");
mkdirSync(out, { recursive: true });

/** An image's area: the first word of its screen id (`inventory-subject-dark` → inventory). */
const areaOf = (name) => (name.startsWith("sign-in") ? "sign-in" : name.split("-")[0]);
const themeOf = (name) => (/(^|-)dark(-|\.png$)/.test(name) ? "dark" : "light");
const only = process.env.CONTACT_AREAS?.split(",").filter(Boolean);

const sheets = new Map();
for (const name of readdirSync(baseline).filter((n) => n.endsWith(".png")).sort()) {
  const key = `${areaOf(name)}-${themeOf(name)}`;
  if (only && !only.includes(areaOf(name))) continue;
  if (!sheets.has(key)) sheets.set(key, []);
  sheets.get(key).push(name);
}

const escape = (s) => s.replace(/[&<>"]/g, (c) => `&#${c.charCodeAt(0)};`);
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1760, height: 900 } });
for (const [key, names] of sheets) {
  const tiles = names
    .map((n) => {
      const uri = `data:image/png;base64,${readFileSync(join(baseline, n)).toString("base64")}`;
      return `<figure><img src="${uri}" alt=""><figcaption>${escape(n.replace(/\.png$/, ""))}</figcaption></figure>`;
    })
    .join("");
  const dark = key.endsWith("-dark");
  await page.setContent(
    `<!doctype html><html><head><style>
      body { margin: 0; padding: 24px; background: ${dark ? "#000" : "#fff"}; color: ${dark ? "#ddd" : "#222"}; font: 13px system-ui, sans-serif; }
      h1 { margin: 0 0 16px; font-size: 18px; }
      main { display: grid; grid-template-columns: repeat(${names.length > 40 ? 6 : 4}, 1fr); gap: 16px; }
      figure { margin: 0; border: 1px solid ${dark ? "#333" : "#ccc"}; }
      img { display: block; inline-size: 100%; block-size: 360px; object-fit: cover; object-position: top; }
      figcaption { padding: 4px 8px; font-family: ui-monospace, monospace; font-size: 11px; }
    </style></head><body><h1>${escape(key)} — ${names.length} images</h1><main>${tiles}</main></body></html>`,
    { waitUntil: "load" },
  );
  await page.screenshot({ path: join(out, `${key}.png`), fullPage: true });
  console.log(`${key}: ${names.length} images`);
}
await browser.close();
