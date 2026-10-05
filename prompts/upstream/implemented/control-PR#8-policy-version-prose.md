# control-PR#8 — api/README.md's current policy_version

Backfilled from [control#8](https://github.com/Hoplock/control/pull/8), which
predates the request queues and handed over no kickoff: its `## Note for
upstream (not blocking)` section, verbatim. Answered by
[proxy#26](https://github.com/Hoplock/proxy/pull/26) (fixed directly; no prompt
was queued).

> `api/README.md` in the merged proxy tree still says, under "Versioning: additive fields, and a proxy that fails closed", that `policy_version`'s "current value is `3`, exported as `control.PolicyVersion`". The rest of the document, the YAML, and `internal/control/contract.go` (`const PolicyVersion = 4`) all say **4**, so this is a missed line rather than an ambiguity, and I synced against `4`. Flagging it rather than acting on it — the contract is upstream's (M1), and per §3.2 that is a change for its own PR there.
