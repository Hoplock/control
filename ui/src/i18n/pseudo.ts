// The pseudolocales (DESIGN.md §10.7, §10.8, PLAN M21).
//
// en-XA accents every letter of every catalogue string and pads it by 40%,
// so a string that renders unaccented is hardcoded English and a layout that
// clips is too tight for a real translation. en-XB does the same and wraps
// each run of text in a right-to-left override, under dir="rtl", so a
// physical CSS property shows as a mirrored layout that is not mirrored.
//
// Only the message's text is changed: placeholders, plural and select
// keywords and the # of a plural are the message's grammar, and accenting
// them would break it. So each message is parsed and its literals rewritten,
// and the catalogue the build carries is the parsed form.

import { parse, TYPE, type MessageFormatElement } from "@formatjs/icu-messageformat-parser";

/**
 * Every accent is in Latin-1, which the bundled faces cover (DESIGN.md §3.5),
 * so a pseudolocale build renders in the console's own type rather than a
 * fallback face with other metrics. Letters Latin-1 has no accented form for
 * stay as they are; every string has a vowel, and a vowel always changes.
 */
const ACCENTS: Record<string, string> = {
  a: "á", c: "ç", d: "ð", e: "é", i: "í", n: "ñ", o: "ö", p: "þ", u: "ü", y: "ý",
  A: "Å", C: "Ç", D: "Ð", E: "É", I: "Î", N: "Ñ", O: "Ø", P: "Þ", U: "Û", Y: "Ý",
};

/** The expansion every pseudolocalised string carries (DESIGN.md §9). */
export const EXPANSION = 0.4;

const RLO = "‮";
const PDF = "‬";
const PAD = "·";

export type PseudoMode = "en-XA" | "en-XB";

export function accent(text: string): string {
  return Array.from(text, (c) => ACCENTS[c] ?? c).join("");
}

/** The length of a message's own text, before any rewriting. */
export function textLength(elements: MessageFormatElement[]): number {
  let n = 0;
  for (const e of elements) {
    if (e.type === TYPE.literal) n += e.value.length;
    else if (e.type === TYPE.tag) n += textLength(e.children);
    else if (e.type === TYPE.plural || e.type === TYPE.select) {
      // The longest branch is what the layout has to hold.
      n += Math.max(0, ...Object.values(e.options).map((o) => textLength(o.value)));
    } else n += 4; // an argument stands for some text of its own
  }
  return n;
}

function rewrite(elements: MessageFormatElement[], mode: PseudoMode): MessageFormatElement[] {
  return elements.map((e) => {
    switch (e.type) {
      case TYPE.literal: {
        const accented = accent(e.value);
        return { ...e, value: mode === "en-XB" ? RLO + accented + PDF : accented };
      }
      case TYPE.tag:
        return { ...e, children: rewrite(e.children, mode) };
      case TYPE.plural:
      case TYPE.select:
        return {
          ...e,
          options: Object.fromEntries(
            Object.entries(e.options).map(([k, o]) => [k, { ...o, value: rewrite(o.value, mode) }]),
          ),
        } as MessageFormatElement;
      default:
        return e;
    }
  });
}

/** One message, pseudolocalised, as the parsed form a formatter takes. */
export function pseudolocalise(message: string, mode: PseudoMode): MessageFormatElement[] {
  const parsed = parse(message);
  const pad = PAD.repeat(Math.ceil(textLength(parsed) * EXPANSION));
  const open = mode === "en-XB" ? RLO + "[" + PDF : "[";
  const close = mode === "en-XB" ? RLO + pad + "]" + PDF : pad + "]";
  return [
    { type: TYPE.literal, value: open },
    ...rewrite(parsed, mode),
    { type: TYPE.literal, value: close },
  ];
}

/** A whole catalogue, pseudolocalised. */
export function pseudoCatalogue<K extends string>(
  catalogue: Record<K, string>,
  mode: PseudoMode,
): Record<K, MessageFormatElement[]> {
  const out = {} as Record<K, MessageFormatElement[]>;
  for (const key of Object.keys(catalogue) as K[]) out[key] = pseudolocalise(catalogue[key], mode);
  return out;
}
