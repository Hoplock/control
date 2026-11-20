# 0064 — one contract version, end to end — Learnings

## Summary
- **Shipped (`v0.51.0`, MINOR, `### Breaking`; no migration; contract still `4.7.0`; `ext`, `server` untouched):** this server serves policy vocabulary `6` only. Any other declared `policy_version`, lower or higher, gets `500 policy_version_unsupported` naming the declared number, the served one and the `proxy_id`. It is checked after validation and before anything is read, deny routes included. Absent or `<1` stays `400`. Never `401`, never a thinned `200`. The tiers (`4`/`5`/`6`, `requiredVersion`) are gone, and the assembly takes no version.
- **The numbers:** `internal/contract/version.go` is the only place either is spelled (`PolicyVersion`, `PolicyVersionMinimum`, `DocumentVersion`, `VendoredCommit`). `TestTheNumbersAreTheVendoredDocuments` reads both prose statements of the current vocabulary, `policy_version`'s `example` and `minimum`, `info.version` and `contract/UPSTREAM`. `TestTheTieFailsWhenTheDocumentMoves` proves that each move of the document is caught. `TestNoSecondVersionLiteral` (repo root) refuses either number anywhere else, Markdown, `contract/` and `dist/` excepted.
- **No thinning path:** `TestOnlyTheGateReadsTheDeclaredVocabulary` (an AST test) allows only `validate`, `Authorize`, `checkVersion` and `newRecordedInputs` to read the declared number. `assembleSnapshot` takes no version.
- **Conformance layers:** the expectation key `authorize.vocabularies` is required: `single` here, `per-route` for the mock. The contract layer runs on both servers: absent → `400`; the field's minimum refused or answered unthinned; the minimum refused for a `brokered-certificate` route and for a floor or ban route; a refusal names the declared number. The `(single)` cases run only here: lower and higher refused on the negotiation, direct and deny routes, naming both numbers and the proxy. The `(per-route)` case, one below the current refused for a floor or ban and served for the rest, runs only on the mock. `TestEachServerSaysWhichVocabulariesItAnswers` pins which file says which. 62/62 against this server locally; the mock leg is graded in CI only (Details).
- **Wrong build made visible (north API `6`→`7`, chosen by the user):** `fleet.RecordDeclaredVocabulary` writes the declared number to `proxies.contract_version` before the gate, bounded like the algorithm declaration (M5). `fleet.VocabularyUnsupported` is the one judgment. It feeds `contract_version_unsupported` on `GET …/proxies` and in the fleet summary (`since:"7"`), the health reason `proxies_contract_unsupported` (fleet `degraded`), the pre-publish verdict, and the warning `satisfiability.contract_version_unsupported`. The console shows `Unsupported` (outage tone).
- **Untouched on purpose:** the north-bound API's own range and negotiation (M19; it moved to `7` by its own rule, nothing folded) and the registration protocol's version. The `device_field.` namespace and the per-level floor and per-proxy ban rules stay capability checks (M17), never versions.
- **NEXT contract sync that moves a number:** `TestTheNumbersAreTheVendoredDocuments` fails and says which. Move the constant in `version.go`. If the vocabulary moved, every proxy still declaring the old number is refused from that deploy on. Ship the matching proxy build with it, decide what each new response field means for `assembleSnapshot`, and expect the fleet view to name the stragglers. Also revisit pdpconform's `(per-route)` case: it assumes the floor and bans are the newest additions.
- **Cross-repo:** a downstream sync is queued for `hoplock/enterprise` (north API `7`, the new reason, members and finding, and the single-version south-bound). Nothing is owed upstream.

## Details

### What the audit found

The prompt calls this an audit that runs last and may find little. It found:

- one `PolicyVersion` constant, but defined as `VocabularyAlgorithmFloor`, with
  the tier constants `VocabularyBaseline`/`VocabularyBrokeredCertificate`
  beside it and nothing tying any of them to the document (the comment said
  so: "nothing ties the two together but this line until phase 0064");
  `DocumentVersion` was already tied by a test (0046);
- the tiered gate 0008 built and 0019 filled (`requiredVersion`, run after
  assembly) — refusing, never thinning, but answering `4` and `5` per route;
- version literals: the console's three e2e fixtures (`policy_version: 6`),
  `cmd/pdpconform/checks_errors.go` (`{"policy_version": 4}`), the fleet and
  store tests (`ContractVersion: 4`, `5`), and the chrome unit test's
  substituted instance answer (`contract: "1.4.0"`), which the new check
  caught on its first run;
- `proxies.contract_version`, "the enrolled contract version" the prompt calls
  operational data, was set by nothing on the wire: `fleet.Registry.Enroll`
  and `Heartbeat` have no production caller and seed does not set it, so the
  fleet view showed `0` for every proxy. A readiness signal had to start
  being recorded before it could be surfaced; the authorize request is the
  only place a proxy states its vocabulary, so that is where it is recorded.

### The refusal, and why it runs first

`checkVersion(req)` runs after `validate` and after the vocabulary is recorded,
before the budget, the standing denials, the program or the graph. Running it
after evaluation (where the tiered gate ran) would answer a wrong build `401`
on a denied target and `5xx` on an allowed one — the nondeterminism the
conformance suite's `(single)` deny-route case exists to catch, and a `401`
that sends an operator to debug permissions during a rollout. No decision
record is written for a mismatch (none is for a `400` either): it is not a
decision. The operator finds it three ways — the south-bound error log line
(`southbound_failure`, naming both numbers and the proxy), the fleet view, and
the publish-time report.

The message is safe to disclose (M11): two numbers and the caller's own id.
It reads "deploy the proxy build that declares N, or the server that serves
M" — the remedy in both directions, because a newer proxy against an older
server is the same mismatch.

`validate` now refuses `< PolicyVersionMinimum` (the schema's `minimum: 1`) as
`400`: below the minimum is malformed, at or above it and not `PolicyVersion`
is a mismatch.

### Why the tiers could be folded in

The prompt's own argument, and the test that holds it:
`TestEveryOtherVocabularyIsRefusedOnEveryRoute` and
`TestAFloorOrABanIsRefusedAtEveryOtherVocabulary` send every vocabulary from
the minimum to two past the current one at a `brokered-certificate` route, a
baseline route, a floor route, a ban route and an unknown target, and only
`PolicyVersion` is ever answered. Every route a tier refused is refused; every
route a tier served below `6` is now refused too. A proxy is therefore never
sent vocabulary above what it declared, by construction: the only answers are
to a proxy declaring the vocabulary the assembly produces.

The per-level floor rule and the per-proxy ban rule were **not** folded in:
`TestAFloorIsSentOnlyToAProxyThatDeclaredIt` and
`TestABanIsJudgedPerProxyFromItsOwnDeclaration` still run, now at
`PolicyVersion`, and still expect a `CapabilityError`, not a mismatch.

### The repository-wide check

`version_literal_test.go` at the module root (it runs in `go test ./...`, so
CI's `test` job carries it). Line patterns, outside `internal/contract/version.go`:

- a key or identifier `policy_version`/`PolicyVersion`/`contract_version`/
  `ContractVersion`/`Vocabulary…` assigned or compared (`:`, `=`, `:=`, `==`,
  `!=`) to a **non-zero** integer — zero is "declared nothing";
- `DocumentVersion`'s value anywhere;
- `contract`/`contract_version`/`document_version` given a dotted version.

Not scanned: `contract/`, `*.md` (the plan, learnings, changelog and prompts
are history), `dist/`, `node_modules/`, `.git`, `package-lock.json`. It cannot
see a literal passed positionally (`authorize(route, 5)`); the decision and
conformance tests derive every version from the constants, which is the
pattern. `TestTheVersionLiteralCheckCatchesEachShape` writes each shape into a
temp tree, and the shapes it must leave alone (Markdown, `contract/`, `dist/`,
zero, a bundle's `version: 3`, `APIVersion = 6`, a derived value).

A browser-side file cannot import Go, so `ui/e2e/fixture/contract.ts` reads
the vocabulary out of `contract/control.yaml` with the same regex the Go tie
reads ("The current vocabulary is `N`"), and `ui/test/chrome.test.tsx` reads
`info.version`. If upstream rewords that sentence, the Go tie fails first and
says the document states no vocabulary.

### The conformance layers, and the mock

The mock leg could not be run from this session: attaching `hoplock/proxy` was
refused, so CI's `conform` job is the first run against `cmd/mock-control`.
What changed for it, and why each should pass:

- the certificate and floor/ban refusal cases declare `PolicyVersionMinimum`
  (`1`) instead of `4`/`5`: the mock's tiers refuse `1` for both, as they
  refused `4`/`5`;
- `requireVersionRefusal` now requires only the declared number in the message
  at the contract layer (word-bounded, so `1` is not found in `/v1/`); before,
  it required the tier as well, which is a per-route server's fact;
- the old "refused at 5, everything else answered at 5" case is unchanged
  under `(per-route)`, with `below = PolicyVersion - 1` instead of the deleted
  constant;
- the vocabulary case's low version is `PolicyVersionMinimum` instead of a
  literal `1` — the same number.

If the mock fails, the assertion is in the wrong layer: move it, do not loosen
it.

### Surfacing: the decision the user made

The prompt asks for the mismatch to be "a fleet-readiness failure in the fleet
view" and also to leave the north-bound API version alone. A new member on
`GET …/proxies` and a new health reason both move that version under
`docs/API-VERSIONING.md`, so the session asked; the user chose to move it to
`7`. Nothing about the north-bound negotiation changed — it still answers `1`
to `7`, and the two numbers are still independent: north `7` moved for a
member, the contract's vocabulary did not move at all.

- Members are `omitempty` + `since:"7"` and false/zero in the healthy case, so
  a client of `6` is served exactly as before until a proxy is of the wrong
  build, and then `500 api_version_unexpressible` (the rule's own
  "never a body that leaves it out").
- The finding is document-scope, warning, id `satisfiability.contract_version_unsupported/`
  (stable whatever the set of proxies, like `algorithm.floor_undeclared`'s per
  rule), parameters `served` and `proxies` (`proxy_id`, `zone`, `declared`) via
  `capped`. It gates activation, deliberately: publishing while a proxy
  refuses every session should be an acknowledged act.
- The pre-publish verdict (`proxyVerdict`, not the pure `CheckProxy`, whose
  input is a capability set) marks the proxy not OK with
  `fleet.UnsupportedVocabularyReason`.
- Console: status `unsupported` (outage tone, DESIGN §6 updated), beside the
  number in Fleet's contract column and as a pill in the detail with a
  sentence; the summary's "Unsupported contract" count; the reason sentence on
  Deployment; the finding sentence. The summary's `contract_versions` bucket
  `0` (never declared) now reads "none declared" — it was always all-`0`
  before, so "version 0" had never been seen beside a real number.

### The e2e fixture, and what it costs other screens

`ui/e2e/fixture/fleet.ts` has `px-edge-05` (already "a rollout in progress")
declare `POLICY_VERSION - 1`; the request is refused `500` and recorded. So:
Fleet shows it `Unsupported`; the tenant's fleet gains a reason (it was
already `degraded` for `px-edge-07`, so no top bar changed); and **every
publish-time report in the fixture now carries a second warning**, which
changed the policy report, candidate-floor and activation screens and the
mutation spec's accept step (it now checks every `[data-accept]`). The fixture
proxies that authorize at `POLICY_VERSION` now show `6` where Fleet showed
"None". Baselines regenerated for exactly those screens and the gallery's
status page (the new status entry).

Two e2e assumptions of "one warning" broke and were fixed rather than worked
around: `policy-activate`'s readiness in `e2e/screens.ts` waited on
`[data-finding]`, a strict-mode violation with two (now `.first()`), and the
author's flow in `e2e/mutations.spec.ts` checked one `[data-accept]` (now every
one). Run locally against the seeded server: the `checks` project 1258 passed
before that fix and the 15 `policy-activate` checks after it, `mutations` 8 of
8. This container renders the unchanged screens byte-identically to the
committed baseline (159 of 171 matched before regeneration), so the images are
CI's.

### What was left alone

- `docs/learnings/0002-…` and `0006-…` describe the multi-version position as
  it stood; they are records, and PLAN §4 states the current one. The prompt's
  "0006's note on the enrolled version" lives in code comments
  (`store.Proxy.ContractVersion`, `fleet.EnrollmentRequest.ContractVersion`,
  `fleet.ProxyHealth`), revised; migration `0002`'s comment is merged and
  forward-only.
- No upstream request: nothing here needs a contract change. The contract
  already sanctions the `5xx` and leaves per-route answering to a server.
