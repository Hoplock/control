// THE PIECES ALGORITHM POSTURE SHARES (0058): Fleet's algorithms view, the
// publish-time report's algorithm findings and the runbook set identifiers,
// proxy ids and the floor's one explanation the same way everywhere.

import { offered } from "../api/permit";
import type { AlgorithmAxis, DeclarationPage, DeclaredAlgorithms } from "../api/types";
import { Chip } from "../components/Badge";
import type { MessageKey } from "../i18n/catalogue";
import { useI18n } from "../i18n/I18n";
import { useLoad, type Load } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { buildsOf, PQ_HYBRID, PQ_HYBRID_KEX, readAll, SNTRUP_KEX, type Build } from "./algorithmModel";
import styles from "./Algorithms.module.css";

/** At most this many proxy ids are listed beside a build or a level; the count says the rest. */
const SHOWN = 12;

/** Each list is read in pages of this size, to its end. */
export const READ_LIMIT = "1000";

/** Each axis's name in the console's words; the identifier is the contract's. */
export const AXIS_LABEL: Readonly<Record<AlgorithmAxis, MessageKey>> = {
  key_exchanges: "algorithms.axis.key_exchanges",
  ciphers: "algorithms.axis.ciphers",
  macs: "algorithms.axis.macs",
  host_keys: "algorithms.axis.host_keys",
  public_key_auth: "algorithms.axis.public_key_auth",
};

/** Identifiers, as data: mono, LTR, never translated (DESIGN.md §9). */
export function Idents({ names }: { names: readonly string[] }) {
  const { t } = useI18n();
  if (names.length === 0) return <span className={styles.quiet}>{t("algorithms.none")}</span>;
  return (
    <ul className={styles.idents}>
      {names.map((n) => (
        <li key={n}>
          <code dir="ltr" translate="no">
            {n}
          </code>
        </li>
      ))}
    </ul>
  );
}

/** Proxy ids, the first few as chips and the count of the rest. */
export function ProxyChips({ ids }: { ids: readonly string[] }) {
  const { t } = useI18n();
  return (
    <span className={styles.chips} data-proxies={ids.join(" ")}>
      {ids.slice(0, SHOWN).map((id) => (
        <Chip key={id} data>
          {id}
        </Chip>
      ))}
      {ids.length > SHOWN ? <span className={styles.quiet}>{t("algorithms.more", { count: ids.length - SHOWN })}</span> : null}
    </span>
  );
}

/**
 * What pq-hybrid-kex is, said wherever a finding names it (PLAN §5.2). It
 * never promises sntrup761. The exchanges are identifiers, so they are set as
 * code beside the sentence, never inside it: the UI face's contextual
 * alternates would draw the `x` in `768x25519` as a multiplication sign.
 */
export function PqHybridNote() {
  const { t } = useI18n();
  return (
    <div className={styles.note} data-pq-note="">
      <p className={styles.para}>{t("algorithms.pq.explain", { level: `\u2068${PQ_HYBRID}\u2069` })}</p>
      <dl className={styles.facts}>
        <dt>{t("algorithms.pq.accepted")}</dt>
        <dd>
          <code dir="ltr" translate="no">
            {PQ_HYBRID_KEX}
          </code>
        </dd>
        <dt>{t("algorithms.pq.notMet")}</dt>
        <dd>
          <code dir="ltr" translate="no">
            {SNTRUP_KEX}
          </code>
        </dd>
      </dl>
    </div>
  );
}

/**
 * The fleet's builds, read to the end of GET …/fleet/algorithms (M26), for a
 * view that must say whether builds disagree. Undefined where it is not asked
 * for or the role does not hold the route's permission: the console never
 * makes a call RBAC would refuse (M2).
 */
export function useBuilds(enabled: boolean): Load<Build[]> | undefined {
  const { tenant, session, api } = useDeployment();
  const permissions = (tenant && session.scopes[tenant]?.permissions) || [];
  const allowed = enabled && Boolean(tenant) && offered("fleetAlgorithms", permissions);
  const [load] = useLoad<Build[]>(allowed ? `builds|${tenant}` : null, async (signal) => {
    const all = await readAll<DeclarationPage, DeclaredAlgorithms>(
      (after) =>
        api.call("fleetAlgorithms", {
          params: { tenant: tenant as string },
          query: after ? { limit: READ_LIMIT, after } : { limit: READ_LIMIT },
          signal,
        }),
      (p) => p.declarations,
    );
    return all.kind === "ok" ? { ...all, data: buildsOf(all.data) } : all;
  });
  return allowed ? load : undefined;
}
