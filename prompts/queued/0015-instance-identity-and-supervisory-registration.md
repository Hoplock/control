# 0015 — Instance identity, north-bound compatibility & supervisory registration

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§2 (M19, M18, M2, M11, M15)**, §3
  (`internal/instance`), and §4's `policy_version` obligation, which is the
  pattern the north-bound version promise copies.
- `docs/learnings/` — read summaries; open `0004` (the `ext` package and
  `ClusterCoordinator`'s default), `0009` (the event stream, its replay and
  `resync` — this phase reuses its shape pointing the other way), `0011`
  (tokens, scopes, RBAC), and `0014` (the north-bound API this phase versions).
- In the **Hoplock Proxy repository**, `docs/PLAN.md` **D11** and §6.1 —
  outbound relay registration. This phase is that idea one level up, and it
  should be recognisably the same idea in the code.

## Objective
Give a Control deployment an identity, a north-bound surface it can keep
promising across versions, and an opt-in way to register outbound to a
supervisor above it.

None of this manages anything. It makes a deployment **manageable** — by an
operator's own dashboard, by a second Control in a staging/production pair, or
by Hoplock Enterprise's supervisory plane (its E14). Per M15 this is the real
default that ships here; the plane that consumes many deployments is not ours.

## In scope

### Instance identity (`internal/instance`)
A deployment knows what it is and says so north-bound:
- a **stable instance id**, generated once and persisted, never derived from a
  hostname, a pod name, or anything else that changes under a deployment;
- an operator-set display name;
- the software version (already stamped by 0001), the **contract version** it
  vendors, and the **north-bound API version** it serves;
- its tenant set (M18) and, per tenant, whether it is active.

A **clustered deployment reports one identity, not one per node.** The id
belongs to the logical deployment and lives in the database; a node never
carries an identity of its own. Get this wrong and a supervisor counts nodes as
customers.

### Health, at every level the deployment has (M19)
Identity is one question and health is another, and this phase must not answer
the second with the shape of the first. A deployment's health is not a single
word — a three-node cluster with one node down is **degraded**, and a summary
that can only say `healthy` or `unreachable` has discarded the fact an operator
needs. So report health at three levels, beneath one identity:

- **Deployment** — database reachable, migrations at the expected version,
  policy compiled and its version, north-bound and south-bound listeners
  serving, and the roll-up of the two levels below. The roll-up states *why* it
  is degraded, never just that it is.
- **Node** — membership, each node's software version, uptime, last-seen, which
  node holds each leader-elected job and which holds the supervisory
  registration, event-bus health (M9) and any replication or subscription lag. A
  node's version differing from its peers during a **rolling upgrade is expected
  and must read as in-progress, not as a fault** — a monitoring surface that
  cries wolf on every deploy is one an operator learns to ignore, which is worse
  than not having it.
- **Proxy fleet** — the summary 0006 already computes: proxies enrolled, live,
  stale, and unreachable; config-version drift; contract-version spread; live
  relay registrations; recent error counts. Serve it as a **summary with a
  drill-down**, not as a full dump of every proxy on every poll: at telco scale
  (PLAN M5) a fleet view that returns every proxy is a fleet view nobody can
  call often enough to be useful.

Three rules keep this honest:
- **Absent is not healthy.** A check that did not run, a node that has not
  reported, and a fleet summary that could not be computed are each their own
  state and are never rendered as green. This is M11's distinction applied to
  observability — the failure to know is not a finding of health.
- **Every health value is timestamped** with when it was observed, not when it
  was served. A stale value presented as current is the specific way a
  monitoring surface causes an outage to be missed.
- **Health is read-only and off the decision path.** Computing it may not take a
  lock, a connection, or a budget the authorize path needs (M5). A health poll
  that degrades the thing it measures is worse than no health poll.

### The north-bound API becomes a compatibility promise
Until now the north-bound surface shipped in lockstep with its only clients.
A supervisor consuming many deployments meets version skew, so:
- the north-bound API carries an **explicit version**, distinct from the
  software version and from the contract version, moved deliberately;
- a client names the version it can read; the server **never answers outside
  it** — same principle as `policy_version` south-bound (§4), same failure mode
  if got wrong, and the same resolution: a server holding a representation it
  cannot express within the declared version says so (`5xx`, M11) rather than
  sending fields that will be misread;
- what "additive" means on this surface is written down: a new field with a
  documented absent-value default is additive; a changed meaning is not, whatever
  it does to the JSON;
- a deprecation window is stated in the documentation, not discovered by a
  client that broke.

**This retires an assumption phase 0018 rests on.** Its premise — "this product
ships its proxy and its server together and has no installed base" — is true
only while nobody operates a fleet of deployments. Update that phase's prompt in
this PR to say the north-bound surface is exempt from its single-version rule
and why; the south-bound rule is untouched.

### Supervisory registration
An opt-in outbound registration to a supervisor, off by default:
- **Configured locally, revocable locally.** A new config section, strictly
  decoded like everything else, naming the supervisor endpoint and the
  credential. Absent means unsupervised, which is the default and must stay the
  common case. An operator can revoke without the supervisor's cooperation, and
  revoking takes effect without a restart.
- **One long-lived outbound connection**, reusing 0009's stream shape and its
  guarantees pointed the other way: heartbeats, monotonic event ids, replay from
  a `last_event_id`, and `resync` when replay is impossible. Do not invent a
  second streaming mechanism; if 0009's cannot be reused directly, say precisely
  why in the learnings.
- **A cluster singleton.** Acquire it through `ext.ClusterCoordinator` (0004),
  whose default already answers "I am the leader, there is one node". A
  single-node deployment therefore needs no clustering, and a clustered one gets
  a real election from Enterprise (its E9) **with no second code path**. Losing
  leadership drops the registration; gaining it re-registers with the last event
  id. Test both.
- **What flows out**: identity, version set, health at all three levels above,
  and a coarse event feed (a node joined or left, a leader-elected job moved, a
  rolling upgrade started or finished, fleet health changed, licence-relevant
  counts, policy version published). Push health changes rather than making a
  supervisor poll for them: a supervisor watching forty deployments cannot poll
  each often enough to be the first to know, and being the first to know is what
  the supervision is for.
  Not audit records, not decision records, not session content. A supervisor
  that wants those asks the north-bound API for them, authenticated and scoped
  as any other client, so that the request is a request an operator can see and
  refuse — never a side channel opened by registration.
- **What flows in**: nothing that acts. This phase's inbound direction carries
  liveness and a request to re-report, and nothing else. Administrative actions
  from a supervisor are north-bound API calls with a scoped credential, subject
  to RBAC and audit like any other — that separation is what keeps the
  registration channel from quietly becoming a privileged inbound surface.

### The supervisor's credential
- A north-bound token (M2, 0011), scoped by the existing grammar and by tenant
  (M18). No new credential type and no new listener.
- **Every action taken through it is in this deployment's audit trail**,
  attributed to the supervisor rather than folded into an anonymous "API"
  actor. The operator being supervised must be able to read what was done to
  them, in their own audit store, without asking the supervisor.
- Its scope is visible in the north-bound API and in the console (0016), and it
  is revocable there.

## Out of scope
- The supervisory plane itself — enumerating, aggregating, or acting across many
  deployments. That is Enterprise's E14 and it consumes what this phase serves.
- Clustering (Enterprise E9). This phase uses the `ClusterCoordinator` seam and
  its single-node default; it does not implement an election.
- Multi-tenant governance (Enterprise E11). M18's mechanism is assumed present.
- Any change to `hoplock/proxy` or to the vendored contract. There is none: a
  proxy is unaware that its Control is supervised, and that is the design.

## Acceptance criteria
- Instance identity is stable across restarts and across a config change to the
  display name; a three-node deployment reports **one** id, with per-node health
  beneath it.
- **Degradation is legible, not binary.** A three-node deployment with one node
  stopped reports `degraded` **and names the node and the reason**; with the
  database unreachable it reports degraded for a different, distinguishable
  reason. A test asserts the two are not collapsed into one state.
- A rolling upgrade across mixed node versions reports **in progress**, not a
  fault, and returns to healthy when it completes.
- A check that has not run, a node that has not reported, and an uncomputable
  fleet summary each report their own state and never report healthy.
- Health values carry their observation time, and a value older than its
  freshness bound is marked stale rather than served as current.
- Computing health takes nothing the authorize path needs: assert the decision
  path's latency is unaffected while health is polled at its maximum rate.
- North-bound version negotiation: a client declaring an older version never
  receives a field introduced after it; a server holding something it cannot
  express within the declared version answers `5xx` with a correlation id, never
  a thinned body and never a `401` (M11).
- **The unsupervised path is unchanged.** With no supervisor configured, the
  north-bound API, the console, and every test from earlier phases behave
  exactly as before — asserted by running those suites, not by inspection.
- **A supervisor cannot affect a session.** Drive a real proxy through an
  authorize call while the supervisor is (a) unconfigured, (b) configured and
  unreachable, (c) configured and returning errors, (d) configured and hanging
  past every timeout. All four are indistinguishable on the decision path, and
  no case produces a deny. This test may never be weakened.
- Registration singleton: in a three-node deployment exactly one registration
  exists; kill the holder and exactly one exists again, resuming from its last
  event id.
- Local revocation ends the registration without a restart and without the
  supervisor's cooperation.
- Every supervisor action through the north-bound API appears in the local audit
  trail attributed to the supervisor, and a tenant-scoped supervisor token
  cannot read another tenant's rows (M18).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0015-instance-identity-and-supervision-learnings.md`. Summary
block MUST give: the identity shape and where it is persisted; the health model
at all three levels, its states, its freshness bounds, and how "absent" is
distinguished from "healthy"; the north-bound
version number, what moves it, and what "additive" means on that surface; the
registration protocol, its event kinds and its replay semantics; how the
singleton is acquired and what happens on leadership change; the config section;
and the exact scope grammar a supervisor credential uses.

**Cross-repo impact.** Hoplock Enterprise builds its supervisory plane (E14) on
this phase's output. Per `docs/CROSS-REPO-PROTOCOL.md` §3.1 the merging session
owns a sync PR there, and it must carry the north-bound version number, the
registration protocol, and the credential scope grammar verbatim — those three
are what the downstream phase is written against.
