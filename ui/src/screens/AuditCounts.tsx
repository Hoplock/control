// THE AUDIT COUNTS (0051; DESIGN.md §7, §8, §11): hand-written SVG, the second
// of the two charts the console allows, so no chart library. It counts the
// records ON THIS PAGE by kind — never the tenant's, which no answer holds
// (M26) — and says so, and says when the answer was cut. It is a real table
// with a caption, the count in figures beside every bar, and the bars in one
// neutral ink, so it reads in greyscale and to a screen reader alike; the
// severities are counted beside it in words.

import type { AuditRecord } from "../api/types";
import { useI18n } from "../i18n/I18n";
import { countByKind, countBySeverity, SEVERITIES } from "./auditModel";
import { useKindLabel, useSeverityLabel } from "./auditLabels";
import styles from "./Audit.module.css";

const WIDTH = 100;
const HEIGHT = 8;

export function AuditCounts({ records, truncated }: { records: readonly AuditRecord[]; truncated: boolean }) {
  const { t, f, dir } = useI18n();
  const kindLabel = useKindLabel();
  const severityLabel = useSeverityLabel();
  const rows = countByKind(records);
  const most = Math.max(1, ...rows.map((r) => r.count));
  const severities = countBySeverity(records);
  return (
    <figure className={styles.counts} data-chart="audit-counts">
      <table className={styles.countTable}>
        <caption className={styles.countCaption}>
          {truncated
            ? t("audit.counts.captionCut", { count: records.length })
            : t("audit.counts.caption", { count: records.length })}
        </caption>
        <thead>
          <tr>
            <th scope="col">{t("audit.col.kind")}</th>
            <th scope="col" className={styles.countNumber}>
              {t("audit.counts.records")}
            </th>
            <th scope="col" className={styles.countBarHead}>
              <span className={styles.hidden}>{t("audit.counts.bar")}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const w = (r.count / most) * WIDTH;
            return (
              <tr key={r.kind} data-kind={r.kind}>
                <th scope="row">{kindLabel(r.kind)}</th>
                <td className={styles.countNumber}>{f.number(r.count)}</td>
                <td className={styles.countBarCell}>
                  <svg className={styles.countBar} viewBox={`0 0 ${WIDTH} ${HEIGHT}`} preserveAspectRatio="none" aria-hidden="true" focusable="false">
                    <rect width={WIDTH} height={HEIGHT} className={styles.countTrack} />
                    <rect x={dir === "rtl" ? WIDTH - w : 0} width={w} height={HEIGHT} className={styles.countFill} />
                  </svg>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <figcaption>
        <ul className={styles.severities} aria-label={t("audit.counts.severities")}>
          {SEVERITIES.map((s) => (
            <li key={s} data-severity={s}>
              <span className={styles.severity} data-severity={s}>
                {severityLabel(s)}
              </span>
              <span className={styles.countNumber}>{f.number(severities[s])}</span>
            </li>
          ))}
        </ul>
      </figcaption>
    </figure>
  );
}
