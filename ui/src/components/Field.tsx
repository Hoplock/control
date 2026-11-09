// FORM CONTROLS (DESIGN.md §7): input, textarea, select, checkbox, radio,
// switch, and the Field that labels one. Every control is the native element,
// so the browser's keyboard behaviour and accessibility are the console's; a
// Field gives it a visible label, a hint, and an error that is reported at
// the field (§7: a failed mutation reports inline, never by a toast alone),
// tied to the control with aria-describedby and aria-invalid.

import {
  cloneElement,
  forwardRef,
  isValidElement,
  useId,
  type InputHTMLAttributes,
  type ReactElement,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react";
import styles from "./Field.module.css";

interface ControlProps {
  id?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
}

export function Field({
  label,
  hint,
  error,
  children,
}: {
  label: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  children: ReactElement<ControlProps>;
}) {
  const id = useId();
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const control = isValidElement(children)
    ? cloneElement(children, {
        id: children.props.id ?? id,
        "aria-describedby": [hintId, errorId].filter(Boolean).join(" ") || undefined,
        "aria-invalid": error ? true : undefined,
      })
    : children;
  return (
    <div className={styles.field} data-invalid={error ? true : undefined}>
      <label className={styles.label} htmlFor={children.props.id ?? id}>
        {label}
      </label>
      {control}
      {hint ? (
        <p id={hintId} className={styles.hint}>
          {hint}
        </p>
      ) : null}
      {error ? (
        <p id={errorId} className={styles.error}>
          {error}
        </p>
      ) : null}
    </div>
  );
}

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement> & { mono?: boolean }>(
  function Input({ className, mono, ...rest }, ref) {
    return (
      <input
        ref={ref}
        className={[styles.control, mono ? styles.mono : "", className].filter(Boolean).join(" ")}
        dir={mono ? "ltr" : undefined}
        {...rest}
      />
    );
  },
);

/** A textarea; `mono` is for data typed as written — policy source (0053) — so it is LTR, mono and unchecked for spelling. */
export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement> & { mono?: boolean }>(
  function Textarea({ className, mono, ...rest }, ref) {
    return (
      <textarea
        ref={ref}
        className={[styles.control, styles.textarea, mono ? styles.mono : "", className].filter(Boolean).join(" ")}
        dir={mono ? "ltr" : undefined}
        spellCheck={mono ? false : undefined}
        translate={mono ? "no" : undefined}
        {...rest}
      />
    );
  },
);

export const Select = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement>>(function Select(
  { className, children, ...rest },
  ref,
) {
  return (
    <select ref={ref} className={[styles.control, styles.select, className].filter(Boolean).join(" ")} {...rest}>
      {children}
    </select>
  );
});

type CheckProps = Omit<InputHTMLAttributes<HTMLInputElement>, "type"> & { label: ReactNode };

export const Checkbox = forwardRef<HTMLInputElement, CheckProps>(function Checkbox({ label, className, ...rest }, ref) {
  return (
    <label className={[styles.check, className].filter(Boolean).join(" ")}>
      <input ref={ref} type="checkbox" className={styles.box} {...rest} />
      <span>{label}</span>
    </label>
  );
});

export const Radio = forwardRef<HTMLInputElement, CheckProps>(function Radio({ label, className, ...rest }, ref) {
  return (
    <label className={[styles.check, className].filter(Boolean).join(" ")}>
      <input ref={ref} type="radio" className={[styles.box, styles.radio].join(" ")} {...rest} />
      <span>{label}</span>
    </label>
  );
});

/** A switch is a checkbox announced as one: role="switch", on or off. */
export const Switch = forwardRef<HTMLInputElement, CheckProps>(function Switch({ label, className, ...rest }, ref) {
  return (
    <label className={[styles.check, className].filter(Boolean).join(" ")}>
      <input ref={ref} type="checkbox" role="switch" className={styles.switch} {...rest} />
      <span>{label}</span>
    </label>
  );
});

/** A group of radios or checkboxes, with its legend. */
export function Choices({ legend, children }: { legend: ReactNode; children: ReactNode }) {
  return (
    <fieldset className={styles.choices}>
      <legend className={styles.label}>{legend}</legend>
      {children}
    </fieldset>
  );
}
