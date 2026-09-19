# audit — cross-repo impact — Learnings

> One file, updated in place (`docs/PROTOCOL.md` §6). **Newest run at the top**;
> earlier runs are kept beneath it. The history is the point: it is what shows
> whether the same obligation keeps being missed.

---

## Run 2026-09-19

> Taken against proxy `37359c5` on **2026-09-14**, then **re-verified and
> extended to `ba9ad26` on 2026-09-19** before merge, after five Control phases
> (0002–0006) and three more proxy PRs landed in between. One run, one record:
> the earlier date is when the walk started, not a separate run. Every finding
> below was re-confirmed against `origin/main` on the later date — **none had
> been fixed by the intervening work.**

## Summary
- **As-of markers.** Proxy `main` at **`ba9ad26`** ("Merge pull request #61").
  **Highest proxy PR examined: #61.** Bounded set: **21 merges**, from
  `git log --oneline --merges --full-history -- api/ docs/CROSS-REPO-PROTOCOL.md`.
  Both halves reached: files (clone) **and** PR bodies (GitHub API).
- **The next run can skip** re-deriving the history before #61. Take merges newer
  than `ba9ad26`, and re-run §5's contract check — that half reads the *document*
  and is never skippable.
- **The command in the prompt was wrong and is now fixed.** Without
  `--full-history` git drops merges TREESAME to a parent: the bare form returns
  **1** merge, the `--full-history` form **21**. Every earlier run of this audit
  was searching a 1-PR set.
- **Three obligations were missing and are landed here**, all traceable to
  `Hoplock/proxy#15` (contract v3), whose sync never ran as its own PR:
  `algorithm_profile` (0008, 0010, PLAN §5.2/§7), and `target_auth_method` +
  `target_auth_rung` (0010, PLAN §7). **All three are now in this repository's
  own vendored `contract/control.yaml`** and named by no prompt — so this is no
  longer "upstream said something we have not heard"; the fields sit in-tree
  unreferenced.
- **One §6 assumption landed**: the `/v1/auth/password` first-factor oracle,
  decided in proxy phase 0034, is **Control's** decision (proxy D2) and the
  contract *requires* it — now in 0007 so no session "hardens" it into a
  contract violation.
- **§3.2 dependencies found: none.** Everything needed already exists upstream.
- **Gotcha:** the proxy's history carries **two PR-number series**. Early merges
  read `#3 from mauroasilva/…` (a predecessor repo) while `Hoplock/proxy#3` is a
  different, later PR — so a number resolved naively returns a wrong body that
  looks right.
- **`contract/` now exists** (0002 landed), so §7's vendoring rules are live for
  the first time. The vendored copy is pinned at `37359c5` while upstream is at
  `ba9ad26`; that is **legitimate and owned** — see below.

## Details

### What was audited, and how

Both halves of §1 were available. The proxy was already attached to the session
and cloned at `/home/user/proxy`; PR bodies were fetched over the GitHub API
(`GET /repos/Hoplock/proxy/pulls/<n>`, and `mcp__github__pull_request_read`).
**No impact section was reconstructed from a commit title.**

The clone arrived **shallow** (151 commits) and was deepened with
`git fetch --depth=1000 origin main` until `git rev-parse
--is-shallow-repository` reported `false` (194 commits, back to the initial
commit). A shallow clone truncates the bounded set silently, so this check is
now written into the prompt.

### The bounded set: 18 merges, not 1

```
git -C <proxy clone> log --oneline --merges --full-history \
    -- api/ docs/CROSS-REPO-PROTOCOL.md
```

The prompt's own command omitted `--full-history`. Git's default history
simplification discards a merge commit TREESAME to one of its parents, which in
a repository where every PR lands as a merge commit is nearly all of them. The
bare form returns **one** merge (#35) — and would have reported the estate clean
while `#41`, `#51` and `#53`, the three PRs the prompt itself names as its
motivating evidence, sat outside the set. This is the single highest-value
finding of the run, because it is the one that silently disables every future
run. Fixed in the prompt, with the measured numbers, so the next session cannot
re-derive the short form and believe it.

### The two PR-number series

Four merges in the set (`3bb9cd3`, `4f186c2`, `daa4928`, `b75f5ff`) read
`Merge pull request #2..#5 from mauroasilva/…`. These are PRs of a **predecessor
repository**; `Hoplock/proxy#2..#5` are different, later PRs whose bodies read
plausibly and are about other work. The mismatch is detectable — the API's
`head.label` does not match the branch in the merge subject — and the check is
now in the prompt.

Those four are the genesis of the contract (`api/management.yaml`, later renamed
`api/control.yaml`). Their bodies are unreachable, and **nothing is owed**: every
shape they introduced has been superseded through contract v2/v3/v4 and the
collapse, and what survives is the present-tense document that §5's check reads
directly. `management.yaml` has **0** hits in this repository.

### Findings table

| Proxy PR | Surface | Impact section | Obligations | Where it stands here |
| --- | --- | --- | --- | --- |
| fork #2–#5 | `api/` | **unreachable** (predecessor repo) | genesis of the contract, all superseded | Nothing owed; covered by §5's document check. `management.yaml`: 0 hits |
| #1 | `api/` both | **misnamed** — `## Note for the sibling repos` | `bastion_id`→`proxy_id`, `/v1/bastions/`→`/v1/proxies/`, `policy_version` | Landed (Control#1). `bastion`: **0** hits. Predates the protocol (#2 created it the next day), and it *did* look downstream |
| #2 | protocol | Yes | mirror the file | Landed (Control#2) |
| #6 | `api/README.md` | Yes | `/v1/auth/cert` recognises a proxy key as a chain leg; read `conn.hop_trail` | Landed (Control#5). `hop_trail` 16 hits, `auth/cert` 4 |
| #8 | protocol | Yes | mirror file; own `KICKOFF.md` sync block | Landed (Control#4) |
| **#15** | `api/` both | Yes | contract v3: ladder shape, platform advertisement, `username`, **audit field names** | **Partly landed — no sync PR of its own.** Ladder ✅, platform ✅, `username` ✅ (late, via Control#17); **`target_auth_method`/`target_auth_rung`/`algorithm_profile` ❌ — landed by this PR** |
| #21 | `api/` both | Yes | `device_field.<name>` namespace, shape rules | Landed (Control#6). `device_field` present with the full shape rule in 0008 |
| #25 | `api/` both | Yes | two enforcement axes, capability reports, four session bounds, `policy_version` 4 | Landed (Control#8). 0008:328–360; `capabilities/report` 9 hits, `session_deadline` 8, `grant_context` 7 |
| #26 | `api/` both | Yes — **"None"**, verified | none | Correct: contract byte-identical, prose only |
| #33 | `api/` both | **absent** | — | Derived from the diff: a proxy-internal phase renumber (`0025`→`0024`) in `api/` prose. **Genuinely none.** No wire shape moved |
| #35 | `api/` both | Yes | `HostKeyReportResponse.cache`; reuse keyed on target+port+fingerprint; `4.0.0`→`4.1.0` | Landed (Control#12/#13) |
| #36 | protocol | Yes | mirror file; `PROTOCOL.md` §2 branch rule; `KICKOFF.md` | Landed (Control#11) |
| #41 | `api/` both | Yes | `username` REQUIRED on `brokered-key` | Landed (Control#17). `username` 17 hits — **was 0 when the impact section claimed it "already carries" it** |
| #51 | `api/` both | Yes | `POST /v1/uids/lease`; cursor only ever advances; floor not on the cacheable response | Landed (Control#16), thoroughly: 0003 (the `CHECK`/trigger), 0007 §198+, 0002 §159+, PLAN §791/§906 |
| #53 | `api/` both | Yes | singular `target_auth` gone; `policy_version` REQUIRED; `info.version` `4.0.0`; history sections gone | Landed (Control#19), thoroughly — see §5 below |

| #56 | `api/` both | Yes | re-vendor; `RevocationEvent.HeartbeatIntervalSeconds` + resolver; conformance reads the advertised interval; two ambiguities now answered | Landed (`07d5ce5`) — 0009 rewritten to own it, 0018 and PLAN updated. **The re-vendor is deliberately deferred to 0009 step 1** (below) |
| #60 | protocol | Yes | mirror the file; `KICKOFF.md` "Upstream request" block both ways; `PROTOCOL.md` §3 cites §4.1 + §4.2; learnings README; **this audit prompt's §3.2 citations**; `0018` | Landed (Control#28) — all six items, including this prompt's §3.2 wording |
| #61 | none of its own | Yes — **"None"**, verified | none | Correct. It appears in the set only because the *merge* diff spans #60; its own branch changed no surface |

Buckets: **14 stated obligations**, **2 stated "None"** (#26 and #61, both
verified against the diff), **2 with no section** (#1 misnamed but substantive;
#33 absent and genuinely none), **4 unreachable**.

### Re-verification on 2026-09-19

Between the two dates Control implemented **0002–0006** and took two syncs
(#56, #60); the proxy merged **#56, #59, #60, #61**. Every finding was re-run
against `origin/main` at `6b0cdb7`:

| Finding | Status on 2026-09-19 |
| --- | --- |
| `--full-history` missing from the prompt | **still absent** — still needed |
| PR-number-collision edge missing | **still absent** — still needed |
| `algorithm_profile` | **still 0 hits** in `prompts/` and `docs/PLAN.md` |
| `target_auth_method` / `target_auth_rung` | **still 0 hits** |
| first-factor oracle absent from 0007 | **still absent** |
| `/v1/proxies/{id}/events` | **still `{id}`** at PLAN §4 and §10 |
| wrapped `contract v4` citation | **still present** at `PLAN:1385` |

Nothing was fixed by the intervening work, and the first four are now *more*
load-bearing than they were: `contract/` landed with 0002, so
`algorithm_profile`, `target_auth_method` and `target_auth_rung` are in this
repository's own vendored contract, and no prompt names them.

One thing the re-verification **removed** from the finding list. The vendored
copy is pinned at `37359c5` while upstream is `ba9ad26`, and `make
contract-check` passes because it checks the copy against its *pin*, not the pin
against upstream. That looked like a silent divergence, and it is not: #56's sync
recorded the obligation and **phase 0009 owns the re-vendor as its explicit step
1** (`prompts/queued/0009…:10-20, 45-60`), `0018:84-86` says the copy is already
behind and names 0009, and `PLAN:884` states both numbers. The audit prompt's own
§5 already carries the rule that settles it — a gap between `contract/UPSTREAM`
and upstream `main` is a finding **only when no prompt names the re-vendor** — so
this is correctly owned, and re-vendoring here would be a §3.1 sync folded into
an audit, which §7 forbids.

### §5 — the independent contract check

Against the **document**, not the claims about it.

- **Stale revision citations.** The prompt's grep returns **2** hits, both
  correct: `0002:22` describes the *absence* of those sections in the present
  tense, and `CROSS-REPO-PROTOCOL.md:200` is a commit-message example inside a
  **proxy-owned mirrored file** — not this repository's to edit (§6; the
  upstream copy carries the same line).
- **One hit the grep could not see.** It is line-based and these documents are
  hard-wrapped, so `docs/PLAN.md`'s `grant_context` bullet — wrapped as
  `contract\n  v4` — survived the sync that removed every other citation. Found
  with a wrap-insensitive scan, now written into the prompt. Restated in the
  present tense and the number deleted.
- **The two version numbers are current, including the split.** The vendored
  `contract/control.yaml` says **4.0.0** and upstream `api/control.yaml` now says
  **4.1.0** (#56 moved it up); `policy_version` is **4** on both. Both are stated where the owning phase
  sees them (0002, 0018, PLAN §4), and **non-monotonicity is explicitly
  recorded** — `0018:107`, `0002:234`, `PLAN:854` all note the `4.3.0`→`4.0.0`
  move. No check assumes it only rises.
- **`policy_version` required, no absent-value default — intact, and the
  mechanism is intact.** This is the drift the prompt calls most damaging and it
  has **not** happened: `0008:94–137` states required-with-no-default, the `400`
  for absent, never `401`, and then says in terms that removing the older
  versions is not removing the versioning. Also `PLAN:821`, `0002:116`,
  `0018:121`, `0006:33`.
- **Endpoint table vs. contract.** All ten contract paths are accounted for. One
  drift: PLAN §4 and §10 wrote `/v1/proxies/{id}/events` where the contract says
  **`{proxy_id}`** (0009 already had it right). Fixed.
- **Enum values.** Every value of `method`, `execution` and `reach` that this
  repository names matches the contract. The only absentees were
  `legacy-rsa-sha1` and `legacy-device` — the `algorithm_profile` enum, i.e. the
  same gap, which is corroboration rather than a separate finding.
- **`D*` ids.** Every id cited here — D1, D2, D3, D4, D5a, D6a, D7, D11, D12,
  D13, D14, D15, D16, D17 — resolves in the proxy's register today. **D17 is
  ⊘ WITHDRAWN upstream**, and this repository's single mention (`PLAN:1374`) is a
  historical range in a renumbering note ("decisions D13–D17 there"), not a claim
  that D17 settles anything. Correct as written; left alone.

### The obligations landed by this PR

1. **`algorithm_profile`** → `0008` (snapshot fields) and `PLAN §5.2`. 0008 still
   carried proxy#15's placeholder *verbatim* — "A per-route algorithm profile
   where the target speaks something the proxy's SSH stack does not enable by
   default" — with no field name, no enum and no absent-value default, while the
   two placeholders beside it had both been replaced. That is the fingerprint of
   a sync that ran partially. Now: the name, the three-value enum, absent ⇒
   `default`, server-named-per-route, preset-not-a-list, and unknown values
   refused rather than coerced.
2. **`target_auth_method` / `target_auth_rung`** → `0010` and `PLAN §7`. The
   placeholder "The credential method and enforcement rung actually in force"
   had had its *rung* half filled in (four fields, from #25's sync) and its
   *credential-method* half left abstract. Now: both field names, `target_auth_rung`
   as the **0-based index** into `target_auth_ladder`, `algorithm_profile` stored
   beside them, and **D14's non-disclosure rule** — the one place PLAN §4.3's
   disclosure rule does not apply.
3. **The first-factor oracle** → `0007`. §6's case exactly: proxy phase 0034
   evaluated a decoy challenge and answered **no**, recording that the decision
   is Control's (proxy D2) and that the contract *requires* the oracle — `200` on
   `/v1/auth/password` is documented as "the password was accepted". It touched
   no shared surface, so §4 never fired and nothing carried it downstream. Without
   it, a session implementing 0007 could add a decoy as an obvious
   anti-enumeration measure and ship a contract violation. Landed with the
   reasoning, the measured cost (1 Control call → ~121 per failed guess), that
   rate limiting is the control that actually works, and that changing it starts
   upstream (§3.2).

### Two defects fixed in the audit prompt itself

The prompt is re-run rather than completed, so a defect in it is permanent until
someone fixes it. Both are §7-style fixes to the prompt that will run next, not
implementations:

- **§2's bounded-set command** now carries `--full-history`, the measured 1-vs-18
  comparison, the shallow-clone check, and the PR-number-collision edge.
- **§5's stale-citation grep** now has a wrap-insensitive companion.

The prompt stays in `prompts/audit/`, unrenamed and unmoved.

### What the next run can skip, precisely

- The 21-merge derivation up to and including **#61** (`ba9ad26`). Take merges
  newer than that commit.
- The `D*` register check **for the ids listed above**, unless the proxy's
  register changes — cheap to re-confirm, and D17's withdrawn status is the one
  worth re-reading.
- The four unreachable fork-era merges. Their content is superseded and the
  finding is recorded; do not re-derive it.

**Do not skip** §5's contract check. It reads the current document rather than
the PR history, so it catches what §4 structurally cannot, and it is the half
that found the wrapped citation and the `{proxy_id}` drift this run.

- The **vendor-pin question** only until 0009 lands. Once it has re-vendored,
  re-check `contract/UPSTREAM` against upstream `main` on the §5 rule above:
  behind is fine where a prompt names the re-vendor, and a finding otherwise.

### Not found

**No §3.2 dependency.** Nothing this repository needs is missing upstream: every
obligation traced here had a real shape in `api/control.yaml` to land against.
Nothing was pushed upstream, no prompt was renumbered or renamed, no vendored
artifact was touched (`contract/` does not exist yet — it lands with 0002), and
no new prompt was queued: all three gaps were paragraphs in prompts that already
own their area, which is what §7 asks you to prefer.
