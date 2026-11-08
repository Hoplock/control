// SMALL LABELS (DESIGN.md §7): badge, chip/label, avatar, keyboard hint and
// breadcrumb. A badge is a count or a fact, neutral unless it carries a
// status (and then it is a Status, not a coloured badge: status hues are
// reserved, §3.4). A chip is an operator's label (`env=prod`), mono and never
// translated; a removable chip is how the filter bar shows a filter.

import type { ReactNode } from "react";
import { useI18n } from "../i18n/I18n";
import { Link } from "../router/router";
import styles from "./Badge.module.css";

export function Badge({ children, tone = "neutral" }: { children: ReactNode; tone?: "neutral" | "accent" | "info" }) {
  return (
    <span className={styles.badge} data-tone={tone}>
      {children}
    </span>
  );
}

/** An operator's label or a filter: data, so mono, LTR and untranslated when `data`. */
export function Chip({
  children,
  data = false,
  onRemove,
  removeLabel,
}: {
  children: ReactNode;
  data?: boolean;
  onRemove?: () => void;
  /** The remove button's accessible name: required when removable. */
  removeLabel?: string;
}) {
  return (
    <span className={styles.chip} data-data={data || undefined}>
      <span className={styles.chipText} dir={data ? "ltr" : undefined} translate={data ? "no" : undefined}>
        {children}
      </span>
      {onRemove ? (
        <button type="button" className={styles.remove} onClick={onRemove} aria-label={removeLabel}>
          <svg viewBox="0 0 12 12" aria-hidden="true" focusable="false">
            <path d="M3 3l6 6M9 3l-6 6" />
          </svg>
        </button>
      ) : null}
    </span>
  );
}

/** Initials of a name, as data: the name itself is the accessible label. */
export function Avatar({ name }: { name: string }) {
  const initials = name
    .split(/[\s._@-]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((p) => Array.from(p)[0]?.toUpperCase() ?? "")
    .join("");
  return (
    <span className={styles.avatar} role="img" aria-label={name} translate="no">
      <span aria-hidden="true">{initials}</span>
    </span>
  );
}

/** A key or a chord, as the keyboard shows it. Key names are not translated. */
export function KeyboardHint({ keys }: { keys: readonly string[] }) {
  return (
    <span className={styles.kbdGroup} dir="ltr" translate="no">
      {keys.map((k) => (
        <kbd key={k} className={styles.kbd}>
          {k}
        </kbd>
      ))}
    </span>
  );
}

/** Where a page sits. The last crumb is the page itself and is not a link. */
export function Breadcrumb({ items }: { items: readonly { label: string; to?: string }[] }) {
  const { t } = useI18n();
  return (
    <nav aria-label={t("component.breadcrumb.label")}>
      <ol className={styles.crumbs}>
        {items.map((item, i) => (
          <li key={`${item.label}-${i}`} className={styles.crumb}>
            {item.to && i < items.length - 1 ? (
              <Link to={item.to} className={styles.crumbLink}>
                {item.label}
              </Link>
            ) : (
              <span aria-current={i === items.length - 1 ? "page" : undefined}>{item.label}</span>
            )}
          </li>
        ))}
      </ol>
    </nav>
  );
}
