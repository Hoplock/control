# 0067 — A registered validator's finding at its rule's line

## Read first
- `docs/PROTOCOL.md` — session workflow; §3 on `ext/` as a compatibility
  promise (this phase changes one doc comment there and no signature); §8's
  "A console phase shows what it built" (this phase changes no screen's code,
  but it changes what one shows).
- `docs/PLAN.md` — §2: **M3** (a closed vocabulary buys explanations that
  name a rule; the compiler's rejections carry the rule and its line),
  **M4**, **M15** (Enterprise extends Control through `ext` and nothing else;
  Control reads a validator's findings, it never asks a validator to know
  Control's internals), **M17** (the satisfiability findings, three of which
  this phase also locates), **M19** (whether a member set in more cases moves
  the north-bound version), **M21** (a finding is a code and parameters; the
  console builds the sentence and the place) and **M23** (every phase is a
  release); §3 at `internal/authoring` (the publish-time report and its three
  sources); §5.6, "Validation answers two questions, in two vocabularies"
  (severities, finding ids, acceptance, "a finding is sized by the bundle").
- `docs/API-VERSIONING.md` — "What additive means".
- `ext/policy.go` — `Finding` and `PolicyValidator`, doc comments included.
- `docs/learnings/` — read summaries; open **in full** `0053-console-policy`
  (a finding is set beneath its line in a version's source and in the
  candidate editor only when the report carries `line`: `sourceNotes` in
  `ui/src/screens/PolicyParts.tsx`; and "the compiler reports a rule's
  rejection at the rule's `- id:` line") and `0022-audit-0019-policy-lifecycle`
  (a validator's finding, its id and its severity). Read the summaries of
  `0004` (the `ext` seam), `0035` (findings sized by the bundle) and `0066`
  (the visual editor sets findings on the card of the rule they name, by
  `rule`, and needs nothing from this phase).
- In **`hoplock/enterprise`**: https://github.com/Hoplock/enterprise/pull/76,
  `## Upstream request`, item 1 — the need this phase answers. Read only that
  section. Its requester's decisions are cited by id, never restated: **E2**
  (only `ext/` and `server/` are a host's surface, so the answer cannot ask a
  validator to reach into Control) and **E3** (Enterprise builds against a
  pinned release, so the line reaches it only with the release this phase
  cuts).

## Why this phase exists
`hoplock/enterprise` asked for it (enterprise#76, queued as its
`prompts/upstream/queued/enterprise-PR#76-registered-finding-line.md`).

The publish-time report's `authoring.Finding` has `Rule` and `Line`, and the
console sets a finding beneath the line it names — in a version's source and
in the candidate editor — only when `line` is present. Control's own
rule-level findings set `Line: rule.Line()` from the compiled rule
(`internal/authoring/checks.go`). A registered `ext.PolicyValidator`'s never
do: `ext.Finding` has `Severity`, `Code`, `Path`, `Detail` and `Message` and no
line, and `Service.findings` (`internal/authoring/service.go`) copies `Path`
into `Rule` and stops there.

**What downstream cannot do until this lands.** An Enterprise guardrail (its
0009) that finds something about a rule sets `Path` to the rule's id, as its
prompt requires. Its finding is listed in the report and in the activation
dialog with the rule's name, and is never shown beneath that rule in the
source view or the candidate editor, where Control's own findings are. An
author has to find the rule by hand. Enterprise's PLAN §4 and its 0009 say
so, and forbid working around it there — no line computed and carried in
`Path`, which would change the finding's id, or in `Detail`, which the console
shows as a parameter and never as a place.

Reading the code to place this turned up the same gap in three of Control's
**own** findings: `satisfiability.no_proxy_provides`,
`satisfiability.target_cannot_take` and `satisfiability.target_unproven` set
`Rule: sat.RuleID` and no `Line` (`checks.go`, `satisfiability`), so they too
are listed and never placed. The shape below locates them by the same step,
and this phase owns that too: it is one rule about the report, not two.

## Objective
**Every finding in the publish-time report whose `rule` names a rule of the
compiled bundle carries that rule's line — whoever said it.** Control
resolves the line itself, once, from the program it already compiled. A
validator keeps saying only what it knows: the rule's id, in `Path`.

The shape, in this repository's vocabulary:

- A registered validator's finding whose `Path` equals the id of a rule in
  the compiled program gets `Line` = that rule's `compile.Rule.Line()` — the
  same line Control's own findings about that rule carry, and the `- id:` line
  the compiler's rejections use.
- `Rule` stays `Path`, verbatim. The id stays
  `findingID(f.Code, provider, f.Path)` — `<code>/<provider>/<path>` — so an
  acceptance a CI run carries forward still accepts the same finding.
- A `Path` that names no rule — empty, a document pointer, an id the bundle
  does not hold — keeps `Line` 0, which the report omits (`line,omitempty`).
  Control never guesses a place.
- **`ext/` does not change**: no field, no method, no signature. The
  alternative the requester named — `Line int` on `ext.Finding` — is **not**
  taken. It would make every validator parse YAML positions that Control's
  compiler already holds, and it would let a validator's line disagree with
  the rule its `Path` names, which the report could then only pick between.
  Only `Finding.Path`'s doc comment changes, to say what Control does with a
  rule's id.

```go
// internal/authoring — one step in Service.findings, after Control's checks
// and every registered validator have appended, before sortFindings.

// locate sets each finding's line from the rule it names. A finding whose
// Rule names no rule of the program — the document's scope, or a validator's
// Path that is not a rule's id — keeps Line 0, which the report omits. A line
// a check already set is the same rule's line and is left as it is.
func locate(fs []Finding, prog *compile.Program) {
	lines := make(map[string]int, prog.Len())
	for i := 0; i < prog.Len(); i++ {
		r := prog.Rule(i)
		lines[r.ID()] = r.Line()
	}
	for i := range fs {
		if fs[i].Line == 0 && fs[i].Rule != "" {
			fs[i].Line = lines[fs[i].Rule]
		}
	}
}
```

Rule ids are unique within a bundle (`rule.id_duplicate` refuses a second), so
the lookup is unambiguous, and a validator runs only on a source that
compiles (§5.6), so the program always exists when it does.

## In scope
- `internal/authoring/service.go` — call `locate(out, cand.Program)` in
  `Service.findings` after the validator loop and before `sortFindings`. The
  same report then reaches every answer that carries it: `POST …/policy/validate`,
  `POST …/policy/bundles` (upload), `POST …/policy/apply`, and the findings
  listed in an activation's `409 findings_not_accepted` and `422
  policy_blocked` (`internal/httpapi/north/policy.go`), which are computed
  again at activation (§5.6).
- `internal/authoring/report.go` — `locate` (or beside the service, as fits),
  and `Finding`'s comment on `Rule` and `Line`: a finding that names a rule of
  the bundle carries that rule's line, whichever source said it; both are
  empty at document scope.
- `ext/policy.go` — `Finding.Path`'s doc comment only: when `Path` is the id
  of a rule in the bundle, Control's report places the finding at that rule's
  line, so a validator sets the rule's id alone and never computes a line or
  carries one in `Path` or `Detail`. No other line of `ext/` changes.
- `docs/PLAN.md`, revised in place (`docs/PROTOCOL.md` §3): §3's
  `internal/authoring` bullet, §5.6's validation paragraph (a finding about a
  rule is placed at that rule's line, whoever said it, resolved by Control
  from the compiled program; a validator's `Path` that names no rule is not
  placed), and 0067's §10 row. No decision is added, amended or withdrawn.
- `CHANGELOG.md` — the release's entry (below).

## Out of scope
- **Any change to `ext/` beyond the one doc comment**, and any change to
  `server/` or the vendored contract.
- Finding ids, codes, severities, the report's order, acceptance by id, the
  bound on a finding's items (M26.3) — all unchanged.
- **A line inside a rule.** A finding is placed at the rule's `- id:` line, as
  a rejection is; locating a key within the rule is not this phase.
- Locating a validator's `Path` that is anything but a rule's exact id: a
  document pointer, a label or scope declaration's name, a prefix. Those stay
  unplaced, and a later phase that wants them placed owns that shape.
- **The console's code.** `sourceNotes` already sets every finding that
  carries `line`, and the visual editor (0066) already sets findings on the
  card of the rule they name. No file under `ui/src` changes, and no
  screenshot baseline moves.
- `policyctl`'s text output, which prints a finding's severity, id and
  message and no place; `--json` carries `line` as the server sends it.

## Acceptance criteria

**The report** (`internal/authoring/service_test.go`, `report_test.go` as fits)
- A registered validator's finding whose `Path` is the id of a rule in the
  bundle carries that rule's `Line()` — the same value a Control finding about
  that rule carries — from `Validate`, `Upload` and the activation refusals
  (`NotAcceptedError`, `BlockedError`).
- Its `Rule` is the `Path` verbatim, and its id is `<code>/<provider>/<path>`
  exactly as before. The existing acceptance tests pass unchanged: a warning
  accepted by the id it had before this phase is still accepted.
- A validator's finding whose `Path` is empty, a document pointer
  (`/rules/0`), or an id no rule in the bundle has carries no line, and its
  JSON has no `line` member.
- `satisfiability.no_proxy_provides`, `satisfiability.target_cannot_take` and
  `satisfiability.target_unproven` carry their rule's line.
- **The invariant, as one test.** Over a bundle that makes every rule-level
  check Control has fire, with a validator registered that names a rule, a
  document pointer and nothing: every finding whose `rule` names a rule of the
  program carries exactly that rule's line, and every finding carrying a line
  names a rule. A new check that forgets its line fails here, not in review.

**The north-bound surface** (`internal/httpapi/north`, beside
`lifecycle_test.go`)
- `POST …/policy/validate` with a validator registered answers that
  validator's finding with `"rule": "<id>"` and `"line": <n>` for a rule the
  bundle holds, and with neither `line` nor any other member changed for a
  `Path` that names none.
- The version decision is recorded: per `docs/API-VERSIONING.md`, `line` is a
  member every served version already has on a finding, with the meaning it
  always had (the line of the rule `rule` names), and Control's own findings
  have carried it since version 1, so a client of any served version already
  reads it. This phase's expectation is therefore that it moves **no** version.
  Confirm that against "What additive means"; if the session finds it does
  move the version, it moves it by one, `north_api_min` stays `1`, and
  `docs/API-VERSIONING.md`'s history says why.

**The console** (`ui/test/policy.test.ts`)
- `sourceNotes` sets a validator's finding that carries `line` beneath that
  line, its text by the fallback naming its provider and code; one that
  carries no line is not set. This tests what already works, so that it keeps
  working.
- Because what a screen shows changes, the PR shows, per `docs/PROTOCOL.md`
  §8, the candidate editor with a registered validator's finding set beneath
  its rule's line, light and dark, made by substituting the report in the
  browser and captioned as substituted — no test server registers a
  validator, and none may be added to a listener for this (§3's
  debug-endpoint rule).

**Everything else**
- `go build ./...`, `go vet ./...`, `go test ./...`, `golangci-lint run`, the
  console's unit tests and browser checks, and `make release-check` pass.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0067-registered-finding-line-learnings.md`. Its summary says
that a finding naming a rule of the bundle carries that rule's line whoever
said it, that Control resolves it from the compiled program, and that a
validator sets the rule's id in `Path` and nothing else.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names. `ext/` changes by a doc comment, which
`apidiff` does not see, so there is no `### Breaking`. The entry says what a
host's validator now gets: its finding placed at the rule its `Path` names.

**Cross-repo.** This phase answers an upstream request, so it is itself a
change a consumer must catch up with, and it **owes a downstream sync to
every consuming repository once merged — including `hoplock/enterprise`, the
one that raised the request**, which is the easiest to forget because it is
already waiting (`docs/CROSS-REPO-PROTOCOL.md` §4.1, §5, "The PR that answers
an upstream request is not a sync"). Under `## Cross-repo impact` the PR
states, and its queued `prompts/downstream/queued/control-PR#<n>-….md`
carries, for `hoplock/enterprise`, from the release this phase cuts and never
from `main`:

1. the pin, and what the binary now does: a registered validator's finding
   whose `Path` is a rule's id is set beneath that rule's line in the report,
   in a version's source and in the candidate editor; one whose `Path` names
   no rule is not; the id, the severity, the acceptance and `ext/` are
   unchanged;
2. its PLAN §4 (the `PolicyValidator` bullet, "No validator's finding is set
   beneath a line yet") and its 0009 ("A guardrail's finding is not set
   beneath a line at …") rewritten to say that from this release a
   guardrail's finding is placed by its `Path`; that the prohibition on
   computing a line here — in `Path` or in `Detail` — stands, now because
   Control already does; and that a 0009 acceptance criterion holds a
   guardrail about a rule to being placed at that rule's line against the
   pinned release;
3. where they point at `prompts/upstream/queued/enterprise-PR#76-registered-finding-line.md`,
   point at `prompts/upstream/implemented/`, where it is by then.

`hoplock/proxy`: none — no contract change, and the proxy cites none of
Control's queued phases.
