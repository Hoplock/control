# 0058 — the console's algorithm posture and the emergency runbook — Learnings

## Summary
- **Shipped (`v0.45.0`, MINOR; no migration; north-bound API unchanged at `5`; `ext`/`server`/`contract` untouched; no Go change):** Fleet's algorithms view, the floor's impact preview and a ban's per-build warnings inside the publish-time report's finding cards, and the advisory runbook.
- **Routes each view consumes** (`ui/src/api/routes.ts`): algorithms view (`/console/fleet/algorithms`) — `GET …/fleet/algorithms` and `GET …/proxies` (`fleet:read`), both read to their last page at `limit=1000`. Report findings — no new route: the `algorithm.*` findings of `POST …/policy/validate`, plus `GET …/fleet/algorithms` (to the end, only where an `axis_emptied` finding is shown and the role holds `fleet:read`). Runbook (`/console/policy/runbook[?axis=&name=&limit=&range=|from=&to=]`) — `GET …/policy/active`, `GET …/fleet/algorithms`, `POST …/policy/validate` (`policy:read`/`fleet:read`), `POST …/policy/apply` (`policy:publish`), `POST …/revocations` (`session:revoke`), `GET …/audit/negotiated` (`audit:read`).
- **The runbook as built** (`ui/src/screens/Runbook.tsx`, `runbookModel.ts`): (1) the active source with the name added to the bundle's own top-level `algorithm_bans` (`withBan`), checked, every warning accepted by id, applied with `{source, accept}` — or, where the active version already bans it everywhere, continued from that version with no call; (2) `{type: cache_invalidate, all: true}`; (3) `audit/negotiated?axis=&value=&limit=` followed to `truncated: false`, then `{type: session_kill, session_ids, reason}` in batches of 500. Each step is offered only after the one before it answered (`stepStates`), confirms by typing (the name, the tenant, the name) and keeps its receipt.
- **Key files:** `ui/src/screens/{FleetAlgorithms,AlgorithmFindings,AlgorithmParts,Runbook}.tsx`, `algorithmModel.ts`, `runbookModel.ts`, `Algorithms.module.css`; tests `ui/test/{algorithms,runbook}.test.ts`; fixture `ui/e2e/fixture/algorithms.ts`; specs `ui/e2e/algorithms.spec.ts` (checks), `runbook.spec.ts` (mutations).
- **Statuses added** (DESIGN.md §6): `declared`, `done` (allow); `notDeclared`, `notJudged` (unknown). Error sentence: `list_unterminated` (the console's own).
- **Decisions:** none added or amended; register unchanged. PLAN §4 (one sentence) and §10 (0058's row) revised in place; **`ui/DESIGN.md` §6 and §7 changed** (Details).
- **Gotchas:** a declaration is recorded only on an ALLOW (`decision.Service.serve`), so the fixture authorizes sessions the seeded policy allows; a negotiated record is kind `provisioning` with `event: target.algorithms_negotiated`; a "build" is a distinct declaration — the contract carries no build id.
- **NEXT (0059):** Fleet now has view links (Proxies | Algorithms) above its summary; the health bar is on the Proxies view.

## Details

### Why each view reads what it reads

- **The posture is the fleet's, so both lists are read whole.** One page of
  declarations would make "builds disagree" a statement about a page. The
  lists are bounded by the fleet, as the publish-time report's own read is,
  and `readAll` follows `next` to `truncated: false`; a page that fails fails
  the read (M26.6), and a page that says it continues without a new position
  is the console's own outage, `list_unterminated`. Declarations failing is
  the error state; proxies failing is the partial state (who declared nothing
  is the named gap).
- **The report's evidence is the server's.** The impact preview and the
  per-build ban findings were already in the report (0019, 0035); this phase
  sets their parameters out — `targets[]` (`hostname`, `port`, `floor_met`,
  `negotiated`, `offered`, `observed_at`) and `proxies[]` (`proxy_id`,
  `offered`) — inside the card, through `ALGORITHM_DETAIL`, in place of the
  card's generic chips. The one thing a finding cannot say is whether the
  other declaring builds would refuse the route too, so the report reads the
  declarations for that alone, and says `In progress` when
  `0 < proxies_count < declaring`. A role without `fleet:read` sees the
  finding without that sentence.
- **Stale and absent are one case.** The server's `target_not_reported`
  already lumps them; the console shows each `Not reported` and never in the
  below-floor table.
- **`pq-hybrid-kex`** is explained wherever a finding or the levels table
  names it. The exchanges sit beside the sentence as code, not inside it:
  Inter's contextual alternates drew `768x25519` as `768×25519`.

### The runbook's choices

- **Step 1 writes the ban everywhere**, as the bundle's top-level
  `algorithm_bans` (PLAN §5.2: the compiler adds it to every allow route).
  `withBan` edits text: a flow list gains the name, a block list gains an
  item, an absent axis or block is added; a shape it does not edit (a flow
  mapping) is left to the operator with a sentence. The candidate is shown in
  the mono editor, and what was added is quoted above it.
- **A ban already in force** (the active source bans the name at top level —
  a GitOps pipeline applied it) lets step 1 complete with no call. A ban on
  routes only is not detected, and the operator applies one here.
- **Public-key authentication is not a runbook axis**: `audit.negotiatedAxes`
  has no public-key entry (it is offered, not negotiated), so no session can
  be found by one.
- **The window** is the time range picker, all time by default: ending a
  session that already ended is harmless, missing one that started long ago
  is not.
- **Progress is component state.** A reload starts again; step 2 sent twice
  is harmless. Not kept in storage on purpose: a half-run runbook resumed in
  another tab would be a worse surprise.
- **RBAC**: each step's control is rendered only for a role holding its
  permission and otherwise named in words; a held control is shown closed
  until its step is ready, so the harness finds exactly one per holder. The
  harness covers the three mutating calls; the reads (`policy/active`,
  `policy/validate`, `fleet/algorithms`, `audit/negotiated`) are held by every
  fixture role, so they are covered by its "every request names a route the
  role holds" assertion rather than a refusal.

### Changes to `ui/DESIGN.md` in this PR, and why

1. **§6, the allow row: `Declared`, `Done`.** A level or profile every
   declaring build states alike needed a word that is not `Active` (nothing
   is in force) or `Valid` (nothing compiled); a workflow step that answered
   needed one that is not `Applied` (a proxy's document).
2. **§6, the unknown row: `Not declared`, `Not judged`.** A level no build
   declares is a fact of the fleet, the same kind as `Not registered`, and
   never a failure; a build that cannot judge a ban is never fine, and is not
   `Not reported` either, because it did report — just not that.
3. **§6, a paragraph on algorithm posture** (per build, in progress, not
   reported, not declared, not judged, a runbook step's two states).
4. **§7, a finding's evidence and a workflow**: evidence inside the card,
   never beside the report; a workflow's steps, offered in order and by
   permission, each destructive call through `ConfirmDialog`, each answer
   kept as a receipt.

### The fixture

`fixture/algorithms.ts` (`seedAlgorithms`, after `seedAudit`, whose vault
grant px-dmz-03's allow rides on): three builds declared on real allows —
px-core-01 (both levels, three ciphers), px-core-02 (modern-kex without
ML-KEM, `aes128-ctr` alone), px-dmz-03 (levels, no profiles) — so a ban on
`aes128-ctr` empties the cipher axis on one of two judgeable builds; db-01
last met `modern-kex` (fresh, `now`), api-01's observation is from January
(stale), vault-01 has none; five sessions that negotiated `aes128-ctr`. The
candidate (`floorCandidate()`) is the seeded policy plus a `prod-pq-floor`
rule and the top-level ban. The runbook spec opens with `?limit=2`, so the
negotiated list is three pages. The two declaring allows to db-01 are
recorded decisions like any other, so replaying v3 flips them as it flips
`sess-e2e-allow`: the author's flow in `mutations.spec.ts` now expects four
flips, three of them allow→deny to db-01. A fixture that records decisions
moves every replay over recorded traffic; run the mutations project too.

### The baseline

New: `fleet-algorithms*`, `policy-runbook*`, `policy-candidate-floor*`.
Rewritten because the screen changed: `fleet*` (view links), `policy*` list
(the runbook link). Rewritten because the fixture's data changed: the audit
views (`audit-records*`, the counts) show the new decisions and records, and
the policy version's replay shows the new decisions flipping. CI's
`console-e2e` gains `algorithms and runbook`.

### Noticed, not changed

- A target's key-exchange observations (`…/fleet/target-observations/
  key-exchanges`) have no console list of their own; the console shows them
  where they bear on a decision, in the floor's findings. PLAN §4 now says so.
- The report reads the declarations a second time where the server already
  read them; a count of declaring proxies on `axis_emptied` would save it, an
  additive member of the report.

### Cross-repo

No shared surface changed (`ext`, `server`, the contract and the cross-repo
protocol are untouched), but the release changes what Enterprise's binary
serves and the status vocabulary its screens are built from, so a downstream
sync for `hoplock/enterprise` is queued (the PR's `## Cross-repo impact`).
Proxy: none; `Hoplock/proxy#69` and `#72` are consumed as specified.
