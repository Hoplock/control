# 0065 — Policy's refusals as codes

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — §2: **M21** (records and answers carry codes and data,
  never sentences; the console owns the sentence), **M3** (the compiler is a
  boundary with a closed vocabulary), **M11**, **M19** (the north-bound API is
  versioned; a new parameter moves it) and **M23**; §5.6 (validation,
  simulation and replay — what each refuses and why).
- `docs/learnings/` — read summaries; open `0005` (the compiler's rejections:
  `model.Rejection{Code, Rule, Line, Params}` and the `messages` map),
  `0022` (dry-run and replay refusals, and why a record replay cannot read is
  listed rather than skipped), `0045` (north-bound answer names) and `0053`
  **in full** — its Details list exactly what arrived as English only, and
  `ui/test/policy-codes.json` is how the console holds its sentences to the
  server's codes.
- `ui/DESIGN.md` §9 (the server does not localise; identifiers are data).

## Objective
Every policy refusal an operator meets in the console arrives as a stable
code and typed parameters, so the console builds the sentence (M21) and no
part of one is English the server wrote. Phase 0053 found four places where
it does not, and rendered each as "the server's words" rather than parse it:

1. **A compiler parameter that is itself prose.** `detail` on
   `policy.document_malformed` (the YAML parser's error, and "unknown day"),
   `route.filter_rule_invalid`, `route.restricted_command_invalid` and
   `obligation.contradicts_route` (`internal/policy/model/parse.go`,
   `internal/policy/compile/compile.go`). The console interpolates English
   into a translated sentence.
2. **An unreplayable record's reason.** `decision.Unreplayable.Reason` is
   `err.Error()` (`internal/decision/replay.go`). The console shows it as the
   server's words.
3. **A refused simulation.** `400 invalid_request` with
   `{field: body, problem: invalid, reason: <English>}`
   (`internal/httpapi/north/policy.go`, `authoring.ErrSimulation`). The
   console can say only "the request could not be read".
4. **A candidate diff that does not parse.** `POST …/policy/diff` answers
   `422 policy_invalid` with `{reason: <English>}` and no rejections, so the
   console can say only that it does not parse.

## In scope
- For (1): split each prose `detail` into its own codes, or into typed
  parameters a sentence can be built from — the YAML failure's kind and
  position, the unknown day's value, which filter-rule field is wrong and how,
  which obligation term contradicts which route field. Keep every existing
  code's meaning (`model` never reuses a code); new codes join `messages`,
  which `exhaustive` holds complete.
- For (2): a closed set of unreplayable causes (inputs that do not decode, no
  recorded instant, an address or grant instant that does not parse, a
  standing denial whose explanation disagrees), as `code` plus parameters on
  `Unreplayable`, `reason` kept beside them for policyctl.
- For (3): each `ErrSimulation` as `{field, problem}` in the grammar every
  malformed parameter already uses (M21), with `problem` values named here.
- For (4): the candidate's rejections, as `policy_invalid` carries them on
  upload, instead of a parse error's text.
- The console renders every new code from the catalogue: add each to
  `ui/src/screens/policyCodes.ts` and `en.json`, regenerate
  `ui/test/policy-codes.json` (`HOPLOCK_UPDATE_POLICY_CODES=1 go test
  ./internal/authoring -run TestTheConsolesPolicyCodesAreTheServers`), and
  extend it to the unreplayable causes and the simulation problems.
- `docs/API-VERSIONING.md` and the north-bound version, if a new parameter or
  member moves it (M19).

## Out of scope
- A registered validator's findings: their codes are their provider's, and
  the console renders them by the fallback by design.
- Any other screen's errors, and any locale other than English.

## Acceptance criteria
- No policy answer the console renders carries a parameter whose value is an
  English sentence: a test walks every `model.Code`'s parameters and every
  new code's, and fails on one named `detail` or `reason` that a sentence
  interpolates.
- `ui/test/policy.test.ts` still holds every code to a sentence naming only
  its parameters, now including the unreplayable causes and the simulation
  problems; the console's "server's words" rendering is gone, and a code the
  catalogue does not know still renders the fallback.
- policyctl and CI print the same English as before (`Message()`), or better.
- The browser checks stay green; the baseline changes only where a sentence
  replaced the server's words.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0065-policy-refusals-as-codes-learnings.md`, listing every
code added and every parameter renamed. A changed north-bound answer owes a
downstream sync for `hoplock/enterprise` (`docs/CROSS-REPO-PROTOCOL.md` §4.1).
**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
