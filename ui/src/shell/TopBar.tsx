// The top bar (DESIGN.md §4, §5): which deployment this is, before anything
// else on the page — its name, its short instance id in mono and its health,
// on a chip in the deployment's configured stripe — then the tenant where
// more than one is in scope, a badge for each supervisor, a break-glass
// session said out loud, the display preferences, and the operator.
//
// Presentational: every fact arrives as a prop, so each rule is asserted by
// rendering it (test/chrome.test.tsx).

import type { Health, Identity, Session, Supervisor } from "../api/types";
import { Ident, shortInstanceId } from "../components/Ident";
import { Status } from "../components/Status";
import { useI18n } from "../i18n/I18n";
import type { Density, Prefs, Theme } from "../prefs/prefs";
import { Link } from "../router/router";
import { healthStatus, showsTenant } from "./chrome";
import type { Load } from "./load";
import styles from "./TopBar.module.css";

export interface TopBarProps {
  session: Session;
  identity: Load<Identity>;
  health: Load<Health>;
  tenants: readonly string[];
  tenant: string | null;
  supervisors: readonly Supervisor[];
  prefs: Prefs;
  navOpen: boolean;
  onNav(open: boolean): void;
  onTenant(tenant: string): void;
  onPrefs(prefs: Prefs): void;
  onSignOut(): void;
}

const THEMES: readonly Theme[] = ["system", "light", "dark"];
const DENSITIES: readonly Density[] = ["screen", "compact", "comfortable"];

export function TopBar(p: TopBarProps) {
  const { t, f } = useI18n();
  const status = healthStatus(p.health);
  const id = p.identity.state === "ok" ? p.identity.data : null;
  const name = id ? id.display_name || t("chrome.deployment.unnamed") : null;
  const members = p.health.state === "ok" ? p.health.data.nodes.members.filter((m) => m.member) : [];
  const versions = Array.from(new Set(members.map((m) => m.version))).sort();
  const operator = p.session.display_name || p.session.subject || p.session.principal_id;

  return (
    <header className={styles.bar}>
      <button
        type="button"
        className={styles.menu}
        aria-expanded={p.navOpen}
        aria-controls="console-nav"
        onClick={() => p.onNav(!p.navOpen)}
      >
        {p.navOpen ? t("shell.nav.close") : t("shell.nav.open")}
      </button>

      <div className={styles.deployment}>
        {id && name ? (
          <Link
            to="deployment"
            className={styles.chip}
            aria-label={t("chrome.deployment.open", { name, id: id.instance_id, state: t(status.label) })}
            aria-describedby="deployment-details"
          >
            <span className={styles.name}>{name}</span>
            <Ident value={id.instance_id} short={shortInstanceId(id.instance_id)} />
            <Status tone={status.tone} label={t(status.label)} />
          </Link>
        ) : (
          <span className={styles.chip} data-unread={p.identity.state !== "loading" || undefined}>
            {p.identity.state === "loading" ? null : (
              <>
                <span className={styles.name}>{t("chrome.deployment.unread")}</span>
                <Status tone={status.tone} label={t(status.label)} />
              </>
            )}
          </span>
        )}
        {id ? (
          <div id="deployment-details" role="tooltip" className={styles.details}>
            <p>{t("chrome.deployment.version", { version: id.versions.software })}</p>
            <p>{t("chrome.deployment.contract", { contract: id.versions.contract })}</p>
            {members.length > 0 ? (
              <p>{t("chrome.deployment.nodes", { count: members.length, versions: f.list(versions) })}</p>
            ) : null}
          </div>
        ) : null}
      </div>

      {showsTenant(p.tenants) && p.tenant ? (
        <label className={styles.field} data-testid="tenant-switch">
          <span className={styles.fieldLabel}>{t("chrome.tenant.label")}</span>
          <select className={styles.select} value={p.tenant} onChange={(e) => p.onTenant(e.currentTarget.value)}>
            {p.tenants.map((tenant) => (
              <option key={tenant} value={tenant} translate="no">
                {tenant}
              </option>
            ))}
          </select>
        </label>
      ) : null}

      {p.supervisors.map((s) => (
        <span key={s.principal_id} className={styles.badge} data-tone="info" data-testid="supervisor-badge">
          {t("chrome.supervised", { name: s.display_name || s.principal_id })}
        </span>
      ))}

      {p.session.break_glass ? (
        <span className={styles.badge} data-tone="warn">
          {t("chrome.breakGlass")}
        </span>
      ) : null}

      <div className={styles.end}>
        <label className={styles.field}>
          <span className={styles.fieldLabel}>{t("prefs.theme.label")}</span>
          <select
            className={styles.select}
            value={p.prefs.theme}
            onChange={(e) => p.onPrefs({ ...p.prefs, theme: e.currentTarget.value as Theme })}
          >
            {THEMES.map((theme) => (
              <option key={theme} value={theme}>
                {t(theme === "system" ? "prefs.theme.system" : theme === "light" ? "prefs.theme.light" : "prefs.theme.dark")}
              </option>
            ))}
          </select>
        </label>
        <label className={styles.field}>
          <span className={styles.fieldLabel}>{t("prefs.density.label")}</span>
          <select
            className={styles.select}
            value={p.prefs.density}
            onChange={(e) => p.onPrefs({ ...p.prefs, density: e.currentTarget.value as Density })}
          >
            {DENSITIES.map((d) => (
              <option key={d} value={d}>
                {t(d === "screen" ? "prefs.density.screen" : d === "compact" ? "prefs.density.compact" : "prefs.density.comfortable")}
              </option>
            ))}
          </select>
        </label>
        <span className={styles.operator}>{t("chrome.account.label", { name: operator })}</span>
        <button type="button" className={styles.button} onClick={p.onSignOut}>
          {t("chrome.signOut")}
        </button>
      </div>
    </header>
  );
}
