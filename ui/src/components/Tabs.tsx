// TABS (DESIGN.md §7, §8): the tabs pattern with a roving tab stop. The arrow
// keys follow the reading direction, so in a right-to-left page the left
// arrow moves forward; Home and End go to the ends.

import { useId, useRef, type KeyboardEvent, type ReactNode } from "react";
import { useI18n } from "../i18n/I18n";
import styles from "./Tabs.module.css";

export interface Tab {
  id: string;
  label: ReactNode;
  disabled?: boolean;
}

export function Tabs({
  label,
  tabs,
  selected,
  onSelect,
  children,
}: {
  label: string;
  tabs: readonly Tab[];
  selected: string;
  onSelect(id: string): void;
  children: ReactNode;
}) {
  const { dir } = useI18n();
  const base = useId();
  const list = useRef<HTMLDivElement>(null);
  const enabled = tabs.filter((t) => !t.disabled);
  const keys = (e: KeyboardEvent) => {
    const at = enabled.findIndex((t) => t.id === selected);
    const forward = dir === "rtl" ? "ArrowLeft" : "ArrowRight";
    const back = dir === "rtl" ? "ArrowRight" : "ArrowLeft";
    let next = -1;
    if (e.key === forward) next = (at + 1) % enabled.length;
    else if (e.key === back) next = (at - 1 + enabled.length) % enabled.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = enabled.length - 1;
    if (next < 0) return;
    e.preventDefault();
    const id = (enabled[next] as Tab).id;
    onSelect(id);
    list.current?.querySelector<HTMLElement>(`[data-tab="${CSS.escape(id)}"]`)?.focus();
  };
  return (
    <div className={styles.tabs}>
      <div ref={list} role="tablist" aria-label={label} className={styles.list} onKeyDown={keys}>
        {tabs.map((tab) => (
          <button
            key={tab.id}
            type="button"
            role="tab"
            id={`${base}-${tab.id}`}
            data-tab={tab.id}
            aria-selected={tab.id === selected}
            aria-controls={`${base}-panel`}
            tabIndex={tab.id === selected ? 0 : -1}
            disabled={tab.disabled}
            className={styles.tab}
            onClick={() => onSelect(tab.id)}
          >
            {tab.label}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`${base}-panel`} aria-labelledby={`${base}-${selected}`} className={styles.panel}>
        {children}
      </div>
    </div>
  );
}
