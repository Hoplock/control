// COMBOBOX AND COMMAND PALETTE (DESIGN.md §2, §7, §8).
//
// The combobox is the ARIA 1.2 pattern: focus stays in the input, the arrows
// move the active option (aria-activedescendant), Enter chooses it, Escape
// closes the list. The command palette is a combobox in a modal dialog,
// opened from anywhere with ⌘K / Ctrl-K: navigation and preferences by name,
// so the keyboard is a path through the console rather than an affordance.

import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useI18n } from "../i18n/I18n";
import { KeyboardHint } from "./Badge";
import styles from "./Combobox.module.css";

export interface Option {
  id: string;
  label: string;
  /** Shown after the label, quieter: a group, a key. */
  hint?: ReactNode;
}

function matches(o: Option, query: string, locale: string): boolean {
  const q = query.trim().toLocaleLowerCase(locale);
  return q === "" || o.label.toLocaleLowerCase(locale).includes(q);
}

export function Combobox({
  label,
  options,
  onChoose,
  placeholder,
  alwaysOpen = false,
  autoFocus = false,
  emptyText,
}: {
  label: string;
  options: readonly Option[];
  onChoose(o: Option): void;
  placeholder?: string;
  /** The palette's list is always shown; a field's opens on input. */
  alwaysOpen?: boolean;
  autoFocus?: boolean;
  emptyText: string;
}) {
  const { locale } = useI18n();
  const id = useId();
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(alwaysOpen);
  const [active, setActive] = useState(0);
  const shown = useMemo(() => options.filter((o) => matches(o, query, locale)), [options, query, locale]);
  useEffect(() => setActive(0), [query]);
  const listRef = useRef<HTMLUListElement>(null);
  useEffect(() => {
    listRef.current?.querySelector(`[data-index="${active}"]`)?.scrollIntoView({ block: "nearest" });
  }, [active]);
  const choose = (o: Option | undefined) => {
    if (!o) return;
    onChoose(o);
    if (!alwaysOpen) setOpen(false);
  };
  const keys = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setOpen(true);
      setActive((a) => Math.min(a + 1, shown.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      choose(shown[active]);
    } else if (e.key === "Escape" && !alwaysOpen && open) {
      e.preventDefault();
      setOpen(false);
    }
  };
  const expanded = alwaysOpen || open;
  return (
    <div className={styles.combo}>
      <label htmlFor={`${id}-input`} className={styles.label}>
        {label}
      </label>
      <input
        id={`${id}-input`}
        className={styles.input}
        role="combobox"
        aria-expanded={expanded && shown.length > 0}
        aria-controls={`${id}-list`}
        aria-autocomplete="list"
        aria-activedescendant={expanded && shown[active] ? `${id}-opt-${shown[active]?.id}` : undefined}
        autoComplete="off"
        spellCheck={false}
        autoFocus={autoFocus}
        placeholder={placeholder}
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={keys}
      />
      {expanded && shown.length === 0 ? (
        <p className={styles.none} role="status">
          {emptyText}
        </p>
      ) : null}
      {expanded && shown.length > 0 ? (
        <ul ref={listRef} id={`${id}-list`} role="listbox" aria-label={label} className={styles.list}>
          {shown.map((o, i) => (
            <li
              key={o.id}
              id={`${id}-opt-${o.id}`}
              role="option"
              aria-selected={i === active}
              data-index={i}
              className={styles.option}
              onMouseMove={() => setActive(i)}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => choose(o)}
            >
              <span>{o.label}</span>
              {o.hint ? <span className={styles.hint}>{o.hint}</span> : null}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

/** The command palette's dialog, opened with ⌘K / Ctrl-K from anywhere. */
export function CommandPalette({ commands, onRun }: { commands: readonly Option[]; onRun(o: Option): void }) {
  const { t } = useI18n();
  const ref = useRef<HTMLDialogElement>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    const closed = () => setOpen(false);
    d.addEventListener("close", closed);
    return () => d.removeEventListener("close", closed);
  }, []);
  return (
    <>
      <button type="button" className={styles.opener} onClick={() => setOpen(true)} aria-keyshortcuts="Control+K Meta+K">
        <span>{t("palette.open")}</span>
        <KeyboardHint keys={[t("palette.key.ctrl"), "K"]} />
      </button>
      <dialog ref={ref} className={styles.palette} aria-label={t("palette.title")}>
        {open ? (
          <Combobox
            label={t("palette.title")}
            options={commands}
            alwaysOpen
            autoFocus
            emptyText={t("palette.none")}
            onChoose={(o) => {
              setOpen(false);
              onRun(o);
            }}
          />
        ) : null}
      </dialog>
    </>
  );
}
