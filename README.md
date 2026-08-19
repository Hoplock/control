# SecureCommandProxy Management

The **management server** for SecureCommandProxy: the Policy Decision Point
(PDP) for a fleet of decrypting SSH bastions.

The bastion terminates a user's SSH connection, opens a fresh one to the target,
and enforces policy on everything in between. It decides nothing on its own.
This server decides everything: who someone is, what they may reach, over which
route, with which channels, requests, forwarding destinations and commands, for
how long, and who approved it — and it holds the record of what happened
afterwards.

The architecture — the two API surfaces, decisions M1–M14, package layout, and
the phased delivery plan — lives in **[`docs/PLAN.md`](docs/PLAN.md)**. Read it
before reading the code.

> Status: specification only. No code yet. `prompts/queued/0001` is the scaffold
> phase; `docs/PROTOCOL.md` explains how a session picks up a prompt and
> delivers it.

## The two surfaces

|  | South-bound | North-bound |
| --- | --- | --- |
| Who calls it | bastions | humans, CI, GitOps |
| What for | authenticate, authorize + route, host keys, log ingest, revocation stream | policy authoring, simulation, "explain this decision", access requests and approvals, audit query, export |
| Auth | bearer token → mTLS | OIDC (humans) + scoped API tokens (automation) |
| Constraint | a user's SSH handshake is held open while it answers | interactive latency, rich responses |

They never share a listener. A bastion token that could reach a policy-authoring
endpoint would be an escalation from "may ask about decisions" to "may author
them" (PLAN M2).

## The contract is upstream

The bastion↔management contract is `api/management.yaml` in the bastion
repository, [`mauroasilva/SecureCommandProxy`](https://github.com/mauroasilva/SecureCommandProxy).
This repo vendors a pinned copy under `contract/` and treats it as read-only and
generated (PLAN M1):

- `make contract-sync` pulls a new version and records the upstream commit.
- `make contract-check` fails if the local copy was edited or drifted. CI runs it.
- `make conform` runs `cmd/pdpconform`, a black-box conformance suite, against a
  running implementation. CI runs it against **this server and the bastion
  repo's mock server** — a suite only ever run against the implementation it was
  written beside proves agreement with itself.

A contract change starts in the bastion repo, lands there, and arrives here
through `contract-sync`. Never the other way around.

## Repository layout

| Path | What lives there |
| --- | --- |
| `cmd/management` | the server daemon (both listeners) |
| `cmd/pdpconform` | black-box contract conformance suite |
| `cmd/policyctl` | CLI: validate, simulate, explain, apply a policy bundle |
| `internal/` | implementation packages (see `docs/PLAN.md` §3) |
| `contract/` | **vendored** contract from the bastion repo — never edited here |
| `migrations/` | versioned, forward-only SQL |
| `deploy/` | docker-compose topology: this server + Postgres + a real bastion |
| `docs/` | plan, session protocol, and per-phase learnings |
| `prompts/` | queued and implemented phase prompts |

## Contributing

**Read [`docs/PROTOCOL.md`](docs/PROTOCOL.md) in full before doing any work.**
It defines how a session picks up a prompt, branches, what "done" means, and how
work is handed off to the next session. `docs/KICKOFF.md` has the exact prompt to
start a session with.

Every `.go` file must carry the license header in
[`docs/LICENSE-HEADER.md`](docs/LICENSE-HEADER.md).

Three rules are worth knowing before you read anything else:

1. **Never edit `contract/`** — it is vendored (M1).
2. **`401` means deny, deliberately.** Every other failure is a `5xx`. A bastion
   turns a `401` into "access denied" for a real user, so returning one for a
   database timeout sends an operator to debug permissions during an outage (M11).
3. **Migrations are forward-only** and every table carries the tenant column,
   even though the prototype is single-tenant (M12).

## License

Proprietary and confidential. Copyright (c) 2026 Mauro Silva. All rights
reserved. See [`LICENSE`](LICENSE).
