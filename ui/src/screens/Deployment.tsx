// The deployment view (DESIGN.md §5: what the chrome's chip opens): the
// deployment's identity, its version set and its health at every level it has
// (PLAN M19, 0046). Health that reads in_progress is an upgrade, not a fault;
// a check never observed is Not reported, never healthy; and a health that
// could not be read is an outage, never a denial.

import type { Health, Identity, Reason } from "../api/types";
import { PageHeader } from "../components/PageHeader";
import { EmptyState, ErrorState, ScreenFrame, Skeleton } from "../components/States";
import { Ident } from "../components/Ident";
import { Status } from "../components/Status";
import type { MessageKey } from "../i18n/catalogue";
import { useI18n } from "../i18n/I18n";
import type { Translate } from "../i18n/messages";
import { HEALTH_STATUS } from "../shell/chrome";
import { useDeployment } from "../shell/Shell";
import styles from "./Screen.module.css";

/** Each health reason this console has a sentence for (instance.AllReasons). */
export const REASON_MESSAGES: Readonly<Record<string, MessageKey>> = {
  database_unreachable: "reason.database_unreachable",
  schema_behind: "reason.schema_behind",
  schema_ahead: "reason.schema_ahead",
  listener_not_serving: "reason.listener_not_serving",
  node_not_reporting: "reason.node_not_reporting",
  node_not_reported: "reason.node_not_reported",
  nodes_unreadable: "reason.nodes_unreadable",
  membership_unavailable: "reason.membership_unavailable",
  rolling_upgrade: "reason.rolling_upgrade",
  event_plane_lag: "reason.event_plane_lag",
  event_plane_failures: "reason.event_plane_failures",
  fleet_uncomputable: "reason.fleet_uncomputable",
  proxies_stale: "reason.proxies_stale",
  proxies_unreachable: "reason.proxies_unreachable",
  config_drift: "reason.config_drift",
  stale: "reason.stale",
  not_observed: "reason.not_observed",
};

export function reasonText(t: Translate, r: Reason): string {
  const key = REASON_MESSAGES[r.code];
  const values = { node: r.node ?? "", tenant: r.tenant ?? "", check: r.check ?? "", count: r.count ?? 0 };
  return key ? t(key, values) : t("reason.unknown", { code: r.code });
}

function IdentitySection({ identity, tenantsShown }: { identity: Identity; tenantsShown: boolean }) {
  const { t, f } = useI18n();
  const v = identity.versions;
  return (
    <>
      <section className={styles.section} aria-labelledby="deployment-identity">
        <h2 id="deployment-identity" className={styles.sectionTitle}>
          {t("deployment.identity")}
        </h2>
        <dl className={styles.fields}>
          <dt>{t("deployment.name")}</dt>
          <dd translate={identity.display_name ? "no" : undefined}>{identity.display_name || t("chrome.deployment.unnamed")}</dd>
          <dt>{t("deployment.instanceId")}</dt>
          <dd>
            <Ident value={identity.instance_id} />
          </dd>
          <dt>{t("deployment.created")}</dt>
          <dd>
            <time dateTime={f.utc(identity.created_at)} title={f.utc(identity.created_at)}>
              {f.dateTime(identity.created_at)}
            </time>
          </dd>
          {tenantsShown ? (
            <>
              <dt>{t("deployment.tenants")}</dt>
              <dd translate="no">{f.list(identity.tenants.map((x) => x.tenant))}</dd>
            </>
          ) : null}
        </dl>
      </section>
      <section className={styles.section} aria-labelledby="deployment-versions">
        <h2 id="deployment-versions" className={styles.sectionTitle}>
          {t("deployment.versions")}
        </h2>
        <dl className={styles.fields}>
          <dt>{t("deployment.software")}</dt>
          <dd>
            <Ident value={v.software} />
          </dd>
          <dt>{t("deployment.contract")}</dt>
          <dd>
            <Ident value={v.contract} /> <Ident value={v.contract_commit} short={v.contract_commit.slice(0, 12)} />
          </dd>
          <dt>{t("deployment.northApi")}</dt>
          <dd>{t("deployment.northApiRange", { current: v.north_api, min: v.north_api_min })}</dd>
        </dl>
      </section>
    </>
  );
}

function HealthSection({ health }: { health: Health }) {
  const { t, f } = useI18n();
  const status = HEALTH_STATUS[health.state] ?? HEALTH_STATUS.unknown;
  const members = health.nodes.members;
  return (
    <section className={styles.section} aria-labelledby="deployment-health">
      <div className={styles.header}>
        <h2 id="deployment-health" className={styles.sectionTitle}>
          {t("deployment.health")}
        </h2>
        <Status tone={status.tone} label={t(status.label)} />
      </div>
      {health.reasons.length === 0 ? (
        <p className={styles.empty}>{t("deployment.reasons.none")}</p>
      ) : (
        <ul className={styles.reasons}>
          {health.reasons.map((r, i) => (
            <li key={`${r.code}-${r.node ?? ""}-${r.tenant ?? ""}-${r.check ?? ""}-${i}`}>{reasonText(t, r)}</li>
          ))}
        </ul>
      )}
      <h3 className={styles.sectionTitle}>{t("deployment.nodes")}</h3>
      {members.length === 0 ? (
        <EmptyState title={t("deployment.nodes")} body={t("deployment.nodes.none")} />
      ) : (
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th scope="col">{t("deployment.node")}</th>
                <th scope="col">{t("deployment.node.version")}</th>
                <th scope="col">{t("deployment.node.state")}</th>
                <th scope="col">{t("deployment.node.reported")}</th>
              </tr>
            </thead>
            <tbody>
              {members.map((m) => {
                const s = HEALTH_STATUS[m.state] ?? HEALTH_STATUS.unknown;
                return (
                  <tr key={m.node_id}>
                    <th scope="row">
                      <Ident value={m.node_id} />
                    </th>
                    <td>
                      <Ident value={m.version} />
                    </td>
                    <td>
                      <Status tone={s.tone} label={t(s.label)} />
                    </td>
                    <td>
                      {m.reported_at ? (
                        <time dateTime={f.utc(m.reported_at)} title={f.utc(m.reported_at)}>
                          {f.dateTime(m.reported_at)}
                        </time>
                      ) : (
                        t("deployment.node.never")
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function Loading() {
  return (
    <div className={styles.screen}>
      <div className={styles.section}>
        <Skeleton shape="title" size="s" />
        <Skeleton size="l" />
        <Skeleton size="m" />
        <Skeleton size="l" />
      </div>
      <div className={styles.section}>
        <Skeleton shape="title" size="s" />
        <Skeleton size="m" />
        <Skeleton size="m" />
      </div>
    </div>
  );
}

/**
 * The four states (DESIGN.md §7): loading is the sections' own skeleton; an
 * identity that cannot be read is the error state (refused or outage, never
 * conflated, M11); a health that cannot be read beside a readable identity
 * is partial, the gap named; a deployment no node has reported for is the
 * nodes' empty state, inside the health section.
 */
export function DeploymentScreen() {
  const { t } = useI18n();
  const { identity, health, tenants, refresh } = useDeployment();
  const gaps = health.state === "outage" ? [t("deployment.partial.health")] : [];
  return (
    <section className={styles.screen} aria-labelledby="deployment-title">
      <PageHeader title={<span id="deployment-title">{t("deployment.title")}</span>} />
      <ScreenFrame load={identity} onRetry={refresh} loading={<Loading />} isEmpty={() => false} empty={null} gaps={gaps}>
        {(data) => (
          <>
            <IdentitySection identity={data} tenantsShown={tenants.length > 1} />
            {health.state === "ok" ? (
              <HealthSection health={health.data} />
            ) : health.state === "loading" ? (
              <Loading />
            ) : health.state === "refused" ? (
              <ErrorState error={health.error} kind="refused" />
            ) : null}
          </>
        )}
      </ScreenFrame>
    </section>
  );
}
