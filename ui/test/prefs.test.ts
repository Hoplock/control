// The console is dark first (0052, DESIGN.md §8): with nothing remembered it
// opens dark, and an operator's own choice — light, or following the
// system — is kept as they made it.

import { afterEach, describe, expect, it } from "vitest";
import { DEFAULT_PREFS, loadPrefs, savePrefs } from "../src/prefs/prefs";

function storage(initial: Record<string, string> = {}) {
  const items = new Map(Object.entries(initial));
  const localStorage = {
    getItem: (k: string) => items.get(k) ?? null,
    setItem: (k: string, v: string) => void items.set(k, v),
  };
  (globalThis as { window?: unknown }).window = { localStorage };
}

afterEach(() => {
  delete (globalThis as { window?: unknown }).window;
});

describe("the theme an operator sees", () => {
  it("is dark when nothing is remembered", () => {
    storage();
    expect(DEFAULT_PREFS.theme).toBe("dark");
    expect(loadPrefs().theme).toBe("dark");
    expect(loadPrefs("subject:ada").theme).toBe("dark");
  });

  it("is dark when what is remembered is not a theme", () => {
    storage({ "hoplock.console.prefs": JSON.stringify({ last: { theme: "sepia", density: "compact" } }) });
    expect(loadPrefs()).toEqual({ theme: "dark", density: "compact" });
  });

  it("keeps an operator's choice, following the system included", () => {
    storage();
    for (const theme of ["system", "light", "dark"] as const) {
      savePrefs("subject:ada", { theme, density: "screen" });
      expect(loadPrefs("subject:ada").theme).toBe(theme);
      expect(loadPrefs().theme).toBe(theme);
    }
  });

  it("is dark when storage cannot be read", () => {
    (globalThis as { window?: unknown }).window = {
      localStorage: {
        getItem: () => {
          throw new Error("denied");
        },
      },
    };
    expect(loadPrefs().theme).toBe("dark");
  });
});
