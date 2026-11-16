// THE FLEET HEALTH CHART (DESIGN.md §7, §8, §11): hand-written SVG, one of
// the two charts the console allows, so no chart library. One bar, the whole
// tenant's enrolled fleet counted by the server (instance health's fleet
// summary), segmented by health: each segment solid in its status hue, with
// the surface left between two segments. Readable in greyscale and without
// colour (0059, the operator's choice over 0050's patterns): every segment
// wide enough carries its status glyph and its count, set in an ink that
// reads on every hue; a narrower one is still drawn, at a minimum width, and
// the legend pairs every state's glyph with its word and count; the bar has
// a sentence for a screen reader. It fills from the inline start, so a
// right-to-left page reads it from the right.
//
// The layout is fleetModel's layoutBar, in pixels: the bar is measured, and
// its three layout tokens (DESIGN.md §3.6) are read as the bar resolves them,
// so the SVG is drawn at one pixel to one unit and a glyph is never stretched.

import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import type { FleetSummary } from "../api/types";
import { STATUS, StatusGlyph } from "../components/Status";
import { useI18n } from "../i18n/I18n";
import { CHART_SEGMENTS, SEGMENT_STATUS, layoutBar, segmentCounts, type BarGeometry } from "./fleetModel";
import styles from "./Fleet.module.css";

/** A length token as the element resolves it, in pixels; 0 where it is not declared. */
function lengthToken(style: CSSStyleDeclaration, name: string): number {
  const v = Number.parseFloat(style.getPropertyValue(name));
  return Number.isFinite(v) ? v : 0;
}

function sameGeometry(a: BarGeometry, b: BarGeometry): boolean {
  return a.track === b.track && a.gap === b.gap && a.minWidth === b.minWidth && a.labelMin === b.labelMin;
}

/** The bar's width and layout tokens, read before the first paint and again whenever it resizes. */
function useBarGeometry(ref: RefObject<SVGSVGElement | null>): BarGeometry | null {
  const [geometry, setGeometry] = useState<BarGeometry | null>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const read = () => {
      const style = getComputedStyle(el);
      const next: BarGeometry = {
        track: el.getBoundingClientRect().width,
        gap: lengthToken(style, "--chart-segment-gap"),
        minWidth: lengthToken(style, "--chart-segment-min"),
        labelMin: lengthToken(style, "--chart-label-min"),
      };
      setGeometry((prev) => (prev && sameGeometry(prev, next) ? prev : next));
    };
    read();
    const observer = new ResizeObserver(read);
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref]);
  return geometry;
}

export function FleetChart({ summary }: { summary: FleetSummary }) {
  const { t, f, dir } = useI18n();
  const bar = useRef<SVGSVGElement>(null);
  const geometry = useBarGeometry(bar);
  const counts = segmentCounts(summary);
  const total = CHART_SEGMENTS.reduce((n, s) => n + counts[s], 0);
  const segments = geometry ? layoutBar(counts, geometry) : [];
  const sentence = t("fleet.chart.summary", {
    total: total,
    live: counts.live,
    stale: counts.stale,
    unreachable: counts.unreachable,
    pending: counts.pending,
    revoked: counts.revoked,
  });
  return (
    <figure className={styles.chart}>
      <svg ref={bar} className={styles.bar} role="img" aria-label={sentence} data-total={total}>
        {segments.length === 0 ? <rect width="100%" height="100%" className={styles.track} /> : null}
        {segments.map(({ segment, count, start, width, labelled }) => {
          const tone = STATUS[SEGMENT_STATUS[segment]].tone;
          const x = dir === "rtl" && geometry ? geometry.track - start - width : start;
          return (
            <g key={segment} data-segment={segment} data-labelled={labelled || undefined}>
              <rect x={x} width={width} height="100%" className={styles.segment} data-tone={tone} />
              {labelled ? (
                <foreignObject x={x} y={0} width={width} height="100%">
                  <div className={styles.segmentLabel}>
                    <StatusGlyph tone={tone} ink />
                    <span>{f.number(count)}</span>
                  </div>
                </foreignObject>
              ) : null}
            </g>
          );
        })}
      </svg>
      <figcaption>
        <ul className={styles.legend}>
          {CHART_SEGMENTS.map((s) => {
            const status = STATUS[SEGMENT_STATUS[s]];
            return (
              <li key={s} className={styles.legendItem} data-segment={s}>
                <StatusGlyph tone={status.tone} />
                <span>{t(status.label)}</span>
                <span className={styles.count}>{f.number(counts[s])}</span>
              </li>
            );
          })}
        </ul>
      </figcaption>
    </figure>
  );
}
