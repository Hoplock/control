// The navigation (DESIGN.md §4): fixed, labelled — never icon-only — and
// listing only the screens that exist (../screens/registry).

import { useI18n } from "../i18n/I18n";
import { Link, match } from "../router/router";
import { SCREENS } from "../screens/registry";
import styles from "./Nav.module.css";

export function Nav({ path, open, onNavigate }: { path: string; open: boolean; onNavigate(): void }) {
  const { t } = useI18n();
  return (
    <nav id="console-nav" className={styles.nav} data-open={open || undefined} aria-label={t("shell.nav.label")}>
      <p className={styles.product}>{t("app.name")}</p>
      <ul className={styles.list}>
        {SCREENS.filter((s) => s.nav).map((s) => {
          const active = match(s.path, path) !== null || path.startsWith(s.path + "/");
          return (
            <li key={s.id}>
              <Link
                to={s.path}
                className={styles.item}
                aria-current={active ? "page" : undefined}
                onClick={onNavigate}
              >
                {t(s.nav as NonNullable<typeof s.nav>)}
              </Link>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
