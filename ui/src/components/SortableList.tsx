// SORTABLE LIST (DESIGN.md §7, §8; 0066): an ordered list whose order is the
// point — the rules of a policy, tried top to bottom — that an operator
// reorders by dragging or by keyboard, and never by one alone. Each item has a
// rail at its inline start: its position, a handle and a move up and a move
// down button. The handle is dragged with a pointer, and with focus on it the
// up and down arrows move the item one place, Home and End to the ends; the
// buttons do the same for a pointer that does not drag. Every move is
// announced ("moved … to position 2 of 5") and focus follows the item it moved,
// so a keyboard user keeps their place. A last, fixed item (`fixedEnd`: the
// default deny) is shown in the same list and cannot be moved or passed.

import { useEffect, useId, useRef, useState, type DragEvent, type KeyboardEvent, type ReactNode } from "react";
import { useI18n } from "../i18n/I18n";
import { IconButton } from "./Button";
import { Skeleton } from "./States";
import styles from "./SortableList.module.css";

const GripGlyph = (
  <svg viewBox="0 0 16 16" fill="currentColor" stroke="none">
    <circle cx="6" cy="4" r="1.2" />
    <circle cx="10" cy="4" r="1.2" />
    <circle cx="6" cy="8" r="1.2" />
    <circle cx="10" cy="8" r="1.2" />
    <circle cx="6" cy="12" r="1.2" />
    <circle cx="10" cy="12" r="1.2" />
  </svg>
);
const UpGlyph = (
  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
    <path d="M4 10l4-4 4 4" />
  </svg>
);
const DownGlyph = (
  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
    <path d="M4 6l4 4 4-4" />
  </svg>
);

type Control = "handle" | "up" | "down";

export function SortableList<T>({
  label,
  items,
  itemKey,
  itemName,
  renderItem,
  onMove,
  disabled = false,
  fixedEnd,
}: {
  /** The list's accessible name. */
  label: string;
  items: readonly T[];
  /** A key that stays with an item when it moves. */
  itemKey(item: T, index: number): string;
  /** What an announcement and a control call the item: data (a rule id), shown as it is. */
  itemName(item: T, index: number): string;
  renderItem(item: T, index: number): ReactNode;
  /** Asks for the item at `from` to be put at `to`; the caller reorders `items`. */
  onMove(from: number, to: number): void;
  disabled?: boolean;
  /** A last item that is always last: shown, numbered, never moved. */
  fixedEnd?: ReactNode;
}) {
  const { t, f } = useI18n();
  const hintId = useId();
  const listRef = useRef<HTMLOListElement>(null);
  const [said, setSaid] = useState("");
  const [drag, setDrag] = useState<{ from: number; over: number | null; side: "before" | "after" } | null>(null);
  const refocus = useRef<{ key: string; control: Control } | null>(null);
  const count = items.length;

  useEffect(() => {
    const want = refocus.current;
    if (!want || !listRef.current) return;
    refocus.current = null;
    const item = listRef.current.querySelector<HTMLElement>(`[data-sortable-key="${CSS.escape(want.key)}"]`);
    const control =
      item?.querySelector<HTMLButtonElement>(`[data-sortable-control="${want.control}"]:not(:disabled)`) ??
      item?.querySelector<HTMLButtonElement>('[data-sortable-control="handle"]');
    control?.focus();
  });

  const move = (from: number, to: number, control: Control) => {
    const item = items[from];
    if (disabled || item === undefined || to < 0 || to >= count || to === from) return;
    refocus.current = { key: itemKey(item, from), control };
    setSaid(t("component.sortable.moved", { name: `\u2068${itemName(item, from)}\u2069`, position: f.number(to + 1), count: f.number(count) }));
    onMove(from, to);
  };

  const keys = (e: KeyboardEvent, index: number) => {
    const to =
      e.key === "ArrowUp" ? index - 1 : e.key === "ArrowDown" ? index + 1 : e.key === "Home" ? 0 : e.key === "End" ? count - 1 : null;
    if (to === null) return;
    e.preventDefault();
    move(index, Math.max(0, Math.min(count - 1, to)), "handle");
  };

  const over = (e: DragEvent<HTMLLIElement>, index: number) => {
    if (!drag) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    const box = e.currentTarget.getBoundingClientRect();
    const side = e.clientY < box.top + box.height / 2 ? "before" : "after";
    if (drag.over !== index || drag.side !== side) setDrag({ ...drag, over: index, side });
  };

  const drop = (e: DragEvent<HTMLLIElement>) => {
    e.preventDefault();
    if (!drag || drag.over === null) return setDrag(null);
    // The place the item lands, counted after it leaves its own.
    let to = drag.side === "before" ? drag.over : drag.over + 1;
    if (to > drag.from) to -= 1;
    setDrag(null);
    move(drag.from, to, "handle");
  };

  return (
    <div className={styles.wrap}>
      <ol ref={listRef} className={styles.list} aria-label={label} data-sortable="" data-disabled={disabled || undefined}>
        {items.map((item, index) => {
          const key = itemKey(item, index);
          const name = `\u2068${itemName(item, index)}\u2069`;
          const marked = drag && drag.over === index && drag.from !== index ? drag.side : undefined;
          return (
            <li
              key={key}
              className={styles.item}
              data-sortable-key={key}
              data-dragging={drag?.from === index || undefined}
              data-drop={marked}
              onDragOver={(e) => over(e, index)}
              onDrop={drop}
            >
              <div className={styles.rail}>
                <span className={styles.position} aria-hidden="true">
                  {f.number(index + 1)}
                </span>
                <button
                  type="button"
                  className={styles.handle}
                  draggable={!disabled}
                  disabled={disabled}
                  aria-label={t("component.sortable.handle", { name, position: f.number(index + 1), count: f.number(count) })}
                  aria-describedby={hintId}
                  title={t("component.sortable.hint")}
                  data-sortable-control="handle"
                  onKeyDown={(e) => keys(e, index)}
                  onDragStart={(e) => {
                    e.dataTransfer.effectAllowed = "move";
                    e.dataTransfer.setData("text/plain", key);
                    const li = e.currentTarget.closest("li");
                    if (li) e.dataTransfer.setDragImage(li, 16, 16);
                    setDrag({ from: index, over: null, side: "before" });
                  }}
                  onDragEnd={() => setDrag(null)}
                >
                  <span className={styles.glyph} aria-hidden="true">
                    {GripGlyph}
                  </span>
                </button>
                <IconButton
                  label={t("component.sortable.up", { name })}
                  icon={UpGlyph}
                  disabled={disabled || index === 0}
                  onClick={() => move(index, index - 1, "up")}
                  data-sortable-control="up"
                />
                <IconButton
                  label={t("component.sortable.down", { name })}
                  icon={DownGlyph}
                  disabled={disabled || index === count - 1}
                  onClick={() => move(index, index + 1, "down")}
                  data-sortable-control="down"
                />
              </div>
              <div className={styles.body}>{renderItem(item, index)}</div>
            </li>
          );
        })}
        {fixedEnd ? (
          <li className={styles.item} data-sortable-fixed="">
            <div className={styles.rail}>
              <span className={styles.position} aria-hidden="true">
                {f.number(count + 1)}
              </span>
            </div>
            <div className={styles.body}>{fixedEnd}</div>
          </li>
        ) : null}
      </ol>
      <p id={hintId} className={styles.hint}>
        {t("component.sortable.hint")}
      </p>
      <p className={styles.said} role="status" aria-live="polite" data-sortable-said="">
        {said}
      </p>
    </div>
  );
}

/** A sortable list's loading state, the shape of its items so nothing shifts. */
export function SortableListSkeleton({ rows = 3 }: { rows?: number }) {
  return (
    <ol className={styles.list} aria-hidden="true">
      {Array.from({ length: rows }, (_, i) => (
        <li key={i} className={styles.item}>
          <div className={styles.rail} />
          <div className={styles.body}>
            <Skeleton shape="title" size="m" />
            <Skeleton size="full" />
            <Skeleton size="l" />
          </div>
        </li>
      ))}
    </ol>
  );
}
