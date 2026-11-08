// BUTTONS (DESIGN.md §7). Four variants and one rule: the dangerous action
// looks dangerous and the reversible one does not (§2), so `danger` is for
// what cannot be undone and nothing else. A loading button keeps its label
// and its width, so nothing shifts while it waits, and it is announced busy.
// An icon-only button is IconButton, which cannot be built without a name.

import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from "react";
import styles from "./Button.module.css";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  /** Waiting on the action it started: disabled, busy, label kept. */
  loading?: boolean;
  /** A glyph before the label, decorative. */
  icon?: ReactNode;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "secondary", loading = false, icon, children, disabled, type = "button", className, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={[styles.button, className].filter(Boolean).join(" ")}
      data-variant={variant}
      data-loading={loading || undefined}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      {...rest}
    >
      {icon ? (
        <span className={styles.icon} aria-hidden="true">
          {icon}
        </span>
      ) : null}
      <span className={styles.label}>{children}</span>
      {loading ? <span className={styles.busy} aria-hidden="true" /> : null}
    </button>
  );
});

export interface IconButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "aria-label" | "children"> {
  /** The accessible name: required, because the button has no visible text. */
  label: string;
  icon: ReactNode;
  variant?: Exclude<ButtonVariant, "primary">;
}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { label, icon, variant = "ghost", type = "button", className, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={[styles.button, styles.iconOnly, className].filter(Boolean).join(" ")}
      data-variant={variant}
      aria-label={label}
      title={label}
      {...rest}
    >
      <span className={styles.icon} aria-hidden="true">
        {icon}
      </span>
    </button>
  );
});
