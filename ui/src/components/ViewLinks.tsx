// A screen's views, as links (DESIGN.md §7): each view is its own deep link
// under /console/, so the bar is navigation, not tabs — a view is opened,
// bookmarked and shared by its URL, and the current one is
// aria-current="page". The audit view's records and blocked commands (0051)
// and the inventory's lists (0054) are each a set of these.

import type { ReactNode } from "react";
import { Link } from "../router/router";
import styles from "./ViewLinks.module.css";

export interface ViewLink {
  id: string;
  /** Its path inside the console. */
  to: string;
  label: ReactNode;
}

export function ViewLinks({ label, views, current }: { label: string; views: readonly ViewLink[]; current: string }) {
  return (
    <nav className={styles.views} aria-label={label}>
      {views.map((v) => (
        <Link key={v.id} to={v.to} className={styles.view} aria-current={current === v.id ? "page" : undefined} data-view={v.id}>
          {v.label}
        </Link>
      ))}
    </nav>
  );
}
