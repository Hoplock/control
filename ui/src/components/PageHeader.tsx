// PAGE HEADER (DESIGN.md §7): title, the object's id, its status, and the
// page's actions — one per page, at the top of its content.

import type { ReactNode } from "react";
import styles from "./PageHeader.module.css";

export function PageHeader({
  title,
  lede,
  id,
  status,
  actions,
  breadcrumb,
}: {
  title: ReactNode;
  lede?: ReactNode;
  id?: ReactNode;
  status?: ReactNode;
  actions?: ReactNode;
  breadcrumb?: ReactNode;
}) {
  return (
    <header className={styles.header}>
      {breadcrumb}
      <div className={styles.row}>
        <div className={styles.heading}>
          <h1 className={styles.title}>{title}</h1>
          {id}
          {status}
        </div>
        {actions ? <div className={styles.actions}>{actions}</div> : null}
      </div>
      {lede ? <p className={styles.lede}>{lede}</p> : null}
    </header>
  );
}
