# control-PR#22 — two contract ambiguities: the heartbeat interval and read-back

Backfilled from [control#22](https://github.com/Hoplock/control/pull/22), which
predates the request queues and handed over no kickoff: its `## Findings for you
— two contract ambiguities` section, verbatim. Answered by
[hoplock/proxy@8f7677f](https://github.com/Hoplock/proxy/commit/8f7677f) (queued
0039 by a direct commit to main, not a PR) and
[proxy#56](https://github.com/Hoplock/proxy/pull/56) (delivered 0039).

> Recorded, not resolved here (PROTOCOL §3, M1). Neither blocks this phase:
>
> 1. **The heartbeat interval has no field on the wire.** The stream requires heartbeats "at a steady interval" and PLAN §4 asks the suite to grade that they arrive "within the interval the server advertises" — but nothing advertises one. The suite takes it as an input.
> 2. **Nothing in the contract publishes an event or reads a log record back**, which the priority-durability and gap-recovery obligations both need to be gradeable at all. Both are suite inputs, so two of PLAN §4's six obligations cannot be graded against a server that serves only the contract.
>
> Both are upstream changes if you want them closed; say the word and I will write the prompt for a proxy-side PR.
