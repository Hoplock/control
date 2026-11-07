// The state of one read, as the chrome and a screen hold it. Refused and
// outage stay apart all the way to the pixels (M11).

import { useCallback, useEffect, useRef, useState } from "react";
import type { Answer } from "../api/client";
import type { ApiError } from "../i18n/errors";

export type Load<T> =
  | { state: "loading" }
  | { state: "ok"; data: T }
  | { state: "refused"; status: number; error: ApiError }
  | { state: "outage"; error: ApiError };

export function fromAnswer<T>(a: Answer<T>): Load<T> {
  switch (a.kind) {
    case "ok":
      return { state: "ok", data: a.data };
    case "refused":
      return { state: "refused", status: a.status, error: a.error };
    case "outage":
      return { state: "outage", error: a.error };
  }
}

/**
 * Runs a read whenever its key changes, and again on refresh or every
 * `every` milliseconds. A refresh keeps the last answer on screen until the
 * next arrives, so nothing flashes empty (DESIGN.md §1).
 */
export function useLoad<T>(
  key: string | null,
  read: (signal: AbortSignal) => Promise<Answer<T>>,
  every?: number,
): [Load<T>, () => void] {
  const [load, setLoad] = useState<Load<T>>({ state: "loading" });
  const [tick, setTick] = useState(0);
  const readRef = useRef(read);
  readRef.current = read;
  const lastKey = useRef<string | null>(null);

  useEffect(() => {
    if (key === null) return;
    if (lastKey.current !== key) {
      lastKey.current = key;
      setLoad({ state: "loading" });
    }
    const abort = new AbortController();
    readRef.current(abort.signal).then(
      (a) => {
        if (!abort.signal.aborted) setLoad(fromAnswer(a));
      },
      () => {
        // Aborted: a newer read owns the state.
      },
    );
    return () => abort.abort();
  }, [key, tick]);

  useEffect(() => {
    if (key === null || !every) return;
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") setTick((n) => n + 1);
    }, every);
    return () => window.clearInterval(id);
  }, [key, every]);

  const refresh = useCallback(() => setTick((n) => n + 1), []);
  return [load, refresh];
}
