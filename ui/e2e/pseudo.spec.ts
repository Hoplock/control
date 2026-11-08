// DESIGN.md §10.7 and §10.8, over every registered screen and state:
//   en-XA: no string renders unaccented (a string that does is hardcoded
//          English), and nothing clips or overflows at +40%;
//   en-XB: the page is right-to-left, no stylesheet or inline style carries a
//          physical property, and every block box is the mirror image of its
//          en-XA position — the property a physical property breaks.
// Identifiers and operator data are never localised (§9): they are marked
// translate="no", and the unaccented check reads past them, as it does a
// <time> (formatted by Intl, not the catalogue).

import { expect, test, type Page } from "@playwright/test";
import { THEMES } from "./harness";
import { openScreen, statesOf } from "./open";
import { SCREENS } from "./screens";

/** Every visible text and every read-aloud attribute that comes out unaccented. */
async function unaccented(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out: string[] = [];
    const exempt = (el: Element | null) => Boolean(el?.closest('[translate="no"], time, script, style, svg, [data-gallery-scaffold]'));
    const plain = (s: string) => /[A-Za-z]/.test(s) && !/[À-ÿ]/.test(s);
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const text = (n.textContent ?? "").trim();
      const el = n.parentElement;
      if (!text || exempt(el) || !el || el.closest("[hidden]")) continue;
      if (el.getClientRects().length === 0 && !el.closest('[role="status"], [role="alert"], caption')) continue;
      if (plain(text)) out.push(`text ${JSON.stringify(text)} in <${el.tagName.toLowerCase()} class="${el.className}">`);
    }
    for (const el of Array.from(document.querySelectorAll("[aria-label], [title], [placeholder], [alt]"))) {
      if (exempt(el)) continue;
      for (const a of ["aria-label", "title", "placeholder", "alt"]) {
        const v = el.getAttribute(a);
        if (v && plain(v)) out.push(`${a}=${JSON.stringify(v)} on <${el.tagName.toLowerCase()}>`);
      }
    }
    return out;
  });
}

/** Text clipped by its box, and any sideways scroll of the page itself. */
async function clipped(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out: string[] = [];
    if (document.documentElement.scrollWidth > window.innerWidth + 1) out.push("the page scrolls sideways");
    for (const el of Array.from(document.querySelectorAll<HTMLElement>("body *"))) {
      if (el.closest('[translate="no"], svg')) continue;
      const cs = getComputedStyle(el);
      const hides = (v: string) => v === "hidden" || v === "clip";
      if (!hides(cs.overflowX) && !hides(cs.overflowY)) continue;
      if (el.getClientRects().length === 0 || cs.clipPath === "inset(50%)") continue;
      const wide = hides(cs.overflowX) && el.scrollWidth > el.clientWidth + 1;
      const tall = hides(cs.overflowY) && el.scrollHeight > el.clientHeight + 1;
      if ((wide || tall) && (el.textContent ?? "").trim()) {
        out.push(`<${el.tagName.toLowerCase()} class="${el.className}"> clips its text (${el.scrollWidth}×${el.scrollHeight} in ${el.clientWidth}×${el.clientHeight})`);
      }
    }
    return out;
  });
}

/**
 * Physical pairs. The CSSOM expands a shorthand into its physical longhands,
 * so `margin: 0 8px` reads as margin-left and margin-right; that is
 * symmetric and mirrors itself. A leak is a pair whose two sides differ, or
 * a side set without its pair — which only a physical longhand can do.
 */
const PAIRS: readonly [string, string][] = [
  ["margin-left", "margin-right"],
  ["padding-left", "padding-right"],
  ["border-left-width", "border-right-width"],
  ["border-left-style", "border-right-style"],
  ["border-left-color", "border-right-color"],
  ["left", "right"],
  ["border-top-left-radius", "border-top-right-radius"],
  ["border-bottom-left-radius", "border-bottom-right-radius"],
  ["scroll-margin-left", "scroll-margin-right"],
  ["scroll-padding-left", "scroll-padding-right"],
];

async function physicalProperties(page: Page): Promise<string[]> {
  return page.evaluate((pairs) => {
    const out: string[] = [];
    const check = (where: string, style: CSSStyleDeclaration) => {
      for (const [a, b] of pairs) {
        const x = style.getPropertyValue(a);
        const y = style.getPropertyValue(b);
        if (x !== y) out.push(`${where} { ${a}: ${x || "unset"}; ${b}: ${y || "unset"} }`);
      }
      for (const p of ["float", "clear", "text-align"]) {
        const v = style.getPropertyValue(p);
        if (v === "left" || v === "right") out.push(`${where} { ${p}: ${v} }`);
      }
    };
    const visit = (rules: CSSRuleList) => {
      for (const r of Array.from(rules)) {
        if (r instanceof CSSStyleRule) check(r.selectorText, r.style);
        if ("cssRules" in r && (r as CSSGroupingRule).cssRules) visit((r as CSSGroupingRule).cssRules);
      }
    };
    for (const sheet of Array.from(document.styleSheets)) visit(sheet.cssRules);
    for (const el of Array.from(document.querySelectorAll<HTMLElement>("[style]"))) check(`<${el.tagName.toLowerCase()} style>`, el.style);
    return out;
  }, PAIRS as [string, string][]);
}

/** The box of every rendered block-level element, in document order. */
async function boxes(page: Page): Promise<{ path: string; x: number; w: number }[]> {
  return page.evaluate(() => {
    const out: { path: string; x: number; w: number }[] = [];
    const W = document.documentElement.clientWidth;
    const all = Array.from(document.querySelectorAll<HTMLElement>("body *"));
    all.forEach((el, i) => {
      const cs = getComputedStyle(el);
      if (cs.display.startsWith("inline") || cs.display === "contents" || cs.position === "fixed") return;
      if (el.closest("svg, [translate=\"no\"], [role=\"tooltip\"], dialog:not([open]), [popover]:not(:popover-open)")) return;
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0 || r.right <= 0 || r.left >= W) return;
      out.push({ path: `${i}:${el.tagName.toLowerCase()}.${el.className}`, x: r.left, w: r.width });
    });
    return out;
  });
}

for (const screen of SCREENS) {
  test.describe(screen.id, () => {
    for (const theme of THEMES) {
      test(`en-XA: every string accented, nothing clipped, ${theme}`, async ({ browser }) => {
        for (const state of [undefined, ...statesOf(screen)]) {
          const page = await openScreen(browser, screen, { theme, state, locale: "en-XA" });
          const where = `${screen.id} ${state ?? "loaded"} ${theme}`;
          expect(await unaccented(page), `${where}: hardcoded text`).toEqual([]);
          expect(await clipped(page), `${where}: clipped at +40%`).toEqual([]);
          await page.context().close();
        }
      });
    }

    test("en-XB: right-to-left, no physical property, every block mirrored", async ({ browser }) => {
      const ltr = await openScreen(browser, screen, { locale: "en-XA", noKerning: true });
      const before = await boxes(ltr);
      const W = await ltr.evaluate(() => document.documentElement.clientWidth);
      await ltr.context().close();
      const rtl = await openScreen(browser, screen, { locale: "en-XB", noKerning: true });
      expect(await rtl.evaluate(() => document.documentElement.dir)).toBe("rtl");
      expect(await physicalProperties(rtl)).toEqual([]);
      const after = new Map((await boxes(rtl)).map((b) => [b.path, b]));
      const unmirrored = before
        .filter((b) => {
          const m = after.get(b.path);
          return m && Math.abs(m.x - (W - b.x - b.w)) > 2 && Math.abs(m.w - b.w) <= 2;
        })
        .map((b) => `${b.path}: x ${b.x.toFixed(0)} → ${after.get(b.path)?.x.toFixed(0)}, mirror is ${(W - b.x - b.w).toFixed(0)}`);
      expect(unmirrored).toEqual([]);
      await rtl.context().close();
    });
  });
}
