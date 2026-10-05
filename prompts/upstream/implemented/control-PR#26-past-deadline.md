# control-PR#26 — the untested past-deadline path

Backfilled from [control#26](https://github.com/Hoplock/control/pull/26), which
raised this in its learnings file rather than its body;
[proxy#58](https://github.com/Hoplock/proxy/pull/58) names control#26 as where
its prompt came from. The passage, verbatim from
`docs/learnings/0005-policy-model-and-engine-learnings.md` as control#26 merged
it:

> 3. **A matched grant always supplies a deadline.** In `eval.deadline()`, a
>    non-nil grant means `earlier(out, grant.ExpiresAt)` runs even when the route
>    authored no duration, so the zero value is replaced. Verified in
>    `hoplock/proxy`: `session.go` arms the timer *before anything is provisioned
>    or dialled*, and `deadline.go`'s `waitUntil` returns immediately on `d <= 0`,
>    so an already-passed deadline expires the session at once — a stale
>    grant-gated decision never reaches the target. (That path has no test
>    upstream; the behaviour is clear from the code, not from a case.)

Answered by [proxy#58](https://github.com/Hoplock/proxy/pull/58) (queued 0041)
and [proxy#59](https://github.com/Hoplock/proxy/pull/59) (delivered 0041).
