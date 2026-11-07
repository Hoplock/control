// The operator's display preferences (DESIGN.md §3.7, §8): theme and density,
// remembered per operator (../prefs). At the foot of the navigation, so the
// top bar carries identity and nothing that competes with it.

import { useI18n } from "../i18n/I18n";
import type { Density, Prefs, Theme } from "../prefs/prefs";
import styles from "./Preferences.module.css";

const THEMES: readonly Theme[] = ["system", "light", "dark"];
const DENSITIES: readonly Density[] = ["screen", "compact", "comfortable"];

export function Preferences({ prefs, onPrefs }: { prefs: Prefs; onPrefs(prefs: Prefs): void }) {
  const { t } = useI18n();
  return (
    <div className={styles.prefs}>
      <label className={styles.field}>
        <span className={styles.label}>{t("prefs.theme.label")}</span>
        <select
          className={styles.select}
          value={prefs.theme}
          onChange={(e) => onPrefs({ ...prefs, theme: e.currentTarget.value as Theme })}
        >
          {THEMES.map((theme) => (
            <option key={theme} value={theme}>
              {t(theme === "system" ? "prefs.theme.system" : theme === "light" ? "prefs.theme.light" : "prefs.theme.dark")}
            </option>
          ))}
        </select>
      </label>
      <label className={styles.field}>
        <span className={styles.label}>{t("prefs.density.label")}</span>
        <select
          className={styles.select}
          value={prefs.density}
          onChange={(e) => onPrefs({ ...prefs, density: e.currentTarget.value as Density })}
        >
          {DENSITIES.map((d) => (
            <option key={d} value={d}>
              {t(d === "screen" ? "prefs.density.screen" : d === "compact" ? "prefs.density.compact" : "prefs.density.comfortable")}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}
