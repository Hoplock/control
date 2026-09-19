# 0007 — south-bound authentication & host keys — Learnings

## Summary
- **What shipped:** the south-bound listener and its credential, `/v1/auth/*`
  with MFA owned end to end, `/v1/hostkeys/report`, `/v1/capabilities/report`
  and `/v1/uids/lease` — plus a CI leg that runs the conformance suite **against
  this server** (15/15 across the four groups this phase serves).
- **Key packages:** `internal/httpapi/south/{server,middleware,errors,handlers}.go`,
  `internal/identity/{identity,outcome,service,mfa,scripted,password}.go`,
  `internal/fleet/{hostkey,uidlease,token}.go`, `internal/store/south*.go`,
  `cmd/hoplock-control/{serve,seed}.go`.
- **Migration `0003_southbound_authentication.sql`.** New tables:
  `subject_keys`, `subject_passwords`, `subject_mfa`, `mfa_challenges`,
  `target_host_keys`, `uid_leases`, `proxy_api_tokens`; plus
  `proxies.key_fingerprint`, a GENERATED column
  (`'SHA256:' || rtrim(encode(sha256(public_key),'base64'),'=')`) that is the
  whole of "is this key one of ours". `identity.KeyFingerprint` is its Go half
  and two tests pin them to each other.
- **M11 is structural, not a convention.** `identity` answers `(Outcome, error)`
  — an error is an OUTAGE with nothing to inspect, a deny is `Outcome.Deny`;
  `fleet.AuthenticateProxyToken` answers `(caller, ok, error)`;
  `south.statusFor` is the only place a status is chosen, and two AST tests
  (`south/discipline_test.go`) fail the build if a third function calls
  `contract.Denied` or a handler names a status constant.
- **0011 implements `identity.Directory` and `identity.MFAProvider`.** Challenge
  lifetime, poll rate, single use and expiry-as-deny stay ABOVE those seams.
  `identity.ScriptedMFA`/`ScriptedMFAConfig` is the deterministic provider.
- **NO cache hint is issued** (M9 — 0009 owns the stream that would withdraw
  one). `fleet.Registry.HostKeyCacheHint()` states that as a function, a test
  asserts the response carries none, and there is deliberately **no `cache_key`
  column**: 0009 adds it in the migration that starts issuing hints.
- **South-bound credential is now a decision, M22:** `<tenant>.<secret>`,
  minted at enrollment (`Enrollment.APIToken`), stored as SHA-256, bound to the
  proxy it was issued to. An empty `proxy_id` is a real state meaning unbound.
  **Chain-leg claim:** `chain_hop_proxy_id`.
- **UID leases:** block 4096, range `2000000..2147483646`, and **`term_seconds`
  is deliberately not stated** — Details says why that is a decision.
- **Also fixed here, in 0005's code:** `TestEvaluationIsLinearAndWithinBudget`
  was comparing a race-instrumented measurement against M5's production budget
  (~19% headroom, not 10x) with a hand-rolled timer. Now `testing.Benchmark` +
  a build-tagged `measurementBudget` + an allocations-do-not-grow assertion.
  **The evaluator was never at fault** — it is flat at ~13.7 ns/rule and one
  alloc per call.
- **Decisions: M22 added** (a south-bound credential carries its tenant and
  names its proxy), with its §2 register row; M2 now cites it rather than
  restating it. Nothing amended or withdrawn. PLAN §3, §5.4, §6 and §10 also
  revised in place. **Cross-repo:** none owed; `contract/` and `ext/` untouched.
- **NEXT session:** there is no north-bound API, so a running server is
  configured with `hoplock-control seed --file`; `make conform` needs `-only`
  because this server implements part of the contract. 0008 adds its group to
  the `conform-self` CI leg and replaces the placeholder `authorize:` block in
  `cmd/pdpconform/testdata/control-expectations.yaml`.

## Details

### The south-bound credential is M22, and the reasoning lives there

M2 settles that the south-bound channel has a credential of its own; it does not
say what is in one. This phase had to decide, so **the answer is a decision —
M22 — and not a paragraph in here**: `<tenant>.<secret>`, minted per proxy at
enrollment, stored as SHA-256, bound to the proxy it was issued to. Read the
plan entry for why the tenant is in the credential rather than on the wire (M18
needs a selector, M1 refuses a field) and why an unbound token is a real state
rather than a half-filled row.

What belongs here instead is where it lives in the code:
`fleet.{MintProxyToken,ParseProxyToken,IssueProxyToken,AuthenticateProxyToken}`,
the `proxy_api_tokens` table, and `Registry.Enroll`, which mints one **inside
the transaction that admits the proxy** and returns it on
`Enrollment.APIToken` — a fleet member admitted with no way to call the API is
a half-enrollment an operator repairs by hand.

One consequence worth knowing before you write a test against it: the
conformance harness presents an **unbound** token, because the uid cases lease
for two proxy ids over one listener and exclusivity is per TARGET. A harness
that could only ever present one proxy would not grade that.

### What is NOT behind the identity seams, and why

`identity.Directory` and `identity.MFAProvider` are what 0011 replaces. What
stays above them is everything about the CONVERSATION rather than about the
factor: challenge lifetime, poll-rate enforcement, single use, expiry-as-deny,
and the clamping of a provider's proposed terms. A provider that returned a
24-hour TTL would otherwise hold an SSH handshake open for a day, and every
provider would have to re-implement replay resistance.

Two bugs this arrangement caught while it was being written, both worth knowing:

- **`PollChallenge` must return the row as it stood BEFORE the poll it is
  stamping.** The first version returned the new `last_polled_at`, so every poll
  appeared to have arrived zero milliseconds after the last one and the rate
  limiter refused all of them — the challenge never resolved. The repository
  interface now says so explicitly; if you change that method, the poll-rate
  test is the one that notices.
- **A duplicate-key conflict aborts the transaction it happens in.** The uid
  cursor is therefore created OUTSIDE `InTx`, where a losing racer can swallow
  the conflict and read what the winner wrote. Inside, the loser would find
  every later statement refused with `25P02`, and two proxies reporting a
  brand-new target at the same instant would take each other down.

### The password oracle is accepted, not overlooked

A wrong password is refused outright and a correct one is answered with an MFA
challenge, so the presence of the challenge confirms the first factor. That is
real, it is **known, evaluated and accepted for this product**, and the test
`TestAWrongPasswordIsRefusedOutright` asserts the absence of a decoy so a later
session cannot "harden" it. Two reasons, the first decisive:

1. The contract **requires** it. `200` on `/v1/auth/password` is documented as
   "the password was accepted", so a decoy challenge for a wrong password would
   be a `200` the contract says means something else (M1).
2. A decoy is an amplifier: upstream measured one failed guess going from 1
   Control call to ~121, and from a stateless rejection to a connection held
   open for the challenge's lifetime.

The control that blunts enumeration here is rate limiting, which is not this
phase's. **A change of mind starts upstream**, at that `200` description in
`contract/control.yaml` (`docs/CROSS-REPO-PROTOCOL.md` §3.2) — not behind a flag
here.

### Passwords: PBKDF2, and why that is the right trade rather than the best KDF

`crypto/pbkdf2` (stdlib, Go 1.24+), SHA-256, 600k iterations, 16-byte salt, with
the parameters stored beside each digest so the cost can be raised without
invalidating existing rows. A memory-hard KDF resists offline cracking better
and would be right for a product whose primary credential is a password — this
one's is not: passwords are the fallback the proxy tries after certificate
authentication was not accepted, and **0011 empties this table**. Buying a
dependency for a table scheduled to empty is the wrong trade. The `algorithm`
column is how a second KDF lands beside this one if that ordering changes.

A digest written with an algorithm this build does not implement is an **error**,
not a mismatch: "I cannot check this" is an outage, and answering `false` would
report an operator's half-finished migration as the user's fault.

### `term_seconds`: this server states none, on purpose

The contract asks for a term and this server answers nothing (config
`uids.lease_term`, default `0s`). The term bounds how long the PROXY keeps
allocating from a block it holds; it is **not** what makes the uids
non-reusable — the monotonic cursor is. A block ends when it is exhausted or
when its term runs out, and both fail closed while this server is unreachable,
so shortening the term costs availability during exactly the outage a held block
exists to survive, and buys nothing. Absent leaves the proxy its own default (a
day), which is a better-informed number than one this server would type. The
proxy's own mock answers `0` for the same reason.

The block size is 4096 and the cap is 2^20. `uid_count` on the request is a
request rather than a requirement; what is granted is
`min(requested, max_block_size, remaining)`, and a block is **never** granted
outside the requested `[range_min, range_max]` — a block outside it is refused by
the proxy anyway, so granting one only turns a clear `409` into a confusing
outage.

`observed_floor` is the highest uid **seen given out**, so the lowest still free
is one past it: the cursor is raised to `observed_floor + 1`, clamped to the
requested range and to `range_end`. It never lowers. The exposure is stated
rather than hidden — root on a target can report a large floor and burn that
target's range — and it is the right side of an invariant that prefers refusing
to reusing.

### What `seed` is, and when it goes away

There is no north-bound API until 0014, so there is no way to configure a server
to serve anything — which makes the acceptance criterion ("the conformance
suite's assertions pass against this server, and CI runs it") unreachable
without one. `hoplock-control seed --file <doc>` is that one. Three properties
keep it from being a back door: it writes and never reads, it takes a reviewable
file, and the credentials it writes are hashed by the same functions the running
server verifies against — there is no seed-only path into the credential tables.
When 0014 lands it becomes a thin client of that API or it goes away.

`cmd/pdpconform/testdata/control-seed.yaml` and `control-expectations.yaml` are
**one document in two halves**. A login in one and not the other grades nothing
or fails invisibly; change them together.

### `-only` now takes a list, and that is not a way to be green

`pdpconform`'s `-only` accepts comma-separated substrings, because a server that
implements part of the contract has to be graded on the part it implements. The
`conform-self` CI leg names `authentication,host,capabilities,uid`. Beware the
substring collision that cost a debugging round: `POST /v1/auth` also matches
`authorize (POST /v1/authorize)`, and the Makefile passes `CONFORM_FLAGS`
unquoted so a value with spaces is split by the shell. Use space-free
substrings.

0008, 0009 and 0010 each add their group in the PR that serves the endpoint, and
replace the placeholder sections in `control-expectations.yaml` — which are
filled in only so the file validates, since the suite (correctly) refuses one
whose missing key would turn an assertion into a no-op.

### A test fixture must not hash at a production work factor

The first CI run went red on `internal/policy/eval`'s
`TestEvaluationIsLinearAndWithinBudget` — a wall-clock assertion in 0005's code
that this PR does not touch. The cause was this PR's:
**PBKDF2 at 600k iterations costs about a second per hash under the race
detector**, these fixtures hash on every test, and `make test` is
`go test -race ./...`, so `internal/identity` and `internal/httpapi/south`
between them burned ~75 CPU-seconds of a two-core runner in parallel with a
test that measures microseconds. The budget failed at 396µs against 300µs —
and on an idle machine that same 2000-rule case under `-race` measures 244µs,
which is the next section's problem rather than this one's.

The fix is `identity.HashPasswordWith(subject, password, iterations)`, which is
what the `iterations` column was already for: a verifier is checked with the
parameters stored beside it, so a fixture writes a cheap digest and the
production path is unchanged. Both test packages dropped from ~40s to ~2s.
`TestHashPasswordUsesTheProductionWorkFactor` stops the cheap value leaking
into a production path.

**If you add Postgres-backed fixtures in a later phase, look at what they cost
under `-race` before you add twenty of them.** A suite that is merely slow is
also a suite that makes somebody else's timing assertion fail.

### The eval budget test was measuring race-instrumented code (fixed here)

Removing the load above stopped THIS PR failing; it did not make that assertion
sound, so the assertion is fixed here too. Measured with `testing.Benchmark` at
several rule counts, the evaluator is exactly what M5 promises and the test was
not:

| rules | ns/op | ns/rule | allocs/op |
| --- | --- | --- | --- |
| 250 | 3,415 | 13.7 | 1 |
| 500 | 6,800 | 13.6 | 1 |
| 1000 | 13,852 | 13.9 | 1 |
| 2000 | 27,303 | 13.7 | 1 |
| 4000 | 90,406 | 22.6 | 1 |
| 8000 | 242,806 | 30.4 | 1 |

**Evaluation is linear to 2000 rules** — 13.6–13.9 ns/rule, flat — at **one
allocation per call regardless of rule count**. There is no quadratic and no
unbounded construct, and the ~27µs at 2000 rules is exactly what the test's own
doc comment recorded. Past 2000 the ns/rule rises with allocations still at 1,
so that is the working set outgrowing cache rather than anything algorithmic —
worth knowing if a bundle ever gets that big, and not a defect.

**The defect is in the measurement.** `make test` is `go test -race ./...`, and
the race detector costs this path 6–9x: the same 2000-rule case measures
**244µs under `-race`** against a **300µs** budget. The budget was sized as "an
order of magnitude above the measurement" from the 27µs *non-race* figure, so
under the only command CI ever runs it has ~19% headroom, not 10x. It has been
passing on luck; at `GOMAXPROCS=2` it fails about three runs in five in
isolation, on this branch and the commit before it alike.

**The fix, in `internal/policy/eval/`, touches no production code.** The figure
now comes from `testing.Benchmark`, which calibrates the iteration count and
discards the warm-up; `measurementBudget` is `evaluationBudget` in a normal
build and `10 *` it under `-race`, split across `budget_norace_test.go` and
`budget_race_test.go` by build tag. **`evaluationBudget` is still the M5 number
and did not move** — what changed is that a run knows which of two things it is
measuring.

A third assertion was added, and it is the one most likely to earn its keep:
**allocations must not grow with rule count.** Evaluation allocates once per
call — the snapshot — whatever the rule count, so anything that allocated per
rule shows up exactly, on any machine, under any load, with no threshold to
tune. It is the linearity check without a clock in it.

Before and after, `GOMAXPROCS=2 go test -race` under deliberate CPU load, five
runs each: the old test failed one in five at **500.8µs** against its 300µs
budget, and its *passing* runs measured 222–255µs — ~19% of headroom, which is
the real finding. The new one passed five of five, including a run that
measured **598µs** and would have failed outright before, with linearity at
2.69x and allocations at 1.

Cost: the eval package goes from ~6s to ~10s under `-race`, which is
`testing.Benchmark`'s calibration. Set against the ~75s this PR took *out* of
`identity` and `south`, the suite is far ahead.

(An earlier draft of this file blamed "the GC cost of a program twice the size".
That was wrong — allocations are constant at one per call. The cause is the
race multiplier against a budget with no headroom.)

### Follow-ups this phase deliberately did not do

- **Rate limiting** on `/v1/auth/*`. It is the control that actually blunts
  password enumeration, and it is nobody's phase yet. Worth queueing.
- **Retiring resolved and expired `mfa_challenges` rows.** The index is there
  (`mfa_challenges_expiry_idx`); nothing sweeps. It is housekeeping, not
  correctness — a resolved row is refused, not honoured — but the table grows
  with every authentication.
- **The `cache` hint on `/v1/hostkeys/report`.** 0009's, and it owes three
  things before it may say yes: the M9 liveness read on THIS path and not only
  on authorize; hinting only an already-known, accepted key (never a `reject`,
  never a `known: false`); and storing the issued key on the host-key record,
  because a subject-scoped `cache_invalidate` cannot match a host-key decision —
  it was not made for a person. A key nobody stored is a decision nobody can
  withdraw short of resyncing the entire fleet's cache.
