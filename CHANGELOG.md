# Changelog

What each release of `github.com/hoplock/control` contains, newest first, for
anyone who depends on the module or runs the binary. Hoplock Enterprise reads it
before every bump of its pin (its E3).

What a release is, when one is cut, and what its number promises is decision
**M23** in `docs/PLAN.md` §2. It is not restated here.

**Adding an entry.**

- List what someone consuming the module or running the binary can observe. Do
  not list prompts, plans, learnings or CI.
- A phase releases in its own PR. Add a `## vX.Y.Z` section above the newest
  one, and move anything under `## Unreleased` into it. In the section:
  - `### Breaking`, when the release changes a public package incompatibly.
    `make release-check` finds those changes and prints them;
  - the phase or phases it contains, by number;
  - the changes to public packages, by identifier;
  - a behaviour change that no API diff can see, which nothing else catches.
- A change that does not release goes under `## Unreleased` until one does.
- A released section is what its tag says. Never edit one; correct it in the
  next release's section.
- `make release-check` says what merging the tree releases, and refuses a
  version number that does not match the change.

## Unreleased

## v0.11.0

Signed retention. A retention pass signs what it leaves behind with a
per-tenant key the database does not hold in usable form, and `audit-verify`
refuses a marker, or a removed session capture, whose signature does not
check. A writer of the database alone can no longer delete a stream's oldest
records — or empty a capture — and make it verify as lawful retention.

### Breaking

- **Retention is refused without a key store.** With no
  `credential.key_encryption_key_env` and no registered `ext.KeyStore`,
  `POST …/audit/retention` and `POST …/audit/captures/retention` answer
  `503 retention_key_not_configured` with `config_key:
  credential.key_encryption_key_env`, and delete nothing. Both passes ran
  unsigned before.
- **Retention written by `v0.6.0`–`v0.10.0` no longer verifies, and nothing
  builds on it.** Nothing can tell an unsigned marker from a forged one, so it
  is never silently trusted:
  - `hoplock-control audit-verify` reports a stream whose marker is unsigned
    as `BROKEN`, a `retention-marker` break naming the marker's sequence and
    hash, and exits 1. With `--captures` it does the same for each capture
    emptied by a pass that signed nothing.
  - A retention pass over such a marker, or over such captures, is
    `409 retention_unverified` (`stream`, or `captures: true`) and deletes
    nothing. So is a pass over a stream that no longer starts where its marker
    says.
  - No path signs them after the fact. Nothing is deployed yet, so a
    database carrying such retention is a development or test one, and is
    rebuilt rather than migrated.
- **`audit-verify` and `audit-retention` hand a host's configuration sections
  to `HostConfig` and seal its registry,** as the daemon does, so they check
  and sign with a host's `ext.KeyStore`. Every other subcommand still never
  calls `HostConfig`.

- **Phase:** 0024, signed retention markers.
- **Public packages:** no identifier is added, removed or changed. The doc
  comments of `server.Main` and `server.Options.HostConfig` name the
  subcommands that call `HostConfig`.
- **Behaviour no API diff shows:**
  - A registered `ext.KeyStore` now holds keys: one Ed25519 key per tenant,
    `audit-retention:<generation>`, generated on the first pass. Without one,
    Control's software custodian holds them, encrypted under the
    key-encryption key. The certificate authority still uses the software
    custodian only.
  - Each marker stores the key that signed it and the signature
    (migration `0013`: `audit_retention.key_name`, `signature`), so a marker
    signed before a rotation still verifies after it. A destroyed key makes
    every marker it signed a `retention-marker` break naming the key.
  - Capture passes keep a signed per-tenant watermark
    (`audit_capture_retention`). A capture counts as retained only when its
    record's own timestamp, read from the hashed body, is before it.
  - A capture pass whose `before` is ahead of the clock runs to now, and its
    answer's `before` says so. Both retention answers gain `key`, the key that
    signed. `operator.audit.retained` carries `operator.key` and
    `operator.signature`, and a capture pass's also `operator.watermark`.
  - `hoplock-control audit-retention key | rotate-key`: show the tenant's
    retention key and its public half, and make a new generation (recorded as
    `operator.audit.retention_key_rotated`).
  - New codes: `retention_key_not_configured` (503) and
    `retention_unverified` (409).

## v0.10.0

The fourth and last audit of `v0.6.0`: the north-bound surface — its routes,
roles, error codes, the record every write leaves, each handler — and what
`v0.6.0` and its pull request said, checked against the code, with every
defect fixed under a test that failed first.

### Breaking

- **A host route on a path Control serves through a wildcard is refused at
  start-up.** `server.Main` and `server.Run` refused a host pattern equal to
  one of Control's, a method beside one, or one the mux found ambiguous, but
  accepted a literal under one of Control's wildcards — `targets/special`
  beside `targets/{target}`, `access-context/bindings/push` beside
  `access-context/{provider}/push` — and the mux then routed that path to the
  host. No identifier changed.
- **`PUT …/subjects/{subject}` refuses a claim the claim mapping would
  refuse** (`400`, `field: claims.<name>`, `problem: reserved` or `invalid`):
  `chain_hop_proxy_id`, any `hoplock.*` name, and a name outside the attribute
  pattern. A stored claim is a policy attribute on every decision for the
  subject, so a reserved name stored here stood beside every login as if a
  proxy's key had authenticated a chain leg.
- **Refusals that changed status or field:**
  - a body past the listener's limit is `413 payload_too_large` with
    `limit`; it was `400 invalid_request`, `field: body`;
  - `POST …/revocations`: a `host_key_withdraw` with a blank `target` or
    `fingerprint` is `400` naming `host_key.target` or
    `host_key.fingerprint`, where it was `409 host_key_unresolved`; a
    selector, reason or audience refusal names `session_ids`, `keys`,
    `reason` or `proxy_ids` rather than `body`;
  - `POST …/targets/relabel`: a change naming no target, or a target a
    second time, is `400` naming `changes[<i>].target`; a target that is not
    there is `404` with `target`;
  - `POST …/audit/retention` with a negative `through_seq` is `400`; it was
    read as absent.
- **The fleet configuration records drop `operator.unannounced`.** The
  record is now written in the publish's transaction, before anything is
  announced; which announcements the stream took is in the answer, as it
  was. The record gains `before`: the version the publish displaced.

- **Phase:** 0023, the audit of 0019's north-bound surface and of the record.
- **Public packages:** no identifier is added, removed or changed. The doc
  comment of `server.Route` names the paths a host may not take.
- **Behaviour no API diff shows:**
  - Three writes now commit with their record: `PUT …/identity/claim-mapping`
    (which also stored and activated the version in two statements),
    `POST …/proxies/enrollments`, and `PUT …/fleet/config/{scope}/{id}` with
    its `rollback`. Each was recorded after it committed, so a failed record
    left a change the chain did not state.
  - A target, subject or claim-mapping write reads what it replaces under the
    tenant's lock for that collection, so concurrent writes record a chain of
    befores. A relabel's write re-reads each target under that lock and
    changes only its labels: a hostname, zone or credential method written
    while it simulated is kept, where it was overwritten with the values the
    preview read.
  - `GET …/audit/negotiated` answers `truncated`, true when its limit cut the
    list short: the runbook's next step ends exactly the sessions listed. An
    `axis` without its `value`, or a `ban_axis` without its `ban_name`, is
    `400` naming the missing one; it was a `500`.
  - No refusal quotes a Go operation, a package's error text, or the
    database's: `this query cannot be answered: store.Audit.RetainPrefix:
    invalid: …` is `this query cannot be answered: the head of stream … is
    never retained`.
- **What `v0.6.0` stated wrongly or left out:**
  - its route list omitted `PUT …/identity/claim-mapping` (`identity:write`);
  - its `### Breaking` did not say that a host route on a path it began
    serving, or a host code equal to one of its eight new codes, is refused at
    start-up — a host binary that started under `v0.5.0` could fail to start;
  - "every operator act is recorded" held, but three of those records were
    written outside the act's transaction, against PLAN §3's rule (above).

## v0.9.0

The third audit of `v0.6.0`: the policy lifecycle — the publish-time report,
upload, activation, simulation, explain and `policyctl` — checked against the
code, with every defect fixed under a test that failed first.

### Breaking

- **A validator finding whose `ext.FindingSeverity` is none of the three is an
  outage.** Validate, upload and activate answer it as a validator error
  does (`5xx`), naming the provider, the code and the value. It was rendered
  `advice` (`FindingSeverity.String`'s default), which gates nothing, so a
  governance rule could pass silently.
- **Two activation-gating warnings arise where they did not.** A cache hint on
  a rule matching `context.source_cidrs` is the warning
  `authoring.cache_hint_spans_sources`, whatever bounds the session: a proxy
  reuses a cached decision by identity, target, port, method and hop trail,
  never by client address. And `algorithm.policy_unmet` now warns every allow
  route reaching a target with a stored `target.algorithm_policy_unmet` under
  its profile, including a route naming no floor and no ban (it warned only
  those that named one). A bundle that activated under `v0.8.0` may now need
  these accepted by id to activate again.
- **`POST …/policy/simulate` refuses a dry-run case that would ask a
  different question** (`400`): a grant with no `expires_at`, which was never
  live, and a grant `origin` or a `subject.auth_method` outside the engine's
  vocabulary, which matched no rule naming one.

- **Phase:** 0022, the audit of 0019's policy lifecycle.
- **Public packages:** no identifier is added, removed or changed. The doc
  comments of `ext.PolicyBundleRef.Version`, `ext.PolicyDocument` (and its
  `AuthorID`, `ProposedAt`, `ActiveVersion`), `ext.FindingSeverity` and
  `ext.PolicyValidator` now say what Control does: a validator runs only on a
  source that compiles, at validate, upload and activation; `Version` is
  empty until a version is stored; `AuthorID` is the subject asking for that
  judgement, empty for an API token; `ProposedAt` is that call's time;
  `ActiveVersion` is the version serving when asked; Control's own
  publish-time checks run beside the compiler's when no validator is
  registered.
- **Behaviour no API diff shows:**
  - Uploads and activations are serialised per tenant. Identical sources
    uploaded at once are one version, found by digest rather than by reading
    every stored source. Concurrent activations each record the version they
    replaced as `before`, and one that finds its version already active
    records nothing; the loser of a race was a `5xx` from a unique index.
  - A ban is a typo warning (`algorithm.ban_unmatched`) only when at least one
    build has declared what it offers. With none, the ban is advice
    (`algorithm.declaration_unknown`) and the emergency runbook's ban
    publishes without an acceptance.
  - A read of the stored unmet records that stops at its limit (1000, newest
    first) says so: advice, `algorithm.policy_unmet_truncated`.
  - Replay lists a record whose recorded instant is missing, or whose source
    address or grant instant does not parse, as unreplayable with its reason;
    it was replayed with zero values in their place.
  - Explain answers `500 decision_unexplainable` for five more partial
    stories: inputs with no instant, an effect this server never writes, an
    allow the engine did not make, an allow with no snapshot, and an unserved
    record that does not say why.
  - The all-added text diff no longer counts a line after the final line
    break.
  - `policyctl` prints a tab or a line break inside a field as a space, so a
    record is one line. `simulate` with neither `-from` nor `-cases`, or with
    `-to` alone, is a usage error (exit `2`); it exited `1`.

## v0.8.0

The second audit of `v0.6.0`: the audit store, retention and fleet
configuration delivery, checked against the code, with every defect fixed
under a test that failed first.

- **Phase:** 0021, the audit of 0019's audit store, retention, fleet
  configuration, store, configuration and daemon wiring.
- **Public packages:** no identifier is added, removed or changed. `ext` and
  `server` are unchanged.
- **Migration:** none. The audit columns and `audit_retention` are as `v0.6.0`
  left them.
- **Behaviour no API diff shows:**
  - A retention pass (`POST …/audit/retention`, `POST
    …/audit/captures/retention`) and its `operator.audit.retained` record
    commit in one transaction. A pass whose record cannot be written deletes
    nothing and moves no marker. Before, the deletion committed first, so a
    failure between the two left a deletion nothing recorded.
  - `POST …/audit/retention` answers `through_hash` on every answer. A pass
    by `before` that took nothing now carries it too: the marker's hash, or
    empty when the stream was never retained.
  - `GET …/audit/negotiated`'s `limit` counts sessions. It counted records,
    so a session with several matching records could crowd others out of
    the answer the runbook hands to `session_kill`.
  - `GET …/audit/sessions/{session}/certificates` and `…/algorithms` read
    every record of the session that carries a serial or an algorithm. They
    read the session's first 500 records and missed anything after them.
  - The negotiated-value, ban, refused-record-id and certificate-serial
    filters of the audit query use the indexes `v0.6.0` built for them.
    Before, each one read every record in the tenant.
  - A proxy whose zone and own scope have nothing published desires no
    version. Its fetch answers `204`, it is sent no `config_changed`, and the
    rollout view does not show it as drift, even after it reports its
    bootstrap file applied. Its first document is `v1`. `v0.6.0` stored the
    empty composition as `v1`: it announced that version, which the fetch
    then denied, and showed the proxy behind it for good. A proxy enrolled
    under `v0.6.0` or `v0.7.0` with nothing published keeps that `v1` until
    its scope is published. Its fetch now answers `200` with no settings,
    which the contract treats as the bootstrap file.
  - A tenant in which a fleet configuration is published or rolled back is
    one the announcer retries in. A lost announcement there was retried only
    once a proxy of that tenant subscribed on the same node.
  - A `config_changed` counts as announced only when a proxy's live event
    stream on the node took it, or the proxy's report names the document.
    Each node's announcer delivers to the proxies whose streams it holds, so
    a publish on one node reaches a proxy subscribed on another within one
    announce interval (15 s). A publish answers `announced: false` for a proxy
    not subscribed on the publishing node, saying why, where it answered
    `true` because the event was retained. Revocations still reach only the
    publishing node's subscribers, so Control still runs as one node.

## v0.7.0

The first audit of `v0.6.0`: the south-bound surface and the decision path,
checked against the code, with every defect fixed under a test that failed
first.

### Breaking

- **`cmd/pdpconform` expectation files must name `algorithms.bans`:** the ban
  route's bans as authored, one list per axis. A file without it is refused
  at start-up, as any missing key is, because the case that compares the
  answer with it would otherwise grade nothing.
- **`south.max_log_body_bytes` below one storable record is refused at
  start-up:** below `audit.max_capture_bytes` as base64, plus
  `audit.max_record_bytes`, plus 4 KiB (`config.MinLogBodyBytes`). A body over
  the limit is a `400` the proxy cures by halving the batch, which a batch of
  one record cannot be, so such a limit lost that record. The shipped bounds
  need about 1.4 MiB under the 4 MiB default, so a configuration that leaves
  all three keys at their defaults is unchanged.

- **Phase:** 0020, the audit of 0019's south-bound surface and decision path.
- **Public packages:** no identifier is added, removed or changed. `ext` and
  `server` are unchanged.
- **Behaviour no API diff shows:**
  - A policy bundle may ban an algorithm name made of blanks. It was refused
    as `route.algorithm_ban_empty`, which the proxy reserves for the empty
    name; it is now a name no build offers, published with the
    `algorithm.ban_unmatched` warning like any other.
  - `POST /v1/credentials/certificate` mints only on a decision inside the
    window a session may be served under it: its cache hint's TTL, or none,
    plus two minutes (`credential.DecisionSetupAllowance`). An older
    `decision_id` is `401 unknown_decision`. A conforming proxy cites a
    decision only while it may reuse it, so it never sees this.
  - A proxy's algorithm declaration, as `GET …/fleet/algorithms` and the
    publish-time report read it, follows the build the proxy runs within a
    minute on every node. A node used to remember its own last write for as
    long as it ran, so after a rollback the stored copy could keep another
    node's newer build indefinitely.
- **Conformance suite:** the error-discipline group grades the certificate
  endpoint and both fleet configuration routes for a rejected token. New
  cases: a ban answered exactly as authored, and unchanged when the request
  declares `capabilities.algorithm_profiles`; a key-exchange report and a
  session-less priority record answering `accepted: true`; a certificate's
  `valid_before` inside the route's `lifetime_seconds`; and a `logging.gap`
  accepted on the path its severity picks.

## v0.6.0

The north-bound surface is complete: an operator, a pipeline or `policyctl`
authors, simulates, publishes and explains policy, manages the inventory,
queries and retains audit, and acts — and a proxy can be sent brokered
certificates, an algorithm floor and bans, and fleet configuration.

### Breaking

- **Configuration keys removed:** `events.publish_listener`,
  `events.publish_token`, `audit.read_listener` and `audit.read_token`. The
  two debug listeners they bound are deleted, and a configuration naming any
  of the four is refused at start-up as an unknown key. What they served is
  on the north-bound listener under a scoped API token:
  `POST /api/v1/tenants/{tenant}/revocations` (`session:revoke`) and
  `GET /api/v1/tenants/{tenant}/audit/records/{record_id}` (`audit:read`).
- **`ext.FindingWarning` stops activation until it is accepted.** It used to
  block nothing. A warning — from Control's own checks or a registered
  `ext.PolicyValidator` — now holds activation until the author accepts it by
  the finding's id, and the acceptance is on the activation's audit record. A
  validator that meant a finding as information returns `FindingAdvice`. No
  identifier changed, so no API diff shows this.
- **Audit columns renamed:** `audit_records.target_auth_method` and
  `target_auth_rung` are `credential_method` and `credential_rung`, the only
  names the proxy emits (migration `0012`). SQL naming the old columns fails.

- **Phase:** 0019, the north-bound API, inventory and policy lifecycle.
- **Public packages:** no identifier is added, removed or changed. The doc
  comments of `ext.FindingSeverity`, `ext.FindingWarning`,
  `ext.FindingBlocking` and `ext.Finding.Code` say what each severity does at
  activation and that `Code`, with the provider and `Path`, forms the id a
  warning is accepted by.
- **North-bound routes**, each under `/api/v1/tenants/{tenant}/`: the policy
  lifecycle (`policy/validate`, `policy/bundles`, `policy/bundles/{version}`,
  `…/diff`, `…/activate`, `policy/diff`, `policy/active`, `policy/apply`,
  `policy/simulate`); explain (`decisions/{decision}`,
  `sessions/{session}/decisions`); inventory (`targets`, `targets/relabel`,
  `subjects`, `role-bindings`, `proxies`, `proxies/enrollments`,
  `proxies/{proxy}/withdraw`, `fleet/config/{scope}/{id}`, its `rollback`,
  `fleet/config/rollouts`, `fleet/algorithms`, `fleet/target-observations`);
  the audit query (`audit/records`, `audit/blocked-commands`, `audit/drift`,
  `audit/algorithm-policy-unmet`, `audit/negotiated`, `audit/gaps`,
  `audit/sessions/{session}/loss`, `…/algorithms`, `…/certificates`,
  `audit/certificates/{serial}/records`) and retention (`audit/retention`,
  `audit/captures/retention`); `revocations`; `extensions`; and
  `access-context/providers`, `access-context/bindings` and
  `access-context/providers/{provider}/binding`.
- **Permissions:** `target:write`, `audit:retain`, `session:revoke`,
  `access-context:read`, `access-context:write` and `extension:read` are
  added, and a host route may name any of them. Every reading role gains
  `access-context:read` and `extension:read`; `policy-author` and
  `fleet-admin` gain `target:write`; `grant-admin` gains `session:revoke`;
  `admin` holds all. `audit:retain` and `access-context:write` are the
  administrator's alone.
- **Error codes:** `policy_invalid` (`422`), `findings_not_accepted` (`409`),
  `policy_blocked` (`422`), `decision_unexplainable` (`500`),
  `config_key_not_fleet_owned` and `config_value_invalid` (`422`),
  `host_key_unresolved` (`409`) and `unavailable` (`503`).
- **South-bound:** the contract is vendored at `4.7.0` (`hoplock/proxy@976faaa`)
  and this server speaks `policy_version` `6`. `POST /v1/credentials/certificate`
  issues a brokered certificate per session; `GET /v1/proxies/{proxy_id}/config`
  and `POST …/config/report` are served, and a publish announces
  `config_changed`. A route naming `brokered-certificate` is refused to a proxy
  declaring below `5`, and one with a floor or a ban below `6` — a `5xx`
  naming both numbers, never a thinned answer.
- **Binary:** `bin/policyctl` is built beside the server: `validate`, `diff`,
  `apply`, `activate`, `versions`, `simulate`, `explain`, exiting `0`, `1` for
  a refusal and `2` for a usage or transport error.
- **Configuration added:** `policy.interpreters` (the executables a restricted
  allow-list warns on; empty takes the shipped list, and a list replaces it) and
  `south.max_log_body_bytes` (the two log paths' body limit, 4 MiB).
- **Migration:** `0012` adds the algorithm declarations, the key-exchange
  observations, the configuration report and announcement columns, the audit
  record projections, `audit_retention`, and `audit_captures.retained_at`.
- **Behaviour no API diff shows:** `session_id: ""` is accepted on every
  record kind and never looked up as a session. Every operator act is recorded
  as `operator.<noun>.<verb>` on the control stream. A retention pass deletes
  a prefix of a stream and `audit-verify` resumes after it. A proxy's running
  configuration comes only from its config report; the heartbeat no longer
  carries one.

## v0.5.0

A host serves a read that spans tenants, and Control resolves which tenants it
covers.

- **Phase:** 0018, the cross-tenant access class.
- **Public packages:** `server.Access` is added, with `server.AccessTenant`
  (the zero value: what every host route was before) and
  `server.AccessCrossTenant`. `server.Route.Access` is added; a `Route` that
  does not set it is unchanged. `server.Caller.Tenants` is added: the set
  Control resolved on a cross-tenant route, sorted and never empty, and nil on
  a tenant route, where `Caller.Tenant` is the one; `Caller.Tenant` is empty
  on a cross-tenant route. `ext` is unchanged.
- **North-bound routes:** Control adds none of its own. A host's
  `AccessCrossTenant` route is mounted at `/api/v1/cross-tenant/<Pattern>`.
  With no selector it covers every tenant in the caller's scope where the
  caller holds the route's permission, and `403 forbidden` naming the
  permission when there is none. A `tenant` query value or an
  `X-Hoplock-Tenant` header narrows that set, never widens it: a named tenant
  out of scope is `403 tenant_out_of_scope`, and one in scope without the
  permission `403 forbidden` naming both — refused, never dropped. No error
  code is added, and no role gains a permission.
- **Start-up refusals:** a host route whose `Access` is neither class; a
  cross-tenant route whose permission the auditor role does not hold
  (`grant:write`, `report:write`, `access-context:push`, ...); and a
  cross-tenant pattern naming `{tenant}`. Each names the route, before a file
  is read or a port bound.
- **Behaviour no API diff shows:** a session covers the one tenant it signed
  in through, so on a cross-tenant route it covers that tenant; a principal
  spans tenants only as a token minted with `identity token-issue --scope`
  naming several. The north-bound access log line of a cross-tenant request
  carries `tenants`, the sorted set, and no `tenant`.

## v0.4.0

An external system ends a window it opened, and the sessions it backed end
with it.

- **Phase:** 0017, ending an external window.
- **Public packages:** `ext.WindowAssertion.Ended` is added. With it, a push
  ends the window its `ID` opened: Control reads the `ID`, records `Reference`,
  `IssuedAt` and `AdditionalContext` when set, and ignores every other field.
  The zero value is an open, as every push was before. The documentation of
  `WindowAssertion`, its `ID`, `Subject`, `Targets` and `Window`, and
  `AccessContextProvider`'s push direction says what an end does. `server` is
  unchanged.
- **North-bound routes:** none added. The push receiver answers an end `200`
  with `{assertion_id, outcome, grant, state}`, where `outcome` is `revoked`,
  `already_ended` or `recorded`, after the settle pass when it revokes. An end
  whose revocation could not be published is `503
  grant_revocation_undelivered` with `grant_id` and `revoked: true`, as the
  revoke route answers one; pushing the end again re-sends. An open under an
  id its system already ended is `400 window_closed`, `field: id`. No error
  code is added, and no role gains a permission.
- **Behaviour no API diff shows:** an end revokes the grant its window became,
  audited as `grant.revoked` with the integration's credential and
  `grant_external_ended: "true"`, and the holder is shown "The access window
  this session depended on has ended." — never the vendor's text. An end is
  never refused for its age, scope or window. An end of a window already
  revoked, ended or expired changes nothing; an expired one is not rewritten
  as revoked. An end for an id with no window is kept and recorded as
  `access_context.window_ended`, and a later open under that id is refused. A
  probe that stops confirming a window still revokes nothing.
- **Configuration:** a declarative provider's `push.ended` — assertions over
  the push body in `probe.confirm`'s vocabulary, all of which must hold, with
  no placeholders — makes a push an end. Absent, every push is an open.
- **Migration:** `0011_external_window_ends.sql` adds one table,
  `access_context_window_ends`. Run `hoplock-control migrate` before starting
  this release.

## v0.3.0

A person asks for access for themselves, through a registered approval
workflow.

- **Phase:** 0016, self-service grant requests.
- **Public packages:** `ext.GrantRequest.BreakGlass` is added: true when the
  credential that raised the request was minted on the break-glass path,
  copied from the stored request, so a resubmission after an outage keeps it.
  `ext.GrantWorkflow`'s documentation names the two doors a request reaches
  `Submit` through: an administrator's create, and a requester's own, where
  `Subject` and `RequestedBy` are one person. In `ext.Points()`, the
  `GrantWorkflow` point's `Absent` text adds that a requester's own request is
  refused while nothing is registered. `server` is unchanged.
- **Permissions and roles:** `grant:request` is added to the closed set, held
  by a new role, `requester`, and by `admin`. `requester` holds nothing else:
  no read set, and so not `license:read`, which is now held by every role
  except `integration` and `requester`.
- **North-bound routes:** four, in a new self-scoped class whose subject is
  the caller's and never a field the caller sends, all under `grant:request`:
  `POST` and `GET /api/v1/tenants/{tenant}/me/grant-requests`, and `GET …/{request}`
  and `POST …/{request}/cancel` beneath it. The create takes the
  administrator's body without `subject` and answers as the administrator's
  create does with a workflow registered (`202`, `201`, `403
  grant_request_denied`, `502 grant_workflow_failed`). With no workflow
  registered it answers `403 grant_workflow_not_registered` and stores
  nothing. Somebody else's request is `404`. A credential with no person
  behind it is refused on every one of them, `403 subject_required`. Two error
  codes are added: `subject_required` and `grant_workflow_not_registered`. A
  host route pattern beginning `me/grant-requests` now collides with
  Control's and is refused at start-up.
- **Behaviour no API diff shows:** the approval workflow's poller now learns a
  tenant when a proxy of that tenant opens its event subscription, so after a
  restart a decision is applied in every tenant with a live proxy within one
  `grants.workflow_poll_interval`, with nobody reading the request.
- **Migration:** `0010_grant_requests_by_subject.sql` adds one index. Run
  `hoplock-control migrate` before starting this release.
- Two texts name Control's phases by their new numbers, after the queue was
  renumbered to fit phase 0018: `ReportProvider`'s `ControlShips` in
  `ext.Points()` now reads "(phases 0010, 0019)", and the start-up warning on
  the two temporary operator listeners now calls each "a pre-0019 operator
  path". Nothing else changes.

## v0.2.0

A host binary can start Control.

- **Phase:** 0015, the public server package and host routes.
- **Public packages:** `server` is added, beside `ext`, and is the same
  compatibility promise. `server.Main` is Control's whole command line and
  `server.Run` the daemon alone; `server.Options` carries what a host adds:
  `Provider`, `Registry`, `HostSections`, `HostConfig`, `Routes` (`server.Route`)
  and `ErrorCodes`. A host route reads its caller with `server.CallerFrom`
  (`server.Caller`) and answers a failure with `server.WriteError`. `ext` is
  unchanged.
- **Permissions:** two codes are added to the closed set. `report:write` is
  held by `admin` alone; `license:read` by every role except `integration`.
  Control serves no route that needs either: a host's routes name them.
- **`hoplock-control`** is one call to `server.Main` and otherwise unchanged:
  the same flags, subcommands, output, configuration and routes. Two things a
  build or an operator can see:
  - an interrupt or `SIGTERM` now cancels a running subcommand, which stops
    with an error, where it used to end the process outright;
  - the `VERSION=` override lands in `internal/daemon`, so an `-X
    main.version=…` passed by hand no longer sets anything. Use
    `make build VERSION=…`.

## v0.1.0

The first release.

- **Phases:** 0001–0013, and 0014, which cut this release. `docs/PLAN.md` §10
  says what each one delivered.
- **Public packages:** `ext`, as `ext/README.md` describes it. `ext.Points()` is
  the authoritative list of its extension points. It is the module's only
  public package.
- **`hoplock-control --version`** reports Control's module version from the
  build's own information: the tag at a release, a pseudo-version after one, a
  `+dirty` suffix for a modified tree, and `dev` when the build has no version.
  Built into another program, it reports the version of Control that program
  depends on, never the program's own. A Control swapped in by a `replace`
  directive reports `dev` and names its replacement.
