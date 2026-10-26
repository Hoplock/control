# 0035 — bounded lists 7: policy versions, a session's decisions and a finding's targets — Learnings

## Summary
- What shipped: `GET …/policy/bundles` in M26's shape (`{versions, truncated, next}`, newest first, no source read); `truncated` on `GET …/sessions/{session}/decisions` (bound 100); every publish-time finding names at most 100 targets or proxies beside `<name>_count` and `<name>_truncated`; `policyctl versions` reads every page, `explain -session` warns on a cut answer. Release `v0.22.0`, behaviour-only `### Breaking`. No migration.
- Key files: `internal/store/bundles.go`, `decisions.go`; `internal/authoring/checks.go` (`MaxFindingItems`, `capped`, `perBuildFindings`), `report.go`; `internal/decision/explain.go`; `internal/httpapi/north/policy.go`; `cmd/policyctl/main.go`.
- Store: `PolicyBundles().Page(ctx, tenant, Page)` replaces `List` (rows carry no `Source`); `Decisions().ListBySession` returns `([]Decision, bool, error)`, reading one past its bound.
- Services: `authoring.Service.Versions(ctx, tenant, store.Page) (store.Paged[Version], error)`; `decision.ExplainSession` returns `([]Story, bool, error)`.
- North: `versionList` (`policy_versions`, `[keyInt]`). The test: `policy/bundles` in `pagedLists`, `sessions/{session}/decisions` in `signalLists`; both left `pendingBounded`.
- **Per-build ruling: folded.** `algorithm.floor_undeclared/<rule>`, `algorithm.axis_emptied/<rule>/<axis>` and `algorithm.declaration_unknown/<rule>/<profile>` replace the per-proxy ids, each naming its builds in a capped `proxies`. An accepted old id accepts nothing; the 409 lists the new one.
- Decisions: none added or amended. M17, M26.3 and §5.2/§5.6/§10 revised in place (register unchanged).
- Cross-repo: a downstream sync for `hoplock/enterprise` is queued (see the PR): the prompt assumed 0028's sync covered the cap, and it did not. `ext/`, `server/`, `contract/` untouched.
- Gotcha: `satisfiability.proxies_unreported`'s `proxies` is a count, not a list, and keeps that name.
- NEXT (0036–0037): a signal-only list follows `ListByGrant`/`ListBySession` (read `limit+1`, return the bool). A finding with a new list takes `capped`.

## Details

### The per-build ruling
The prompt offered two rulings for the findings that were one per proxy per
rule: fold them, or keep them and say why their number is bounded. They are
not bounded by anything but the fleet. A finding per declaring proxy per rule
grows as proxies enrol (0031 made the proxy list a paged list for the same
reason), so 20 floored rules over 5 000 proxies with an old build would answer
100 000 warnings in every validate, upload, apply and activate, and the author
would accept each by id. So they are folded:

| Code | Was | Is | Parameters |
| --- | --- | --- | --- |
| `algorithm.floor_undeclared` | `…/<rule>/<proxy>`, `proxy_id` | `…/<rule>` | `rule`, `floor`, `proxies` (ids) |
| `algorithm.axis_emptied` | `…/<rule>/<proxy>/<axis>`, `proxy_id`, `offered` | `…/<rule>/<axis>` | `rule`, `axis`, `profile`, `floor`, `proxies` (`[{proxy_id, offered}]`) |
| `algorithm.declaration_unknown` (per build) | `…/<rule>/<proxy>`, `proxy_id` | `…/<rule>/<profile>` | `rule`, `profile`, `proxies` (ids) |

Each `proxies` is capped like any other list. An emptied axis stays one finding
per axis: the axes are the contract's five (`contract.Axes`), so the count is
bounded by the bundle, and an author's question is "which axis does this ban
empty, and where". The rule-level `algorithm.declaration_unknown/<rule>` (no
build declared, or none declared what it offers) is unchanged; the per-build
one takes the profile as its last part so the two never share an id when both
arise for one rule. The judgement is still per build — each declaration is
judged on its own, as authorize does — and M17's "per build where the fact is
per build" now says that the finding names each build rather than being one
per build.

**What happens to an accepted id.** Nothing stores acceptances but the
activation's audit record, and `Report.Unaccepted` ignores an accepted id that
does not arise. So an `apply -accept algorithm.floor_undeclared/r/p-1` that
passed under `v0.21.0` is refused `409 findings_not_accepted` under `v0.22.0`,
listing `algorithm.floor_undeclared/r`, which is accepted once for every build.
The records already written keep the ids they accepted. The CHANGELOG says so
under `### Breaking`.

**The one finding per target that stays** is `algorithm.policy_unmet`
(`…/<rule>/<target>/<axis>/<cause>`): one stored record about one target. It is
bounded by the unmet read (`DefaultUnmetLimit`, 1000 records, deduplicated per
target, axis and cause) and says when that read stopped
(`algorithm.policy_unmet_truncated`). It is not folded here. A bundle of many
rules reaching the same unmet targets multiplies it by the rule count; that is
bounded by the bundle and the read, and a later phase may fold it the same way
if it is found heavy. `algorithm.ban_unmatched`'s `rules` list is bounded by
the bundle (its body limit) and is not capped.

### The cap
`MaxFindingItems` (100) and `capped(params, name, list)` (`checks.go`). The list
keeps its own order; `<name>_count` and `<name>_truncated` are present on every
such finding, cut or not, as `decisions_truncated` is on a grant's view.
`satisfiability.no_proxy_provides`'s message named every proxy id, which was the
same estate-sized list again; it now gives the count, as the other messages
already did. There is no route that pages the rest of a cut list: the targets a
rule reaches are the inventory's, and the proxies are the fleet's, each paged.

### Choices the prompt left open
- **The cursor name** is `policy_versions`, not `versions`: the member is
  `versions`, but a later list of something else's versions would otherwise
  share the name.
- **The reader selects only what a version shows** (`versionColumns`) and scans
  with `scanVersion`; `bundleColumns` and `scanBundle` stay for `Get`,
  `GetActive` and `GetByHash`, which answer one version whole.
- **`ListBySession`'s bound stays the caller's argument** (the tests pass small
  ones); zero is `defaultDecisionLimit`, 100. The route passes zero.
- **`policyctl versions -json`** cannot print "the server's answer, unchanged"
  when the answer is several pages. It prints one answer in the server's shape,
  `{"versions": [...], "truncated": false}`, each version as the server wrote
  it. Nothing is printed until the last page is read, as 0034's CLI does, so a
  failure on page two is exit `2` and no partial history.
- **`policyctl explain -session`** warns on stderr in both output modes and
  names how many stories it was given; the exit stays `0`.

### Mutations the tests caught
- The version reader selecting `source` (`bundleColumns`/`scanBundle`):
  `TestAVersionPageReadsNoSource` fails on every row's `Source` and on the
  verbose plan's `Output:` line.
- `capped` returning the whole list: `TestEveryListAFindingNamesKeepsItsFirstHundred`
  fails at 101 and 300 for every code, and on the last item named. The test
  also pins `MaxFindingItems` at 100, so moving the constant fails it too.

### Tests added
- `internal/store`: `PolicyBundles.Page` joins `pagedReaders` (a version is
  numbered `newestFirst(id).UnixMicro()`, its digest is the id;
  `DeletePolicyBundle` and `BundlePageSQL` in `export_test.go`);
  `TestAVersionPageReadsNoSource` (a source over 1 MiB, the rows, and
  `EXPLAIN (GENERIC_PLAN, VERBOSE)`: an index scan backward on
  `policy_bundles_pkey`, no sort, no `source` in the output);
  `TestListBySessionSaysWhenItsBoundCutsIt`.
- `internal/authoring`: `cap_test.go` — every capped finding at 1, 100, 101 and
  300 items; no finding in a report over 300 targets and proxies names more
  than 100; two rules over 600 falling-short builds are one finding per rule
  (per rule and axis), ids naming no proxy. `checks_test.go`'s per-proxy tests
  now assert the folded shape; `service_test.go`'s no-proxy test reads the
  proxy from the parameters.
- `internal/httpapi/north`: `policy_page_test.go` (the version list's members
  and size with a large source; a session's 100 and 101 decisions, with and
  without `?limit=` and `?after=`); the bounded test's two rows.
- `cmd/policyctl`: `paging_test.go` — 501 versions over two pages against the
  real surface, in lines and `-json`; a stub server failing page two prints
  nothing; `explain -session` warns on 101 decisions and not on two.

### Cross-repo impact
The prompt said the cap needed no sync because 0028's sync (control-PR#97) had
told Enterprise about it. That sync's obligations name the list shape, the
limit and the observation split, and say nothing about findings. This phase also
changes three finding ids, which is what an acceptance is carried by. So a sync
for `hoplock/enterprise` is queued, for anything there that reads or renders
Control's publish-time report or carries acceptances. `ext.PolicyValidator` and
its `Finding` are unchanged; a validator's findings are its own.
