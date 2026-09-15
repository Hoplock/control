# `contract/` — vendored, not authored

Everything in this directory is a **copy** of a document that lives in the
[Hoplock Proxy repository](https://github.com/hoplock/proxy). It is generated
output as far as this repository is concerned (PLAN **M1**), and it is
**read-only here**: nothing in this repository may edit it, and CI fails if
anything does.

| File | What it is |
| --- | --- |
| `control.yaml` | The PEP↔PDP contract, byte-for-byte as upstream's `api/control.yaml`. The filename is upstream's on purpose, so "look at `control.yaml`" means one file in both repositories. |
| `UPSTREAM` | Where the copy came from — repository, path, ref, commit, the date it was taken — and the SHA-256 of the copy. |

## Changing the contract

The change is made **upstream**, merged there, and pulled in here:

```
# in github.com/hoplock/proxy: edit api/control.yaml, open a PR, merge it
make contract-sync REF=<branch, tag, or commit>   # here, afterwards
```

`contract-sync` fetches that ref, replaces `control.yaml`, and rewrites
`UPSTREAM` with the commit it resolved and the new checksum. `REF` defaults to
`main`; pass a commit SHA to pin one deliberately. The resulting diff is the
whole change, and it is reviewed here like any other.

`docs/CROSS-REPO-PROTOCOL.md` governs the ordering: upstream merges first.

## Why editing it locally is the failure this rule prevents

A contract is an agreement between two programs that are built, tested, and
released separately. Each one tests against its own copy. So a local edit here
does not produce a disagreement that anybody notices — it produces **two green
test suites and one broken product**, because this side now conforms to a
document the proxy has never seen.

That failure has no natural discovery point. It is not found by a unit test, a
linter, or a code review that only reads this repository; it is found by a user
whose session does not work. `make contract-check` is the discovery point,
which is why it is a CI job of its own rather than a step inside another one:
when it fails, the failure names itself.

If the contract is wrong, ambiguous, or missing something this repository
needs, **stop and say so** (PROTOCOL §3). The remedy is a change upstream, not
a change here.

## What the checksum does and does not catch

`contract-check` compares the bytes of `control.yaml` against the `sha256` in
`UPSTREAM`. That catches a local edit, a partial sync, and a corrupted copy.

It deliberately does **not** key off anything inside the document — not
`info.version`, and certainly not `policy_version`. Those are two independent
numbers (PLAN §4), neither derived from the other, and the document's version
does not only ever rise: upstream `Hoplock/proxy#53` moved it **down**, from
`4.3.0` to `4.0.0`, while the negotiated policy vocabulary stood still at `4`.
A drift check that read a version out of the file would have called that sync a
downgrade and a hand-edit that left the version alone no drift at all.

The Go types for these payloads live in `internal/contract/`, and their enum
constants are tested against this document rather than against memory.
