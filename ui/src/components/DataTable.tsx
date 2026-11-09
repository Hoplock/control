// THE DATA TABLE (DESIGN.md §7, §8; PLAN M26).
//
// Real table markup (<table>, <th scope>, a caption), a header that stays in
// view, the first column pinned while the rest scroll inside the table's own
// container (the page never scrolls sideways, §4), sortable columns
// (aria-sort), column visibility, row selection and per-row actions. A table
// shows ONE page of a bounded list: sorting and filtering act on the rows it
// holds, and when the answer was cut the table says so beside them, so it
// never reads as the whole tenant (M26). Paging is the Pagination beneath it.

import { useMemo, useState, type ReactNode } from "react";
import { useI18n } from "../i18n/I18n";
import { Checkbox } from "./Field";
import { Menu, type MenuItem } from "./Menu";
import { Skeleton, SpinnerOverlay } from "./States";
import styles from "./DataTable.module.css";

export interface Column<T> {
  id: string;
  header: string;
  cell(row: T): ReactNode;
  /** A column with a sort value is sortable. */
  sortValue?(row: T): string | number;
  /** Numbers align to the end and use tabular figures. */
  numeric?: boolean;
}

export type SortDir = "ascending" | "descending";

export interface TableSort {
  column: string;
  dir: SortDir;
}

const KebabGlyph = (
  <svg viewBox="0 0 16 16" fill="currentColor">
    <circle cx="8" cy="3.5" r="1.3" />
    <circle cx="8" cy="8" r="1.3" />
    <circle cx="8" cy="12.5" r="1.3" />
  </svg>
);

const ColumnsGlyph = (
  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
    <rect x="2" y="3" width="12" height="10" rx="1" />
    <path d="M6 3v10M10 3v10" />
  </svg>
);

export function DataTable<T>({
  caption,
  columns,
  rows,
  rowKey,
  rowLabel,
  sort,
  onSort,
  hidden = [],
  onHidden,
  selected,
  onSelected,
  rowActions,
  refreshing = false,
  partialNote,
  empty,
}: {
  /** What the table lists, for assistive technology. */
  caption: string;
  columns: readonly Column<T>[];
  rows: readonly T[];
  rowKey(row: T): string;
  /** A row's name, for its checkbox and its actions menu. */
  rowLabel(row: T): string;
  sort?: TableSort | null;
  onSort?(sort: TableSort | null): void;
  hidden?: readonly string[];
  onHidden?(hidden: string[]): void;
  selected?: ReadonlySet<string>;
  onSelected?(selected: Set<string>): void;
  rowActions?(row: T): readonly MenuItem[];
  refreshing?: boolean;
  /** Said beside the table when its rows are one page of more. */
  partialNote?: string;
  /** What the table shows when it holds no rows after filtering. */
  empty?: ReactNode;
}) {
  const { t } = useI18n();
  const visible = columns.filter((c, i) => i === 0 || !hidden.includes(c.id));
  const sorted = useMemo(() => {
    if (!sort) return rows;
    const col = columns.find((c) => c.id === sort.column);
    if (!col?.sortValue) return rows;
    const value = col.sortValue;
    const out = [...rows].sort((a, b) => {
      const x = value(a);
      const y = value(b);
      const c = typeof x === "number" && typeof y === "number" ? x - y : String(x).localeCompare(String(y));
      return sort.dir === "ascending" ? c : -c;
    });
    return out;
  }, [rows, columns, sort]);
  const keys = sorted.map(rowKey);
  const allSelected = selected !== undefined && keys.length > 0 && keys.every((k) => selected.has(k));
  const someSelected = selected !== undefined && keys.some((k) => selected.has(k));
  const toggleAll = () => {
    if (!onSelected) return;
    onSelected(allSelected ? new Set() : new Set(keys));
  };
  const toggle = (k: string) => {
    if (!onSelected || !selected) return;
    const next = new Set(selected);
    if (next.has(k)) next.delete(k);
    else next.add(k);
    onSelected(next);
  };
  const nextSort = (id: string): TableSort | null => {
    if (sort?.column !== id) return { column: id, dir: "ascending" };
    if (sort.dir === "ascending") return { column: id, dir: "descending" };
    return null;
  };
  return (
    <div className={styles.wrap}>
      <div className={styles.toolbar}>
        {partialNote ? <p className={styles.note} data-partial-note="">{partialNote}</p> : <span />}
        {onHidden ? (
          <Menu
            label={t("component.table.columns")}
            triggerLabel={t("component.table.columns")}
            trigger={ColumnsGlyph}
            items={columns.slice(1).map((c) => ({
              id: c.id,
              label: c.header,
              checked: !hidden.includes(c.id),
              onSelect: () =>
                onHidden(hidden.includes(c.id) ? hidden.filter((h) => h !== c.id) : [...hidden, c.id]),
            }))}
          />
        ) : null}
      </div>
      <div className={styles.scroll} tabIndex={0} role="region" aria-label={caption}>
        <table className={styles.table} aria-busy={refreshing || undefined}>
          <caption className={styles.caption}>{caption}</caption>
          <thead>
            <tr>
              {visible.map((c, i) => {
                const dir = sort?.column === c.id ? sort.dir : undefined;
                return (
                  <th
                    key={c.id}
                    scope="col"
                    aria-sort={dir}
                    className={i === 0 ? styles.pinned : undefined}
                    data-numeric={c.numeric || undefined}
                  >
                    <span className={styles.headCell}>
                      {i === 0 && selected !== undefined ? (
                        <Checkbox
                          label={<span className={styles.hidden}>{t("component.table.selectAll")}</span>}
                          checked={allSelected}
                          ref={(el) => {
                            if (el) el.indeterminate = someSelected && !allSelected;
                          }}
                          onChange={toggleAll}
                        />
                      ) : null}
                      {c.sortValue && onSort ? (
                        <button type="button" className={styles.sort} onClick={() => onSort(nextSort(c.id))}>
                          {c.header}
                          <svg viewBox="0 0 10 10" aria-hidden="true" data-dir={dir ?? "none"}>
                            <path d="M2.5 4 5 1.5 7.5 4M2.5 6 5 8.5 7.5 6" />
                          </svg>
                        </button>
                      ) : (
                        c.header
                      )}
                    </span>
                  </th>
                );
              })}
              {rowActions ? (
                <th scope="col" className={styles.actionsHead}>
                  <span className={styles.hidden}>{t("component.table.actions")}</span>
                </th>
              ) : null}
            </tr>
          </thead>
          <tbody>
            {sorted.map((row) => {
              const k = rowKey(row);
              const isSelected = selected?.has(k) ?? false;
              return (
                <tr key={k} aria-selected={selected !== undefined ? isSelected : undefined} data-row={k}>
                  {visible.map((c, i) =>
                    i === 0 ? (
                      <th key={c.id} scope="row" className={styles.pinned}>
                        <span className={styles.headCell}>
                          {selected !== undefined ? (
                            <Checkbox
                              label={<span className={styles.hidden}>{t("component.table.select", { name: rowLabel(row) })}</span>}
                              checked={isSelected}
                              onChange={() => toggle(k)}
                            />
                          ) : null}
                          {c.cell(row)}
                        </span>
                      </th>
                    ) : (
                      <td key={c.id} data-numeric={c.numeric || undefined}>
                        {c.cell(row)}
                      </td>
                    ),
                  )}
                  {rowActions ? (
                    <td className={styles.actionsCell}>
                      <Menu
                        label={t("component.table.rowActions", { name: rowLabel(row) })}
                        triggerLabel={t("component.table.rowActions", { name: rowLabel(row) })}
                        trigger={KebabGlyph}
                        items={rowActions(row)}
                      />
                    </td>
                  ) : null}
                </tr>
              );
            })}
          </tbody>
        </table>
        {refreshing ? <SpinnerOverlay label={t("common.loading")} /> : null}
      </div>
      {sorted.length === 0 && empty ? <div className={styles.emptyRow}>{empty}</div> : null}
    </div>
  );
}

/** A table's loading state: its own header and rows of skeleton, so nothing shifts. */
export function TableSkeleton({ headers, rows = 6 }: { headers: readonly string[]; rows?: number }) {
  return (
    <div className={styles.wrap}>
      <div className={styles.scroll}>
        <table className={styles.table}>
          <thead>
            <tr>
              {headers.map((h, i) => (
                <th key={h} scope="col" className={i === 0 ? styles.pinned : undefined}>
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {Array.from({ length: rows }, (_, r) => (
              <tr key={r}>
                {headers.map((h, i) => (
                  <td key={h} className={i === 0 ? styles.pinned : undefined}>
                    <Skeleton size={i === 0 ? "l" : "m"} />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
