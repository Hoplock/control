# 0035 — Bounded lists 7: policy versions, a session's decisions and a finding's targets

## Read first
- `docs/PROTOCOL.md`: §1, §3, §4, §5.
- `docs/PLAN.md`:
  - §2: the register, then **M26** (point 3's signal-only lists are half of
    this phase), **M4** (every decision is explainable and addressable),
    **M17** (what a finding about a target or a proxy says), **M21** (a
    finding is a code with typed parameters), **M11** and **M23**;
  - §3: `internal/authoring`, `cmd/policyctl` and `internal/httpapi/north`,
    the paragraph on lists;
  - §5.6: the policy lifecycle and the publish-time report;
  - §10: this phase's row.
- `docs/learnings/`: every summary. Read `0029`'s learnings whole. In
  `0028`'s, read the inventory rows for these routes and for the publish-time
  report. Read `0022`'s summary for the report's findings.
- Code:
  - `internal/httpapi/north/policy.go`: `handlePolicyList`,
    `handleExplainSession`;
  - `internal/authoring/service.go`: `Versions`, `versionOf`;
  - `internal/authoring/checks.go`: every finding whose parameters carry
    `targets` or `proxies` (search for `"targets":` and `"proxies":`), and
    the per-proxy findings;
  - `internal/store/bundles.go`: `List`, `bundleColumns`;
  - `internal/decision/explain.go`: `ExplainSession`;
  - `internal/store/decisions.go`: `ListBySession`, `defaultDecisionLimit`;
  - `cmd/policyctl/main.go`: `versions` and `explain`;
  - 0029's `page.go`.

## Context estimate
- **Reads:** this prompt, M26 and the named decisions (~300 lines), the
  summaries, 0029's learnings, the functions above (~500 lines with
  `checks.go`'s findings), and 0029's helpers.
- **Changes:** two handlers; one paged store reader and one signal; the
  report's finding parameters; `policyctl`'s two commands; tests; the
  pending list; `CHANGELOG.md`.
- **Why it fits:** each change is small, and they share one reader: the
  policy author, who reads versions, explains a session, and reads the
  report. `checks.go` is read only where the findings are built.

## In scope

### 1. `GET …/policy/bundles`
- List `versions`, key `[keyInt]` (version), newest first, primary key
  `(tenant, version)`, read backwards.
- **Do not read a version's source.** `bundleColumns` selects it today, and
  `versionOf` throws it away. The paged reader selects only what a version
  shows.
- The answer is `{versions, truncated, next}`.
- `authoring.Service.Versions` becomes the paged read. `PolicyBundles().List`
  has no other caller; delete it.

### 2. `GET …/sessions/{session}/decisions`: a signal
A session's decisions are bounded by its hops, not by the estate or by time,
so the list **needs only the signal** (M26.3).
- `ListBySession` reads one past its bound (`defaultDecisionLimit`, 100) and
  reports it.
- The answer gains `truncated`.
- It takes no `limit` and no `after`.

### 3. The publish-time report: a finding names at most 100 targets
Every finding whose parameters list targets or proxies is sized by the
estate. A rule over `env=prod` with 300 000 unprobed appliances names all of
them in one finding, in the answer to every validate, upload, apply and
activate (and in `409 findings_not_accepted`).
- **Each such list keeps its first 100**, in its existing order, beside
  `<name>_count` (the whole number) and `<name>_truncated`. The message
  already gives the count. It needs only the signal (M26.3).
- **Rule on the per-proxy findings** (one finding per proxy per rule, in the
  floor and profile checks). Either fold them into one finding per rule
  with a capped `proxies` list, or keep them and say why their number is
  bounded. Record the ruling in the learnings. A finding's id is what an
  author accepts it by, so if you fold, say what happens to an accepted id.
- Hold the cap with a test that fails at 101.

### 4. `policyctl`
- **`versions`** follows `next` until `truncated` is false and prints every
  version as today. It takes no limit flag; a pipeline wants the whole
  history.
- **`explain -session`** prints a warning to stderr when the answer says
  `truncated`, and exits `0`. The answer is still the server's.

### 5. The enumerating test
- `policy/bundles` moves to `pagedLists`, seeded by uploading two versions.
- `sessions/{session}/decisions` moves to `signalLists` (`truncated`),
  seeded by one decision.

## Out of scope
- `POST …/policy/simulate`. Its replay has its own limit (≤ 10 000) and
  already says `truncated`; its dry-run cases are the request's own.
  0028's inventory rules it exempt.
- `…/policy/bundles/{version}`, `…/diff` and `…/active`: one object each.

## Acceptance criteria
- The version list answers in M26's shape and reads no source. Assert it
  with a version whose source is large.
- The session list says `truncated` past 100 decisions.
- No finding names more than 100 targets or proxies, and each that is cut
  says so with its count.
- `policyctl versions` prints every version over several pages, and
  `explain -session` warns on a cut answer.

## Tests
- `internal/store`: the version `Page` and `ListBySession`'s signal.
- `internal/authoring`: the caps, and the per-proxy ruling.
- `cmd/policyctl`: both commands against a test server.
- `internal/httpapi/north`: the handlers and the bounded test.

## Release (M23)
The next MINOR after the latest tag. Under `### Breaking`:
- the version list is at most 500 by default, with `truncated` and `next`;
- the session list gains `truncated`;
- a finding's `targets` or `proxies` holds at most 100, beside `_count` and
  `_truncated`, and the per-proxy ruling.

## Cross-repo impact
- `hoplock/enterprise`: a registered `ext.PolicyValidator`'s findings are its
  own and unchanged. If its console renders Control's findings, 0028's sync
  told it about the cap. Otherwise none.
- `hoplock/proxy`: none.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `prompts/implemented/`, and add
`docs/learnings/0035-bounded-lists-policy-versions-and-findings-learnings.md`.
