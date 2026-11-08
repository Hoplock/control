// THE FILTER BAR (DESIGN.md §4, §7): each filter is a field with a value, the
// active ones are chips that remove themselves, and the whole state is the
// screen's URL under /console/ — a filtered view is a link someone can paste
// into an incident channel. Saved views are named URLs, kept per operator in
// this browser. The bar changes nothing but the query: the screen reads its
// filters back out of the location, so the URL is the one source of truth.

import { useId, type ReactNode } from "react";
import { useI18n } from "../i18n/I18n";
import { Chip } from "./Badge";
import { Input, Select } from "./Field";
import { Menu } from "./Menu";
import styles from "./FilterBar.module.css";

export interface FilterField {
  id: string;
  label: string;
  options: readonly { value: string; label: string }[];
}

export interface SavedView {
  name: string;
  query: string;
}

const ViewsGlyph = (
  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
    <path d="M4 2.5h8v11l-4-3-4 3z" />
  </svg>
);

export function FilterBar({
  label,
  fields,
  values,
  onChange,
  search,
  views,
  extra,
}: {
  label: string;
  fields: readonly FilterField[];
  values: Readonly<Record<string, string>>;
  onChange(values: Record<string, string>): void;
  search?: { label: string; value: string; onChange(v: string): void };
  views?: { saved: readonly SavedView[]; onApply(v: SavedView): void; onSave(): void; onForget(): void };
  extra?: ReactNode;
}) {
  const { t } = useI18n();
  const id = useId();
  const set = (field: string, value: string) => {
    const next = { ...values };
    if (value) next[field] = value;
    else delete next[field];
    onChange(next);
  };
  const active = fields.filter((f) => values[f.id]);
  return (
    <div className={styles.bar} role="search" aria-label={label}>
      <div className={styles.controls}>
        {search ? (
          <span className={styles.control}>
            <label htmlFor={`${id}-search`} className={styles.label}>
              {search.label}
            </label>
            <Input
              id={`${id}-search`}
              type="search"
              value={search.value}
              onChange={(e) => search.onChange(e.target.value)}
              className={styles.search}
            />
          </span>
        ) : null}
        {fields.map((f) => (
          <span key={f.id} className={styles.control}>
            <label htmlFor={`${id}-${f.id}`} className={styles.label}>
              {f.label}
            </label>
            <Select id={`${id}-${f.id}`} value={values[f.id] ?? ""} onChange={(e) => set(f.id, e.target.value)}>
              <option value="">{t("component.filter.any")}</option>
              {f.options.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </Select>
          </span>
        ))}
        {views ? (
          <span className={styles.views}>
            <Menu
              label={t("component.filter.views")}
              triggerLabel={t("component.filter.views")}
              trigger={ViewsGlyph}
              items={[
                ...views.saved.map((v) => ({ id: `view-${v.name}`, label: v.name, onSelect: () => views.onApply(v) })),
                { id: "save", label: t("component.filter.saveView"), onSelect: views.onSave },
                ...(views.saved.length
                  ? [{ id: "forget", label: t("component.filter.forgetViews"), onSelect: views.onForget }]
                  : []),
              ]}
            />
          </span>
        ) : null}
        {extra}
      </div>
      {active.length ? (
        <ul className={styles.chips} aria-label={t("component.filter.active")}>
          {active.map((f) => {
            const value = values[f.id] as string;
            const option = f.options.find((o) => o.value === value);
            return (
              <li key={f.id}>
                <Chip
                  onRemove={() => set(f.id, "")}
                  removeLabel={t("component.filter.remove", { field: f.label, value: option?.label ?? value })}
                >
                  {t("component.filter.chip", { field: f.label, value: option?.label ?? value })}
                </Chip>
              </li>
            );
          })}
          <li>
            <button type="button" className={styles.clear} onClick={() => onChange({})}>
              {t("component.filter.clear")}
            </button>
          </li>
        </ul>
      ) : null}
    </div>
  );
}
