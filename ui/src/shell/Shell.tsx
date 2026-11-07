// The signed-in shell (DESIGN.md §4): the stripe, the top bar, the
// navigation and the content column, around the screen the path names.
//
// It reads, for the tenant in scope, the three things the chrome shows — the
// deployment's identity, its health and its supervisors (0046) — through the
// API client, as the operator: a role without instance:read sees the chrome
// say so, never a privileged answer.

import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import type { ApiClient } from "../api/client";
import type { Health, Identity, Session, Supervisors } from "../api/types";
import { useI18n } from "../i18n/I18n";
import { applyTheme, loadPrefs, savePrefs, type Prefs } from "../prefs/prefs";
import { match, navigate, useLocation } from "../router/router";
import { HOME, SCREENS, type Screen } from "../screens/registry";
import { NotFound } from "../screens/NotFound";
import { currentTenant, holds, liveSupervisors, tenantsInScope } from "./chrome";
import { useLoad, type Load } from "./load";
import { Nav } from "./Nav";
import { Preferences } from "./Preferences";
import { TopBar } from "./TopBar";
import styles from "./Shell.module.css";

/** How often the chrome re-reads the deployment's health while visible. */
const HEALTH_EVERY_MS = 30_000;

export interface DeploymentView {
  tenant: string | null;
  tenants: readonly string[];
  session: Session;
  identity: Load<Identity>;
  health: Load<Health>;
  refresh(): void;
  api: ApiClient;
}

const DeploymentContext = createContext<DeploymentView | null>(null);

/** What the shell knows about the deployment, for a screen beneath it. */
export function useDeployment(): DeploymentView {
  const v = useContext(DeploymentContext);
  if (!v) throw new Error("useDeployment outside the shell");
  return v;
}

function resolveScreen(path: string): { screen: Screen; params: Record<string, string> } | null {
  for (const screen of SCREENS) {
    const params = match(screen.path, path);
    if (params) return { screen, params };
  }
  return null;
}

export function Shell({ api, session, onSignedOut }: { api: ApiClient; session: Session; onSignedOut(): void }) {
  const { t } = useI18n();
  const location = useLocation();
  const [prefs, setPrefs] = useState<Prefs>(() => loadPrefs(session.principal_id));
  const [navOpen, setNavOpen] = useState(false);
  const tenants = useMemo(() => tenantsInScope(session), [session]);
  const tenant = currentTenant(tenants, prefs.tenant);
  const canRead = holds(session, tenant, "instance:read");
  const key = canRead && tenant ? tenant : null;

  const [identity, refreshIdentity] = useLoad<Identity>(key, (signal) =>
    api.call("instance", { params: { tenant: tenant as string }, signal }),
  );
  const [health, refreshHealth] = useLoad<Health>(
    key,
    (signal) => api.call("instanceHealth", { params: { tenant: tenant as string }, signal }),
    HEALTH_EVERY_MS,
  );
  const [supervisors] = useLoad<Supervisors>(key, (signal) =>
    api.call("instanceSupervisors", { params: { tenant: tenant as string }, signal }),
  );

  const forbidden: Load<never> = {
    state: "refused",
    status: 403,
    error: { code: "forbidden" },
  };

  useEffect(() => applyTheme(prefs.theme), [prefs.theme]);

  const change = (next: Prefs) => {
    setPrefs(next);
    savePrefs(session.principal_id, next);
  };

  const signOut = async () => {
    await api.call("endSession");
    onSignedOut();
  };

  useEffect(() => {
    if (location.path === "") navigate(HOME, { replace: true });
  }, [location.path]);
  const resolved = resolveScreen(location.path);
  const density = prefs.density === "screen" ? (resolved?.screen.density ?? "comfortable") : prefs.density;

  const view: DeploymentView = {
    tenant,
    tenants,
    session,
    identity: key ? identity : forbidden,
    health: key ? health : forbidden,
    refresh: () => {
      refreshIdentity();
      refreshHealth();
    },
    api,
  };

  let content: ReactNode;
  if (resolved) content = resolved.screen.render(resolved.params);
  else if (location.path === "") content = null;
  else content = <NotFound path={location.path} />;

  return (
    <DeploymentContext.Provider value={view}>
      <div className={styles.shell} data-density={density}>
        <a className={styles.skip} href="#content">
          {t("shell.skipToContent")}
        </a>
        <TopBar
          session={session}
          identity={view.identity}
          health={view.health}
          tenants={tenants}
          tenant={tenant}
          supervisors={liveSupervisors(key ? supervisors : forbidden)}
          navOpen={navOpen}
          onNav={setNavOpen}
          onTenant={(next) => change({ ...prefs, tenant: next })}
          onSignOut={signOut}
        />
        <Nav
          path={location.path}
          open={navOpen}
          onNavigate={() => setNavOpen(false)}
          footer={<Preferences prefs={prefs} onPrefs={change} />}
        />
        <main id="content" className={styles.main} tabIndex={-1}>
          {content}
        </main>
      </div>
    </DeploymentContext.Provider>
  );
}
