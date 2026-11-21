# 0065 — policy's refusals as codes — Learnings

## Summary
- **Shipped (`v0.52.0`, MINOR, behaviour-only `### Breaking`; no migration; `ext`/`server`/contract untouched; north API `7`→`8`, `north_api_min` still `1`):** no policy refusal carries prose. Compiler: no `detail` param; YAML errors read into codes in `internal/policy/model/yamlerror.go`. Replay: `decision.Unreplayable{Code, Parameters}`. Simulation: `authoring.ErrSimulation{Field, Problem}`. Diff of an unparsable candidate: `*InvalidError` → `422 policy_invalid {rejections, digest}`.
- **Worded parameters (new):** a closed-set param travels as an identifier; `model.Enumerations()` lists them, `Message()` words them (`words.go`), the console uses an ICU `select` (`policyCodes.ts` `WORDED`, passed unisolated). Only `problem`, `found`/`expected`, `vocabulary`.
- **Codes and params:** every one added or renamed is in Details. Unreplayable causes are `unreplayable.*`, not `replay.*`: `replay` is a console catalogue namespace (0053's gotcha).
- **Older clients:** `internal/httpapi/north/policycodes.go` serves a client declaring `≤7` the old shapes (old codes with `detail`, reason-only unreplayable, `{body, invalid, reason}`, `{reason}`). `docs/API-VERSIONING.md` now names this as additive.
- **Tests:** `TestNoPolicyParameterIsProse`; `TestEveryProblemTheParserCanReportHasAKind` reads yaml.v3's source, so an upgrade with new wording fails; `ui/test/policy-codes.json` gains `enumerations`, `unreplayable`, `simulation_problems`.
- **Decisions:** none added or amended; M21 point 3, M19's version paragraph, §5.6 and §10 revised in place; `ui/DESIGN.md` §9 gains the worded-value rule.
- **NEXT:** a new closed-set param joins `worded` (Go) and `WORDED` + a select (console). Retire `policycodes.go` only when `north_api_min` passes 7. A new refusal on this surface is a code on the day it is written.

## Details

### Every code added, and every parameter renamed

Compiler (`internal/policy/model/reject.go`; `policy-codes.json` holds them):

| Code | Parameters | Was |
| --- | --- | --- |
| `policy.document_malformed` | `problem` ∈ `tab_indentation structure unterminated character encoding tag alias unreadable` | `detail` (the library's English); also every case below |
| `policy.key_unknown` (new) | `key` | `document_malformed` "field x not found in type model.Rule" |
| `policy.key_duplicate` (new) | `key`, `other_line` | `document_malformed` "mapping key … already defined" |
| `policy.value_wrong_type` (new) | `found`, `expected` ∈ `string integer number boolean list mapping null timestamp other` | `document_malformed` "cannot unmarshal !!str into []string" |
| `policy.value_unknown` (new) | `value`, `vocabulary` (23 identifiers, `words.go`), `allowed` | `document_malformed` from `decodeEnum`, the username source, and compile's unknown day |
| `policy.duration_malformed` (new) | `value` | `document_malformed` |
| `match.invalid_time_window` (new) | `value` | `document_malformed` |
| `route.filter_rule_invalid` | `index`, `field` (`match`, `action`) | `detail` |
| `route.restricted_command_invalid` | `index`, `executable`, `field` (`executable`, `form`, `argv`) | `detail`; now only "names no field" |
| `route.restricted_command_field_unexpected` (new) | `index`, `executable`, `field`, `form` | `restricted_command_invalid` |
| `route.restricted_argument_invalid` (new) | `index`, `executable`, `argument`, `field` (`kind`, `value`, `values`) | `restricted_command_invalid` |
| `route.restricted_argument_field_unexpected` (new) | `index`, `executable`, `argument`, `field`, `kind` | `restricted_command_invalid` |
| `obligation.contradicts_route` | `obligation`, `field` (`require_session_capture`) | `detail` |
| `obligation.approvers_unexpected` (new) | `obligation` | `contradicts_route` (it was never about the route) |

Replay causes (`decision.UnreplayableCodes`): `unreplayable.inputs_undecodable`,
`unreplayable.instant_missing`, `unreplayable.source_address_invalid`
(`address`), `unreplayable.grant_instant_invalid` (`grant`, `field`,
`value`), `unreplayable.explanation_missing`,
`unreplayable.standing_denial_disagrees` (`effect`).

Simulation problems (`authoring.SimulationProblems`): access's `required`,
`invalid`, `ambiguous`, `not_after_start`, plus `nothing_asked` and
`not_in_vocabulary` (new). Fields are the request's own paths, zero-based like
the claim mapping's: `version`, `body`, `replay.from`, `replay.to`,
`cases[i].now`, `cases[i].context.source_addr`, `cases[i].subject.auth_method`,
`cases[i].grants[j].origin`, `cases[i].grants[j].expires_at`. The replay window
check, one condition before, is now three (from missing, to missing, not
after).

### Reading yaml.v3's errors

yaml.v3 keeps only a `TypeError`'s strings, so an `UnmarshalYAML` hands Parse a
rejection by writing `structuredMark` (a NUL-prefixed marker), the code and
the params as JSON into the string; `rejectionOf` reads it back. The library's
own strings are read by shape (`field … not found in type`, `mapping key …
already defined at line N`, `cannot unmarshal TAG [`v`] into TYPE`), and a
scanner/parser problem by an exact-string table, with format-string families
(`unknown anchor`, `anchor … contains itself`, `input error:`) by prefix.
Expected shapes come from the Go type yaml.v3 prints, resolved against every
named type reachable from `Bundle` by reflection (`schemaShapes`).

- A tab at the start of a line is reported as "found character that cannot
  start any token". `yamlRejection` checks the line's indentation in the
  source and says `tab_indentation` instead.
- `rules: [` at EOF is `structure` ("did not find expected node content"),
  not `unterminated`; `rules: [ x` is `unterminated`. Tests pin both.
- The source-reading test skips if `go list -m` cannot find the module, and
  excuses the library's internal-misuse strings by name.

### The older-version shape

The API-VERSIONING rule for a changed meaning is "moves the version and
raises `north_api_min` after the window", which would have meant serving a
client of `7` the new codes for ≥ 2 MINORs. Instead every reshaped answer is
still given to that client in its old shape, so the change is additive by the
doc's own definition ("served exactly what it was served before"). The prose
inside (`detail`, `reason`) is the new English; the doc allows prose to be
reworded between versions. The unreplayable members carry `since:"8"` and
are zeroed for an older client, so the projection guard (0045) still sees
them as additive members.

### Console

- `rejectionValues(r, worded)` leaves worded params unisolated: an isolate
  around a select value matches no option.
- An unparsed candidate's Changes tab keeps its empty state and sets the
  rejections at their lines below it (`Rejections`, id prefix
  `policy-candidate-changes`). The empty state's body says so.
- A refused simulation reads its sentence from `simulationRefusalText`, shown
  through `ErrorMessage`'s new optional `text`; anything else falls back to the
  code's sentence.
- `.serverWords` is gone with the last use. The baseline did not change: no
  baseline image shows an unreplayable record, an unparsed diff or a refused
  simulation. The PR's screenshots show them, substituted in the browser.

### Noticed, not changed

- policyctl's `unreplayable` line still prints the reason only; adding the
  code would add a TSV column a script may read.
- The fixture's refused candidate (0053) meets none of the changed codes, so
  `policy-candidate-rejected` is unchanged.

### Cross-repo

North API `8`, new codes and causes, and the shapes above change what
Enterprise renders and what a host's own client (which declares no version)
is answered. A downstream sync for `hoplock/enterprise` is queued with the
PR. Nothing is owed to `hoplock/proxy`.
