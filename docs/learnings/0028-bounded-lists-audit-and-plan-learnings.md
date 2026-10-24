# 0028 — bounded lists: audit and plan — Learnings

## Summary
- **M26** (not M25, which 0027 took): every north-bound list is bounded by its caller and says where it stopped. `?limit=` defaults to 500 and is refused above 10 000. `?after=` takes an opaque keyset cursor, bound to list, filters and order, never the tenant. The answer is `{<list>, truncated, next}`.
- **The 42 `GET` routes:** 18 need the shape, 4 need only the signal, 20 are exempt. Also signal-only: the publish-time report's finding targets (not a `GET`). Internal readers stay outside M26. Two on a request path are named: the publish-time estate reads, and `Grants().ListLive`.
- **Chunks:**
  - 0029: the mechanism, `targets`, `subjects`;
  - 0030: `server` for a host's lists;
  - 0031: `proxies`, `fleet/config/rollouts`;
  - 0032: `fleet/algorithms`, and `fleet/target-observations` split into `…/rungs` and `…/key-exchanges`;
  - 0033: `grants`, `me/grant-requests`, the grant view's `decisions_truncated`;
  - 0034: `role-bindings`, `ca/certificates`;
  - 0035: `policy/bundles`, plus signals on `sessions/{session}/decisions` and the finding targets;
  - 0036: `audit/records`, `drift`, `algorithm-policy-unmet`, `gaps`, `certificates/{serial}/records`;
  - 0037: `audit/blocked-commands`, `negotiated`, plus signals on the session certificates and algorithms.
- **The test:** `TestEveryListIsBoundedOrSaysWhyNot` (`internal/httpapi/north/bounded_test.go`). Its pending list, `pendingBounded`, lives in that file: 0029 fills it, each chunk empties its rows, and 0037 deletes it.
- **Renumbered:** 0029–0038 moved to 0038–0047 (§10, "bounded-lists-chunks"). Release `v0.15.0`, no behaviour change. A sync for `hoplock/enterprise` is queued (see the PR); `hoplock/proxy`: none.
- **NEXT (0029):** build the mechanism its prompt specifies, not a variant. Fill the test's maps from "Inventory" below.

## Details

### How the inventory was taken
- **Routes:** a throwaway test in `internal/httpapi/north` printed
  `north.Table("", nil, nil)`. That gave 80 routes, of which 42 are `GET`.
- **Readers:** every exported store method returning a slice or a map, found
  with
  `grep -n '^func (r [a-zA-Z]*) [A-Z][A-Za-z]*(.*) (\[\]\|(\[\]\|(map' internal/store/*.go`.
- **Callers:** each reader's callers outside `internal/store` and the tests,
  found by grep.
- **Handlers and services:** read for each route, from handler to statement.

### Inventory: every `GET` route
Paths are under `/api/v1/tenants/{tenant}/` unless they start with `/`.
"Cut?" asks whether a caller can tell a complete answer from a cut one today.

| Route | Permission | Reader | Bound today | Cut? | Grows with | Verdict | Reason | Chunk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `/api/v1/session/methods` | anonymous | `Connectors().List` | configuration | — | configuration | exempt | one per configured sign-in method | — |
| `/api/v1/session/federated/{connector}/callback` | anonymous | broker | — | — | — | exempt | a sign-in step, not a list | — |
| `/api/v1/session/federated/{connector}/metadata` | anonymous | broker | — | — | — | exempt | one document | — |
| `/api/v1/session` | authenticated | the principal | — | — | — | exempt | one object | — |
| `ca` | `ca:read` | `CA.Describe` → `CAKeys().List` | none | — | rotations | exempt | the trust bundle is answered whole or not at all; one key per rotation inside its overlap | — |
| `ca/certificates` | `ca:read` | `CA.Outstanding` → `SSHCertificates().Outstanding` | none | — | use (a certificate per brokered session) | shape | grows with use; its order has no index | 0034 |
| `grants` | `grant:read` | `access.Service.List` → `Grants().List` | default 100, clamped at 1 000 silently | no | time | shape | grows with time; its index lacks the tiebreak's direction | 0033 |
| `grants/{grant}` | `grant:read` | `Inspect` → `Decisions().ListByGrant(50)` | 50 decisions, silently | no | one grant's use | signal (`decisions_truncated`) | a sample inside one object | 0033 |
| `grant-requests/{request}` | `grant:read` | `GrantRequests().Get` | — | — | — | exempt | one object | — |
| `me/grant-requests` | `grant:request` (self) | `OwnRequests` → `ListForSubject` | default 100, clamped at 1 000 silently | no | one requester's time | shape | grows with time; its index has no tiebreak | 0033 |
| `me/grant-requests/{request}` | `grant:request` (self) | `GetForSubject` | — | — | — | exempt | one object | — |
| `identity/claim-mapping` | `identity:read` | the active mapping | — | — | — | exempt | one object; its lists are the document's, bounded by the body it was written in | — |
| `policy/bundles` | `policy:read` | `authoring.Versions` → `PolicyBundles().List` | none, and reads every source | — | every upload | shape | grows with time | 0035 |
| `policy/bundles/{version}` | `policy:read` | `PolicyBundles().Get` | — | — | — | exempt | one object | — |
| `policy/bundles/{version}/diff` | `policy:read` | two versions | — | — | — | exempt | one diff of two stored documents, each bounded by the upload body | — |
| `policy/active` | `policy:read` | the active version | — | — | — | exempt | one object | — |
| `decisions/{decision}` | `decision:read` | `Decisions().Get` | — | — | — | exempt | one object | — |
| `sessions/{session}/decisions` | `decision:read` | `ExplainSession` → `ListBySession(0)` | 100, silently | no | one session's hops | signal | bounded by one session | 0035 |
| `targets` | `fleet:read` | `Targets().ListByLabels` | none | — | estate | shape | one of 0023's eight | 0029 |
| `targets/{target}` | `fleet:read` | `Targets().Get` | — | — | — | exempt | one object | — |
| `subjects` | `identity:read` | `Subjects().List` | none | — | estate | shape | one of 0023's eight | 0029 |
| `subjects/{subject}` | `identity:read` | `Subjects().Get` | — | — | — | exempt | one object | — |
| `role-bindings` | `identity:read` | `RoleBindings().List` | none | — | people × roles | shape | one of 0023's eight; its order has no index | 0034 |
| `proxies` | `fleet:read` | `Registry.Health`: proxies, relays, states, the subscription copy | none | — | fleet | shape | one of 0023's eight | 0031 |
| `fleet/config/rollouts` | `fleet:read` | `ConfigRollouts`/`ConfigDrift` → `ListStates` | none, and reads every desired document | — | fleet | shape | one of 0023's eight | 0031 |
| `fleet/algorithms` | `fleet:read` | `AlgorithmDeclarations` → `ProxyAlgorithmDeclarations().List` | none | — | fleet | shape | one of 0023's eight | 0032 |
| `fleet/target-observations` | `fleet:read` | `TargetCapabilities().List` + `TargetKex` | none | — | estate | shape, split | one of 0023's eight; two lists keyed differently (M26.5) | 0032 |
| `extensions` | `extension:read` | the sealed registry | — | — | registrations | exempt | one row per extension point | — |
| `access-context/providers` | `access-context:read` | configuration | — | — | configuration | exempt | one per configured provider | — |
| `access-context/bindings` | `access-context:read` | `AccessContextBindings().List` | none | — | configuration | exempt | one per configured provider in the tenant (primary key `(tenant, provider)`) | — |
| `access-context/providers/{provider}/binding` | `access-context:read` | one binding | — | — | — | exempt | one object | — |
| `audit/records` | `audit:read` | `Reader.Find` → `Audit().Query` | default 500, clamped at 10 000 silently | no | time | shape | grows with time; its order is indexed on its first key only | 0036 |
| `audit/records/{record}` | `audit:read` | `Audit().Get` | — | — | — | exempt | one object | — |
| `audit/blocked-commands` | `audit:read` | `Audit().BlockedCommands` (join) | as `records` | no | time | shape | grows with time | 0037 |
| `audit/drift` | `audit:read` | `DeviceConfigChanges` → `Query` | as `records` | no | time | shape | grows with time | 0036 |
| `audit/algorithm-policy-unmet` | `audit:read` | `AlgorithmPolicyUnmet` → `Query` | as `records` | no | time | shape | grows with time | 0036 |
| `audit/negotiated` | `audit:read` | `SessionsBy` → `Audit().Sessions` | default 500 sessions, clamped at 10 000 | yes, `truncated` (0023); no cursor | time | shape (gains `next`) | the runbook must reach every session | 0037 |
| `audit/gaps` | `audit:read` | `Gaps` → `Query` | as `records` | no | time | shape | grows with time | 0036 |
| `audit/sessions/{session}/loss` | `audit:read` | `Audit().GapLoss` | none | — | causes | exempt | one row per gap cause, of which the contract names two | — |
| `audit/sessions/{session}/algorithms` | `audit:read` | `Algorithms` → `Query` | 500 records, silently | no | one session | signal | a summary of one session | 0037 |
| `audit/sessions/{session}/certificates` | `audit:read` | `SessionCertificates` → `Query` | 500 records, silently | no | one session | signal | bounded by one session | 0037 |
| `audit/certificates/{serial}/records` | `audit:read` | `CertificateSessions` → `Query` | as `records` | no | one certificate's use | shape | it takes `limit` already and shares the audit query, so paging costs nothing and keeps one rule | 0036 |

### Non-`GET` answers that carry a list
- **The publish-time report.** It answers validate, upload, apply and
  activate, and appears in `409 findings_not_accepted`.
  - Findings name every target (`target.cannot_take`, `target.unproven`, the
    floor's below and not-reported findings) or every proxy that falls short
    of a rule. That is sized by the estate and by the author's selectors.
  - Verdict: **signal**. Cap each list at 100, with `_count` and
    `_truncated`, in **0035**, which also rules on the per-proxy findings
    (one finding per proxy per rule).
- **`POST …/policy/simulate`.** The replay is limited to 10 000 and already
  says `truncated`, and the dry-run cases are the request's own. Exempt.
- **`POST …/targets/relabel`.** Bounded by the request's own changes, and its
  replay is the simulation's. Exempt.
- **`POST …/policy/diff`.** One diff. Exempt.

### Store readers that return a slice with no limit
None of these are north-bound lists, so M26 does not bind them. M5 binds the
ones on the decision path, and what the estate is binds the rest.

| Reader | Callers | Bounded by | Ruling |
| --- | --- | --- | --- |
| `Proxies().List`, `ProxyEdges().List`, `RelayRegistrations().List` | `fleet.Registry.Graph` (the M5 copy, re-read every 5 s); `Health` until 0031; `CheckPolicy` and `authoring`'s facts | the fleet | internal |
| `ProxyConfigs().ListStates` | the announcer (`fleet/config.go`); the rollout view and `Health` until 0031 | the fleet | internal |
| `Proxies().ListByZone` | a configuration publish to a zone | the zone | internal |
| `Targets().ListByLabels(nil)`, `TargetCapabilities().List` | `fleet.CheckPolicy`, once per validate, upload, apply, activate and simulate | the estate | **named:** an estate-sized read on a north request path. The caller drives how often, not how much. Its answer's lists are bounded by 0035 |
| `ProxyAlgorithmDeclarations().List`, `TargetKexObservations().List` | `authoring`'s publish-time facts | the fleet, the estate | as above |
| `EventSubscriptions().List` | the presence copy (M25) | streams | internal, M5's memory |
| `Audit().Streams`, `Feed().Streams` | verification, retention, the archive and sink feeds | streams per tenant | internal |
| `AccessContextBindings().List` | the binding service and its cache | configured providers | internal |
| `Grants().ListLive(subject, at)` | **the decision path** | one subject's live grants, indexed (`grants_live_by_subject_idx`) | **named:** its size is set by whoever creates grants for the subject (`grant:write`, or a rate-limited push). A decision needs every live grant, so it is not this shape. It is an observation, not queued |
| `Connectors().List`, `CAKeys().List`, `RolesFor`, `GroupsOf`, `HostKeys().ListForTarget`, `ProxyTokens().ListByProxy` | federation, the CA, RBAC, host keys, withdrawal | configuration or one object | internal |
| `SSHCertificates().Outstanding` | the CA route, and `hoplock-control ca certificates` | use | the route pages in 0034, and the CLI walks the pages |
| `Subjects().List`, `RoleBindings().List`, `PolicyBundles().List` | their route only | — | replaced by paged readers (0029, 0034, 0035) |
| `Groups().List`, `ClaimMappings().List`, `FederatedIdentities().ListBySubject`, `NorthPrincipals().List`, `SoftwareKeys().List`, `SubjectKeys().ListBySubject`, `UIDLeases().ListForTarget` | no caller outside the store and its tests | — | not lists anyone is served; not this phase's to delete |

These are already bounded, and listed so nobody re-audits them:
- `Decisions().ListRange` (replay; says `truncated`);
- `ListBySubject` (no caller);
- `ListByGrant` (50; signal in 0033);
- `SessionsByGrant` (the kill limit, with its cut flag);
- `GrantRequests().ListPending` (the poll batch);
- `Audit().Chain`, `EventLog().After` and `Feed().PendingCaptures` (each
  limited).

### Findings the chunks fix
- **Orders no index served:**
  - role bindings (0034);
  - grants and own grant requests, whose indexes lack the tiebreak or its
    direction (0033);
  - outstanding certificates (0034);
  - the audit query beyond recorded-at (0036, which replaces the index
    rather than adding one).
- **Reads of columns no row shows:** the bundle list reads every version's
  source (0035), and the rollout view reads every desired document (0031).
- **One kept without an index:** `audit/negotiated`, whose order is an
  aggregate (M26.4).

### Deviations
- **The prompt said `M25`.** 0027 had already taken it, so the decision is
  **M26**. 0038 (host namespaces) expected "0028 takes `M25`", and now says
  0028 took M26 and its own is the next free id.
- **The per-session reads are signal-only, not paged.** The prompt listed
  them as candidates. Their bound is one session, not the estate, so a
  cursor would be followed by nobody.
- **The host chunk is second (0030), not last.** It exposes 0029's mechanism
  and adds none of its own, and Enterprise is building lists now. 0037 is
  last, and it deletes the pending list.

### What 0029 must build
Its prompt specifies the mechanism in full: `store.Page`, `store.Paged`, the
`list` and `keyKind` types, `readPage`, `answerPage`, and the codec. Build
that and nothing else. The maps of `TestEveryListIsBoundedOrSaysWhyNot` come
from the table above:
- `targets` and `subjects` paged;
- every "shape" or "signal" row pending, under its chunk;
- every "exempt" row exempt, with its reason.

### Cross-repo impact
- **`hoplock/enterprise`:**
  - every Control phase number it cites from 0029 on moved by nine
    (instance identity is now 0042, which its E14 builds on);
  - Control's north-bound lists change shape over 0029–0037;
  - `server` gains the list shape in 0030.

  Queued as a downstream sync; see the PR.
- **`hoplock/proxy`:** none. No contract shape is needed, and nothing on
  `/v1` is a list this changes.
