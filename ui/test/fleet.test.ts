// FLEET'S HEALTH BAR, laid out without a browser (0059): which segments are
// drawn and in what order, each one's start and width, which carry their
// glyph and count, and that the minimum width a narrow segment is held to
// never pushes the bar past its track.

import { describe, expect, it } from "vitest";
import { CHART_SEGMENTS, layoutBar, type BarGeometry, type BarSegment, type ChartSegment } from "../src/screens/fleetModel";
import { rootTokens } from "./tokens";

const counts = (c: Partial<Record<ChartSegment, number>>): Record<ChartSegment, number> => ({
  live: 0,
  stale: 0,
  unreachable: 0,
  pending: 0,
  revoked: 0,
  ...c,
});

/** The tokens as tokens.css declares them today, so a test reads the bar the console draws. */
const G: Omit<BarGeometry, "track"> = { gap: 2, minWidth: 6, labelMin: 72 };
const end = (bar: BarSegment[]) => {
  const last = bar.at(-1);
  return last ? last.start + last.width : 0;
};

describe("the health bar's layout", () => {
  it("draws one segment per state with a proxy in it, in the chart's order, each its share", () => {
    // The seeded fleet: seven live, one never heard from, one revoked.
    const bar = layoutBar(counts({ live: 7, unreachable: 1, revoked: 1 }), { ...G, track: 965 });
    expect(bar.map((b) => b.segment)).toEqual(["live", "unreachable", "revoked"]);
    expect(bar.map((b) => b.count)).toEqual([7, 1, 1]);
    const room = 965 - 2 * 2;
    expect(bar[0]?.width).toBeCloseTo((room * 7) / 9, 9);
    expect(bar[1]?.width).toBeCloseTo(room / 9, 9);
    expect(bar[2]?.width).toBeCloseTo(room / 9, 9);
    expect(bar.map((b) => b.labelled)).toEqual([true, true, true]);
  });

  it("starts each segment one gap after the last one ends, and ends the bar at its track", () => {
    const bar = layoutBar(counts({ live: 3, stale: 2, unreachable: 2, pending: 2, revoked: 2 }), { ...G, track: 1000 });
    expect(bar.map((b) => b.segment)).toEqual([...CHART_SEGMENTS]);
    expect(bar[0]?.start).toBe(0);
    for (let i = 1; i < bar.length; i++) {
      const prev = bar[i - 1] as BarSegment;
      expect((bar[i] as BarSegment).start - (prev.start + prev.width)).toBeCloseTo(2, 9);
    }
    expect(end(bar)).toBeCloseTo(1000, 9);
  });

  it("holds one proxy of hundreds at the minimum width, and leaves its label to the legend", () => {
    const bar = layoutBar(counts({ live: 500, revoked: 1 }), { ...G, track: 1000 });
    expect(bar.map((b) => [b.segment, b.labelled])).toEqual([
      ["live", true],
      ["revoked", false],
    ]);
    expect(bar[1]?.width).toBe(6);
    expect(bar[0]?.width).toBeCloseTo(1000 - 2 - 6, 9);
    expect(end(bar)).toBeCloseTo(1000, 9);
  });

  it("takes the minimum's width from the segments sized by their share, and holds one it squeezes too", () => {
    // Four narrow states beside a large one: each narrow one at the minimum.
    const many = layoutBar(counts({ live: 1000, stale: 1, unreachable: 1, pending: 1, revoked: 1 }), { ...G, track: 600 });
    expect(many.slice(1).map((b) => b.width)).toEqual([6, 6, 6, 6]);
    expect(end(many)).toBeCloseTo(600, 9);
    // Two at the minimum leave stale less than its share, and under the
    // minimum itself: it is held there as well, and live takes the rest.
    const squeezed = layoutBar(counts({ live: 80, stale: 10, unreachable: 1, pending: 1 }), {
      gap: 0,
      minWidth: 10,
      labelMin: 50,
      track: 100,
    });
    expect(squeezed.map((b) => [b.segment, b.width])).toEqual([
      ["live", 70],
      ["stale", 10],
      ["unreachable", 10],
      ["pending", 10],
    ]);
  });

  it("labels a segment exactly as wide as the threshold, and not one a pixel narrower", () => {
    const at = layoutBar(counts({ live: 1, revoked: 1 }), { gap: 0, minWidth: 6, labelMin: 50, track: 100 });
    expect(at.map((b) => b.labelled)).toEqual([true, true]);
    const under = layoutBar(counts({ live: 1, revoked: 1 }), { gap: 0, minWidth: 6, labelMin: 51, track: 100 });
    expect(under.map((b) => b.labelled)).toEqual([false, false]);
  });

  it("shares a track too narrow for every minimum equally, and shrinks gaps it cannot hold", () => {
    const narrow = layoutBar(counts({ live: 100, stale: 1, unreachable: 1, pending: 1, revoked: 1 }), { ...G, track: 20 });
    for (const b of narrow) expect(b.width).toBeCloseTo((20 - 4 * 2) / 5, 9);
    expect(end(narrow)).toBeCloseTo(20, 9);
    const tiny = layoutBar(counts({ live: 1, revoked: 1 }), { ...G, track: 1 });
    expect(end(tiny)).toBeLessThanOrEqual(1);
  });

  it("draws nothing for an empty fleet or a bar not yet measured", () => {
    expect(layoutBar(counts({}), { ...G, track: 1000 })).toEqual([]);
    expect(layoutBar(counts({ live: 3 }), { ...G, track: 0 })).toEqual([]);
  });

  it("never runs past its track, and never draws a segment under the minimum the room allows", () => {
    // A deterministic sweep, so a failure names its case.
    let seed = 59;
    const next = (n: number) => {
      seed = (seed * 1103515245 + 12345) % 2147483648;
      return seed % n;
    };
    for (let i = 0; i < 2000; i++) {
      const c = counts({
        live: next(3) === 0 ? 0 : next(5000),
        stale: next(4) === 0 ? 0 : next(40),
        unreachable: next(3) === 0 ? 0 : next(10),
        pending: next(2) === 0 ? 0 : next(1000),
        revoked: next(2) === 0 ? 0 : next(3),
      });
      const g: BarGeometry = { track: 1 + next(1600), gap: next(5), minWidth: next(12), labelMin: 40 + next(60) };
      const bar = layoutBar(c, g);
      const shown = CHART_SEGMENTS.filter((s) => c[s] > 0);
      const why = JSON.stringify({ c, g });
      expect(bar.map((b) => b.segment), why).toEqual(shown);
      expect(end(bar), why).toBeLessThanOrEqual(g.track + 1e-9);
      if (bar.length === 0) continue;
      expect(end(bar), why).toBeCloseTo(g.track, 6);
      const gaps = Math.min(g.gap, g.track / Math.max(1, bar.length - 1)) * (bar.length - 1);
      const floor = Math.min(g.minWidth, (g.track - gaps) / bar.length);
      for (const b of bar) {
        expect(b.width, why).toBeGreaterThanOrEqual(floor - 1e-9);
        expect(b.labelled, why).toBe(b.width >= g.labelMin);
      }
    }
  });
});

describe("the bar's layout tokens", () => {
  // The bar reads these from the page (FleetChart's useBarGeometry) as pixels.
  it("declares each, as a length the bar can read", () => {
    const tokens = rootTokens();
    expect(tokens.get("--chart-segment-gap")).toBe("var(--sp-2)");
    expect(tokens.get("--chart-segment-min")).toMatch(/^\d+px$/);
    expect(tokens.get("--chart-label-min")).toMatch(/^\d+px$/);
  });
});
