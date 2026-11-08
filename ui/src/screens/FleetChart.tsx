// THE FLEET HEALTH CHART (DESIGN.md §7, §8, §11): hand-written SVG, one of
// the two charts the console allows, so no chart library. One bar, the whole
// tenant's enrolled fleet counted by the server (instance health's fleet
// summary), segmented by health. Readable in greyscale: every segment carries
// a pattern of its own as well as its hue, the legend pairs each glyph with
// its word and count, and the bar has a sentence for a screen reader. It
// fills from the inline start, so a right-to-left page reads it from the
// right.

import type { FleetSummary } from "../api/types";
import { STATUS, StatusGlyph } from "../components/Status";
import { useI18n } from "../i18n/I18n";
import { CHART_SEGMENTS, SEGMENT_STATUS, segmentCounts } from "./fleetModel";
import styles from "./Fleet.module.css";

const WIDTH = 100;
const HEIGHT = 6;

export function FleetChart({ summary }: { summary: FleetSummary }) {
  const { t, f, dir } = useI18n();
  const counts = segmentCounts(summary);
  const total = CHART_SEGMENTS.reduce((n, s) => n + counts[s], 0);
  let at = 0;
  const bars = CHART_SEGMENTS.filter((s) => counts[s] > 0).map((s) => {
    const w = (counts[s] / total) * WIDTH;
    const x = dir === "rtl" ? WIDTH - at - w : at;
    at += w;
    return { s, x, w };
  });
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
      <svg
        className={styles.bar}
        viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        preserveAspectRatio="none"
        role="img"
        aria-label={sentence}
        data-total={total}
      >
        <defs>
          <pattern id="fleet-p-stale" width="2" height="2" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
            <rect width="2" height="2" className={styles.segStale} />
            <rect width="0.7" height="2" className={styles.patternInk} />
          </pattern>
          <pattern id="fleet-p-unreachable" width="2" height="2" patternUnits="userSpaceOnUse">
            <rect width="2" height="2" className={styles.segUnreachable} />
            <circle cx="1" cy="1" r="0.45" className={styles.patternInk} />
          </pattern>
          <pattern id="fleet-p-pending" width="2" height="1.5" patternUnits="userSpaceOnUse">
            <rect width="2" height="1.5" className={styles.segPending} />
            <rect width="2" height="0.5" className={styles.patternInk} />
          </pattern>
          <pattern id="fleet-p-revoked" width="2" height="2" patternUnits="userSpaceOnUse">
            <rect width="2" height="2" className={styles.segRevoked} />
            <path d="M0 0 2 2M2 0 0 2" className={styles.patternStroke} />
          </pattern>
        </defs>
        <rect width={WIDTH} height={HEIGHT} className={styles.track} />
        {bars.map(({ s, x, w }) => (
          <rect
            key={s}
            x={x}
            width={w}
            height={HEIGHT}
            className={s === "live" ? `${styles.segment} ${styles.segLive}` : styles.segment}
            data-segment={s}
            fill={s === "live" ? undefined : `url(#fleet-p-${s})`}
          />
        ))}
      </svg>
      <figcaption>
        <ul className={styles.legend}>
          {CHART_SEGMENTS.map((s) => {
            const status = STATUS[SEGMENT_STATUS[s]];
            return (
              <li key={s} className={styles.legendItem} data-segment={s}>
                <span className={styles.swatch} data-segment={s} aria-hidden="true" />
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
