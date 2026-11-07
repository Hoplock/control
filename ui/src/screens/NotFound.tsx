import { useI18n } from "../i18n/I18n";
import { consoleHref } from "../router/paths";
import { Link } from "../router/router";
import { HOME } from "./registry";
import styles from "./Screen.module.css";

/** A path inside /console/ that names no screen: still the console's page. */
export function NotFound({ path }: { path: string }) {
  const { t } = useI18n();
  return (
    <section className={styles.screen} aria-labelledby="not-found-title">
      <h1 id="not-found-title" className={styles.title}>
        {t("notFound.title")}
      </h1>
      <p className={styles.lede}>{t("notFound.body", { path: consoleHref(path) })}</p>
      <p>
        <Link to={HOME} className={styles.link}>
          {t("notFound.home")}
        </Link>
      </p>
    </section>
  );
}
