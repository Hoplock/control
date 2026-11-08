// The top bar (DESIGN.md §4, §5): which deployment this is, before anything
// else on the page — its name, its short instance id in mono and its health,
// on a chip in the deployment's configured stripe — then the tenant where
// more than one is in scope, a badge for each supervisor (a link to the audit
// view filtered to what it did here, M19), a break-glass
// session said out loud, and the operator — whose name opens the account
// menu: the display preferences (theme and density, remembered per operator)
// and signing out. They hang off the operator's name because they are the
// operator's, and the bar stays identity.
//
// Presentational: every fact arrives as a prop, so each rule is asserted by
// rendering it (test/chrome.test.tsx).

import type { Health, Identity, Session, Supervisor } from "../api/types";
import { Avatar } from "../components/Badge";
import { Ident, shortInstanceId } from "../components/Ident";
import { Menu } from "../components/Menu";
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
  navOpen: boolean;
  onNav(open: boolean): void;
  onTenant(tenant: string): void;
  /** The operator's display preferences, which the account menu sets. */
  prefs: Prefs;
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
            aria-describedby="deployment-details"
          >
            <span className={styles.name} translate={id.display_name ? "no" : undefined}>
              {name}
            </span>
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
        <Link
          key={s.principal_id}
          to="audit/records"
          query={{ principal: s.principal_id }}
          className={styles.badge}
          data-tone="info"
          data-testid="supervisor-badge"
          title={t("chrome.supervised.open")}
        >
          {t("chrome.supervised", { name: s.display_name || s.principal_id })}
        </Link>
      ))}

      {p.session.break_glass ? (
        <span className={styles.badge} data-tone="warn">
          {t("chrome.breakGlass")}
        </span>
      ) : null}

      <div className={styles.end} data-testid="account">
        <Menu
          label={t("chrome.account.menu")}
          variant="quiet"
          heading={t("chrome.account.label", { name: operator })}
          trigger={
            <>
              <Avatar name={operator} />
              <span className={styles.operator} translate="no">
                {operator}
              </span>
              <svg className={styles.chevron} viewBox="0 0 10 10" aria-hidden="true" focusable="false">
                <path d="M2 3.5 5 6.5 8 3.5" />
              </svg>
            </>
          }
          items={[
            ...THEMES.map((theme) => ({
              id: `theme:${theme}`,
              kind: "radio" as const,
              group: t("prefs.theme.label"),
              checked: p.prefs.theme === theme,
              label: t(theme === "system" ? "prefs.theme.system" : theme === "light" ? "prefs.theme.light" : "prefs.theme.dark"),
              onSelect: () => p.onPrefs({ ...p.prefs, theme }),
            })),
            ...DENSITIES.map((density) => ({
              id: `density:${density}`,
              kind: "radio" as const,
              group: t("prefs.density.label"),
              checked: p.prefs.density === density,
              label: t(density === "screen" ? "prefs.density.screen" : density === "compact" ? "prefs.density.compact" : "prefs.density.comfortable"),
              onSelect: () => p.onPrefs({ ...p.prefs, density }),
            })),
            { id: "sign-out", label: t("chrome.signOut"), onSelect: p.onSignOut },
          ]}
        />
      </div>
    </header>
  );
}
