# 0067 — a registered validator's finding at its rule's line — Learnings

## Summary
- **Shipped (`v0.54.0`, MINOR; no migration; north API unchanged at `8`; `server`/contract untouched; `ext` by one doc comment):** a finding in the publish-time report whose `rule` names a rule of the compiled bundle carries that rule's line, whoever said it. Control resolves it from the compiled program; a validator sets the rule's id in `Path` and nothing else.
- **Where:** `locate` (`internal/authoring/report.go`), called once in `Service.findings` after every validator and before `sortFindings`; `validatorFinding` (`service.go`) is the one place an `ext.Finding` becomes a report `Finding`. Every answer carrying the report gets it: validate, upload, apply, and activation's `409`/`422` lists.
- **Unchanged:** `Rule` is `Path` verbatim; the id is `<code>/<provider>/<path>`; severities, order, acceptance, M26.3's bounds. A `Path` naming no rule (empty, `/rules/0`, an unknown id) keeps `line` absent. The line is the rule's `- id:` line, as a rejection's is.
- **Also placed:** `satisfiability.no_proxy_provides`, `…target_cannot_take`, `…target_unproven`, which set `Rule` and no `Line` before.
- **Not taken:** `Line int` on `ext.Finding` (the requester's alternative): it would make every validator parse YAML positions the compiler already holds, and let a line disagree with the rule its `Path` names.
- **Version decision:** none moved. `line` is a member every served version has, meaning what it always meant; a client declaring `1` reads it today. Rule added to `docs/API-VERSIONING.md` "What additive means".
- **Decisions:** none added or amended; register unchanged. PLAN §3, §5.6 and §10 (0067's row) revised in place.
- **Tests:** `TestEveryFindingThatNamesARuleIsAtThatRulesLine` (internal; every rule-level code must fire, and a new code fails until the fixture fires it or it is listed in `documentLevel`); `TestAValidatorsFindingIsPlacedAtTheRuleItsPathNames` (service, DB); `TestAValidatorsFindingCarriesItsRulesLineOverTheWire` (north, versions `8` and `1`); `ui/test/policy.test.ts` (`sourceNotes`).
- **NEXT:** a new rule-level check sets `Rule` to the rule's id (its `Line` may be left to `locate`); a document-level one joins `documentLevel` in `report_test.go`. Nothing may append a finding after `locate`.
- **Cross-repo:** a downstream sync for `hoplock/enterprise` is queued (see the PR); proxy: none.

## Details

### Why `locate` runs after every source, not inside each check

The prompt's shape. A check that already set `Line` is left as it is (it is
the same rule's line, and the invariant test holds that), so the ten
Control findings that set `Line: rule.Line()` themselves were not touched.
Removing those now-redundant assignments was considered and not done: they
cost nothing, and they keep each check readable on its own.

### What the document-level codes are

`satisfiability.proxies_unreported`, `satisfiability.contract_version_unsupported`,
`algorithm.policy_unmet_truncated` (about the fleet or the read) and
`algorithm.ban_unmatched`, which is about a banned *name* and lists the
rules carrying it in its `rules` parameter rather than naming one in `rule`.
A ban typo is therefore still not set beneath any line. Placing it at each
rule it lists would be a finding at several lines, which the report's shape
(`rule`, `line`) cannot carry; out of scope here, and no queued phase owns it.

### The invariant test's fixture

`everyKind` (checks_test.go) plus two facts: a target the floored rule
reaches with no record at all (fires `target_unproven` and
`target_not_reported`) and a third build whose declaration has floors and no
profiles (fires the per-build `declaration_unknown`). Expected lines are read
from the source text (`- id:` lines), and the program's own `Line()` is
checked against them first, so the expectation does not come from the code
under test. Mutations run: `locate` a no-op fails it (six findings); removing
the call from `Service.findings` fails the service tests (validate and
upload, and the three satisfiability assertions).

### The version decision, in full

`docs/API-VERSIONING.md` "What additive means" lists a new member, a new
route, a new header, an open map's keys, a new code or closed-set value, a
changed meaning and a reshaped answer. This is none of them: `line` is on
`Finding` in every served version (`authoring.Finding` carries no `since`),
Control's own findings have carried it since `1`, and its meaning — the line
of the rule `rule` names — is unchanged. A client of `1` already handles a
finding with and without it. The new bullet says so, so the next session
does not re-derive it. The north test sends the request undeclared and
declaring `1` and asserts the same body.

### The console

No code under `ui/src` changed: `sourceNotes` sets every finding carrying
`line`, and a validator's code renders by the fallback naming its provider
and code. The new unit test holds both, rendering the note through
`I18nProvider`. The PR's screenshots substitute the validate answer in the
browser (Playwright `page.route`); no test server registers a validator.

### Noticed, not changed

- `policyctl`'s text output still prints no place for any finding; `--json`
  carries `line`.
- A validator whose `Path` is a rule id with different case or whitespace is
  not placed: the lookup is exact, as the id is.
