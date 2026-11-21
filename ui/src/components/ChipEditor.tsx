// CHIP EDITOR (DESIGN.md §7, §8; 0066): a list of values edited as chips —
// each a removable chip, and one control that adds another. Bound to a closed
// set (`options`), the control is a choice of the members not yet chosen, so
// a value outside the set cannot be typed at all; without one, it is typed
// text, checked (`check`) before it is added, and refused at the field in the
// caller's words. The values are data — an operator's group, a label, a
// hostname — so every chip is mono, LTR and untranslated (§9). Adding and
// removing are announced, and removing a chip moves focus to the control
// rather than losing it.

import { useId, useRef, useState, type ReactNode } from "react";
import { useI18n } from "../i18n/I18n";
import { Chip } from "./Badge";
import { Button } from "./Button";
import { Input, Select } from "./Field";
import { VisuallyHidden } from "./VisuallyHidden";
import styles from "./ChipEditor.module.css";

export function ChipEditor({
  label,
  values,
  options,
  onChange,
  check,
  error,
  hint,
  disabled = false,
  name,
}: {
  /** The field's label: what the values are. */
  label: string;
  values: readonly string[];
  /** The closed set values are chosen from. Without it, values are typed. */
  options?: readonly string[];
  onChange(values: string[]): void;
  /** For typed values: why one cannot be added, or null. */
  check?(value: string): ReactNode | null;
  /** A refusal of the values as they stand, from whoever checks them (the server). */
  error?: ReactNode;
  hint?: ReactNode;
  disabled?: boolean;
  /** Names the editor for tests and deep links: `data-chip-editor`. */
  name?: string;
}) {
  const { t } = useI18n();
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const select = useRef<HTMLSelectElement>(null);
  const [draft, setDraft] = useState("");
  const [refusal, setRefusal] = useState<ReactNode | null>(null);
  const [said, setSaid] = useState("");
  const remaining = options ? options.filter((o) => !values.includes(o)) : [];
  const shownError = refusal ?? error;
  const errorId = shownError ? `${id}-error` : undefined;
  const hintId = hint ? `${id}-hint` : undefined;

  const add = (v: string) => {
    const value = options ? v : v.trim();
    if (value === "") return;
    if (values.includes(value)) {
      setRefusal(t("component.chips.duplicate", { value: `\u2068${value}\u2069` }));
      return;
    }
    const refused = check?.(value) ?? null;
    if (refused) {
      setRefusal(refused);
      return;
    }
    setRefusal(null);
    setDraft("");
    setSaid(t("component.chips.added", { value: `\u2068${value}\u2069`, label }));
    onChange([...values, value]);
  };

  const remove = (value: string) => {
    setSaid(t("component.chips.removed", { value: `\u2068${value}\u2069`, label }));
    onChange(values.filter((v) => v !== value));
    window.setTimeout(() => (select.current ?? input.current)?.focus(), 0);
  };

  return (
    <fieldset className={styles.editor} data-chip-editor={name ?? ""} data-invalid={shownError ? true : undefined} disabled={disabled}>
      <legend className={styles.label}>{label}</legend>
      {values.length > 0 ? (
        <ul className={styles.chips} aria-label={label}>
          {values.map((v) => (
            <li key={v} data-chip={v}>
              <Chip data onRemove={disabled ? undefined : () => remove(v)} removeLabel={t("component.chips.remove", { value: `\u2068${v}\u2069`, label })}>
                {v}
              </Chip>
            </li>
          ))}
        </ul>
      ) : (
        <p className={styles.none}>{t("component.chips.none")}</p>
      )}
      <div className={styles.add}>
        {options ? (
          <Select
            ref={select}
            value=""
            disabled={disabled || remaining.length === 0}
            aria-label={t("component.chips.add", { label })}
            aria-describedby={[hintId, errorId].filter(Boolean).join(" ") || undefined}
            aria-invalid={shownError ? true : undefined}
            onChange={(e) => add(e.target.value)}
            data-chip-add=""
          >
            <option value="">{remaining.length ? t("component.chips.choose") : t("component.chips.allChosen")}</option>
            {remaining.map((o) => (
              <option key={o} value={o} translate="no">
                {o}
              </option>
            ))}
          </Select>
        ) : (
          <>
            <Input
              ref={input}
              mono
              value={draft}
              disabled={disabled}
              spellCheck={false}
              aria-label={t("component.chips.add", { label })}
              aria-describedby={[hintId, errorId].filter(Boolean).join(" ") || undefined}
              aria-invalid={shownError ? true : undefined}
              onChange={(e) => {
                setDraft(e.target.value);
                setRefusal(null);
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  add(draft);
                }
              }}
              data-chip-add=""
            />
            <Button onClick={() => add(draft)} disabled={disabled || draft.trim() === ""} data-chip-add-button="">
              {t("component.chips.addButton")}
            </Button>
          </>
        )}
      </div>
      {hint ? (
        <p id={hintId} className={styles.hint}>
          {hint}
        </p>
      ) : null}
      {shownError ? (
        <p id={errorId} className={styles.error} data-chip-error="">
          {shownError}
        </p>
      ) : null}
      <VisuallyHidden>
        <span role="status" aria-live="polite">
          {said}
        </span>
      </VisuallyHidden>
    </fieldset>
  );
}
