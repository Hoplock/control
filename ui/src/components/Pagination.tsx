// PAGINATION OVER A BOUNDED LIST (DESIGN.md §7, PLAN M26). A north-bound list
// answers one page — `limit` items, `truncated` when there are more, and a
// `next` cursor that continues it — and never the whole tenant. So this pager
// moves by cursor: forward by the answer's own `next`, back by the cursors it
// has already followed, and it says in words when the answer was cut, so a
// table never reads as complete when it is not.

import { useI18n } from "../i18n/I18n";
import { Button } from "./Button";
import styles from "./Pagination.module.css";

export interface PageState {
  /** The cursors followed to reach this page: [] is the first page. */
  trail: readonly string[];
  limit: number;
}

export function Pagination({
  page,
  shown,
  truncated,
  next,
  onPage,
}: {
  page: PageState;
  /** Items on this page. */
  shown: number;
  /** The answer's own signal that there is more. */
  truncated: boolean;
  next?: string;
  onPage(next: PageState): void;
}) {
  const { t, f } = useI18n();
  const number = page.trail.length + 1;
  return (
    <nav className={styles.pager} aria-label={t("component.pagination.label")}>
      <p className={styles.where} aria-live="polite">
        {truncated
          ? t("component.pagination.truncated", { page: f.number(number), count: f.number(shown) })
          : t("component.pagination.complete", { page: f.number(number), count: f.number(shown) })}
      </p>
      <div className={styles.buttons}>
        <Button disabled={page.trail.length === 0} onClick={() => onPage({ ...page, trail: [] })}>
          {t("component.pagination.first")}
        </Button>
        <Button
          disabled={page.trail.length === 0}
          onClick={() => onPage({ ...page, trail: page.trail.slice(0, -1) })}
        >
          {t("component.pagination.previous")}
        </Button>
        <Button
          disabled={!truncated || !next}
          onClick={() => next && onPage({ ...page, trail: [...page.trail, next] })}
        >
          {t("component.pagination.next")}
        </Button>
      </div>
    </nav>
  );
}
