# 0047 — a revoked registration says so — Learnings

## Summary
- **Shipped (`v0.34.0`, MINOR; no migration; `ext`/`server`/`contract` and the north-bound version `1` untouched):** the supervisory registration's `revoked` line, registration protocol `2`, and the protocol's written rule. Answers `Hoplock/enterprise#64`, item 1.
- **The line:** `{"kind":"revoked","id":"evt-<epoch>-<seq>.<line>","at":"<RFC 3339>"}` — a heartbeat's id (a position named, not taken; never in `supervision_log`, never replayed), `at` = `deployment.supervision_revoked_at` as read by the check (the first instant if revoked again; never `time.Now()`), no `by`, no other member.
- **Sent only** when a node's heartbeat check sees a local revocation (`supervision revoke`). Never for a shutdown, a dropped connection, a failed write, a lost singleton or a silent supervisor; nothing is sent while not connected, and no node dials while revoked. Its absence means nothing.
- **The ending (`endRevoked`, `internal/instance/register.go`):** write the line → `pw.Close()` (clean end of the request body) → wait for the supervisor to end its response. All three bounded by **one heartbeat from the check**; at the bound the session is cancelled and the connection cut, with or without the line. Revocation still takes hold on every node within one heartbeat (unchanged).
- **Protocol:** `ProtocolVersion` = `2` on `hello`. Informational, never negotiated. Skip rule: a supervisor skips an unknown line kind or member and refuses none. A new kind or member, either direction, moves it by one; a changed meaning is not covered. Rule and history: `docs/API-VERSIONING.md`, "The registration protocol"; the head comment of `register.go` lists every line with the version it arrived in.
- **Log:** `supervision_revoked` gains `revoked_at` and `told_supervisor` — true only when the supervisor ended its response after the line, within the bound (a line written into an unread socket is not "told").
- **Reference far end:** the test `supervisor` in `register_test.go` enables full duplex, records each connection's clean/unclean body end, and ends its response when the body ends. That last step is what a supervisor must do to receive the line before the bound.
- **NEXT:** 0048 does not move the registration protocol; leave the "Four numbers" row and the registration section in `docs/API-VERSIONING.md` alone. **0049 (queued here) closes a gap this phase found:** a supervisor that sends but never reads can block a write before the check runs. 0049 bounds every write and gates them so nothing is written after a revocation commits. Queued prompts 0049–0053 moved to 0050–0054. Downstream sync for `hoplock/enterprise` is queued (see the PR).

## Details

### Why the ending closes the pipe itself
`session`'s deferred teardown runs `resp.Body.Close()` before `pw.Close()`
(defers are LIFO, and the response is deferred after the pipe). Closing the
response can tear the connection down before the transport has drained the
pipe, which was harmless while nothing more was to be said. `endRevoked`
therefore closes the pipe first and waits on the response before returning to
that teardown. A torn body is visible to the far end: the
`CloseWithError` mutation fails `TestARevokedRegistrationSaysSo` with
"did not end cleanly".

### `told_supervisor`, and why "written" is not enough
On loopback, and on any real network, a pipe write returns once the transport
has read the bytes, long before the peer does. A supervisor that has stopped
reading still has the line and the end of the body "written" into its socket
buffer. So `told` needs evidence from the far end, and the only evidence in
this protocol is the supervisor ending its response after the body ended
(`supervisorEnded`: the response scanner reached EOF with no error, and the
session was not cancelled). The daemon test's not-reading case holds this:
setting `told` on a successful write fails it.

### The epoch on the line
The line uses the epoch the session read at its start. The epoch is the
`deployment` row's `log_epoch`, written once when the row is created and never
updated, so it is the feed's current one, and the ending needs no store read
that could fail. The heartbeat still re-reads it each tick, as 0046 wrote it.

### Tests, and the mutations each one fails
- `TestARevokedRegistrationSaysSo` (HTTP/1.1 full duplex),
  `…OverHTTP2` (`EnableHTTP2` + `StartTLS`; asserts `ProtoMajor == 2`),
  `TestARevokedClusterSaysSoOnceFromTheHolder` (three nodes; the singleton
  moves once by `election.handover` before the revocation): old ending (no
  line) fails all three.
- `TestRevokingTwiceKeepsTheFirstInstant`: `at = time.Now()` fails it.
- `TestOnlyALocalRevocationSendsTheRevokedLine` (shutdown, supervisor ends the
  connection, supervisor silent → watchdog, singleton lost by handover): a
  check that fires without a revocation fails it. A `revoked` written *after*
  any of these endings cannot be caught, and needs no test: the session's
  cancellation has already closed the pipe, or the supervisor has stopped
  reading, so such a line never reaches the far end.
- `TestARevokedLineIsBoundedWhenTheSupervisorStopsReading`: a transport
  (`stalledSupervisor`) that reads the hello, revokes, reads the resync and
  never reads again, so the line's pipe write blocks. Liveness is an hour, so
  only the bound can end it. Removing the bound fails it.
- `TestASupervisorCannotAffectASession`, case (e), over real TCP: a
  supervisor that keeps heartbeating and never reads. It asserts the bound
  from the revocation (≤ 3 heartbeats: up to one for the check, one for the
  bound, plus slack), no redial, `told_supervisor=false`, and authorize
  unchanged while it runs. No bound fails it, a four-heartbeat bound fails
  it, and `told` set on write fails it. Each case now registers its `stop` as
  a cleanup, because a failed check that skipped `stop()` left
  `httptest.Server.Close` waiting forever on the open registration.
- Every test in `internal/instance` and `internal/daemon` passes
  `-race -count=3`.

### A gap noticed and not closed (queued as 0049)
The heartbeat check runs between writes in the session loop. A supervisor
that keeps sending heartbeats (so the liveness watchdog never fires) but never
reads will, once its socket buffer fills, block a heartbeat write forever. The
loop then never reaches the check, and revocation would never take hold on
that connection. At one heartbeat a few seconds apart, the buffer takes hours
to fill. This phase's bound starts at the check, so it does not cover that
case. Worse, a supervisor that later resumes reading is sent every event
logged during its stall before the line that says revoked. The fix bounds
every write by Liveness, and changes 0046's behaviour for every write, not
only this line, so it is a phase of its own. At the user's direction it is
queued as **0049, "No write after a revocation"**, with the user's further
requirement: once a revocation commits, no write but `revoked` may begin. The
prompt's mechanism is an advisory lock that the revocation takes exclusively
and every write takes shared. Queued 0049–0053 were renumbered 0050–0054
(PLAN §10, the revocation-gate note).

### Deviations
None from the prompt. Two choices it left open:
- `told_supervisor` means confirmed by the far end (above), not "the write
  returned".
- The north-bound history table keeps its heading (`## History`). The
  registration protocol's is `### Registration protocol history`, under its own
  section. 0048's prompt now names the north-bound table and the
  "Four numbers" heading explicitly.
