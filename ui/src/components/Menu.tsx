// DROPDOWN MENU AND TOOLTIP (DESIGN.md §7, §8).
//
// The menu is a native popover in the top layer, so a table's scroll
// container never clips it, and anchored to the button that opened it. The
// browser gives it light dismiss and Escape, and returns focus to the button;
// this component gives it the menu pattern's keys — the arrows, Home and End
// move between items, and the first item has focus on open. Items may be
// actions, checkboxes, or groups of choices (menuitemradio), each group
// labelled and ruled off.

import { useEffect, useId, useRef, type KeyboardEvent, type ReactNode } from "react";
import styles from "./Menu.module.css";

export interface MenuItem {
  id: string;
  label: string;
  onSelect(): void;
  /** Destructive items are styled as such and sit last. */
  danger?: boolean;
  disabled?: boolean;
  /** A checkable item (column visibility): announced as menuitemcheckbox. */
  checked?: boolean;
  /**
   * `radio`: one of a group of choices (a theme, a density), announced as
   * menuitemradio, `checked` saying which. Choosing one keeps the menu open,
   * so a second choice is one more key, as with a checkbox.
   */
  kind?: "radio";
  /** Consecutive items with one group are a labelled group, ruled off from the rest. */
  group?: string;
}

/** Consecutive items sharing a group, in order. */
function groups(items: readonly MenuItem[]): { label?: string; items: MenuItem[] }[] {
  const out: { label?: string; items: MenuItem[] }[] = [];
  for (const item of items) {
    const last = out[out.length - 1];
    if (last && last.label === item.group) last.items.push(item);
    else out.push({ label: item.group, items: [item] });
  }
  return out;
}

export function Menu({
  label,
  trigger,
  items,
  triggerLabel,
  heading,
  variant = "button",
}: {
  /** The menu's accessible name. */
  label: string;
  /** What the button shows. */
  trigger: ReactNode;
  /** The button's accessible name, when its content is a glyph. */
  triggerLabel?: string;
  items: readonly MenuItem[];
  /** A line of text atop the menu, read with it: who the account menu is for. */
  heading?: ReactNode;
  /** `quiet`: a borderless trigger, for a menu that sits in the chrome. */
  variant?: "button" | "quiet";
}) {
  const id = useId();
  const menu = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const m = menu.current;
    if (!m) return;
    const toggled = (e: Event) => {
      const open = (e as ToggleEvent).newState === "open";
      button.current?.setAttribute("aria-expanded", String(open));
      if (open) m.querySelector<HTMLElement>("[role^=menuitem]:not([disabled])")?.focus();
    };
    m.addEventListener("toggle", toggled);
    return () => m.removeEventListener("toggle", toggled);
  }, []);
  const keys = (e: KeyboardEvent<HTMLDivElement>) => {
    const all = Array.from(menu.current?.querySelectorAll<HTMLElement>("[role^=menuitem]:not([disabled])") ?? []);
    const at = all.indexOf(document.activeElement as HTMLElement);
    let next = -1;
    if (e.key === "ArrowDown") next = (at + 1) % all.length;
    else if (e.key === "ArrowUp") next = (at - 1 + all.length) % all.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = all.length - 1;
    if (next >= 0) {
      e.preventDefault();
      all[next]?.focus();
    }
  };
  return (
    <>
      <button
        ref={button}
        type="button"
        className={styles.trigger}
        data-variant={variant}
        popoverTarget={id}
        aria-haspopup="menu"
        aria-expanded="false"
        aria-label={triggerLabel}
      >
        {trigger}
      </button>
      <div ref={menu} id={id} popover="auto" role="menu" aria-label={label} className={styles.menu} onKeyDown={keys}>
        {heading ? (
          <div className={styles.heading} aria-hidden="true">
            {heading}
          </div>
        ) : null}
        {groups(items).map((g, i) => {
          const buttons = g.items.map((item) => (
            <button
              key={item.id}
              type="button"
              role={item.kind === "radio" ? "menuitemradio" : item.checked === undefined ? "menuitem" : "menuitemcheckbox"}
              aria-checked={item.kind === "radio" ? Boolean(item.checked) : item.checked}
              className={styles.item}
              data-danger={item.danger || undefined}
              disabled={item.disabled}
              tabIndex={-1}
              onClick={() => {
                if (item.checked === undefined && item.kind !== "radio") menu.current?.hidePopover();
                item.onSelect();
              }}
            >
              <span className={styles.check} aria-hidden="true">
                {item.checked && item.kind === "radio" ? (
                  <svg viewBox="0 0 12 12">
                    <circle cx="6" cy="6" r="2.5" />
                  </svg>
                ) : item.checked ? (
                  <svg viewBox="0 0 12 12">
                    <path d="M2.5 6.5 5 9l4.5-6" />
                  </svg>
                ) : null}
              </span>
              <span>{item.label}</span>
            </button>
          ));
          return (
            <div key={g.label ?? `g${i}`} role={g.label ? "group" : undefined} aria-label={g.label} className={styles.group}>
              {g.label ? (
                <div className={styles.groupLabel} aria-hidden="true">
                  {g.label}
                </div>
              ) : null}
              {buttons}
            </div>
          );
        })}
      </div>
    </>
  );
}

/** A tooltip: a short description of its trigger, on hover and on focus. */
export function Tooltip({ text, children }: { text: string; children(describedBy: string): ReactNode }) {
  const id = useId();
  return (
    <span className={styles.tipWrap}>
      {children(id)}
      <span id={id} role="tooltip" className={styles.tip}>
        {text}
      </span>
    </span>
  );
}
