# Hoplock Control

Open-source control plane for managing Hoplock proxies, identities, targets,
routes, and access policies.

Hoplock Control is the Policy Decision Point (PDP) for a fleet of
[Hoplock Proxy](https://github.com/hoplock/proxy) instances. The proxy is
deliberately thin — it terminates SSH, enforces a decision, and reports what
happened. Everything that *makes* the decision lives here, along with everything
that happens afterwards: the audit record, the replayable session, and the
answer to "why was I denied?".

The architecture — the two API surfaces, decisions M1–M15, package layout, and
the phased delivery plan — lives in **[`docs/PLAN.md`](docs/PLAN.md)**. Read it
before reading the code.

> Status: specification only. No code yet. `prompts/queued/0001` is the scaffold
> phase; `docs/PROTOCOL.md` explains how a session picks up a prompt and
> delivers it.

## Where this fits

| Repository | Role |
| --- | --- |
| [`hoplock/proxy`](https://github.com/hoplock/proxy) | **Enforces** access. SSH proxying, channel and command controls, port-forward policy, multi-hop relay, audit events. |
| `hoplock/control` (this repo) | **Manages** access. Proxies, targets, identities, routes, policies, audit ingest and history, API, console. |
| [`hoplock/enterprise`](https://github.com/hoplock/enterprise) | **Governs** access at organisational scale. Approvals, compliance, retention, SIEM/SOAR, HA. |

**Hoplock Proxy + Hoplock Control is a complete system.** You can self-host both
and have working identity-aware infrastructure access: policy authoring and
simulation, OIDC/SAML login, RBAC, time-boxed grants, a proxy fleet with
configuration distribution, an audit trail you can query, and a console. That is
a design constraint (PLAN M15), not a marketing line — nothing core has been
held back to create a paid limitation.

Hoplock Enterprise **extends** this repository rather than forking it: it
imports Control as a library and implements the interfaces in
[`ext/`](docs/PLAN.md). Control never imports Enterprise, and a test enforces it.

## Two surfaces

|  | South-bound | North-bound |
| --- | --- | --- |
| Who calls it | Hoplock Proxy instances | operators, CI, the console |
| What for | authenticate, authorize + route, host keys, log ingest, revocation stream, config distribution | policy authoring and simulation, "explain this decision", inventory, audit query, fleet health |
| Auth | bearer token → mTLS | OIDC (humans) + scoped API tokens (automation) |
| Constraint | a user's SSH handshake is held open while it answers | interactive latency, rich responses |

They never share a listener. A proxy token that could reach a policy-authoring
endpoint would be an escalation from "may ask about decisions" to "may author
them" (PLAN M2).

## The contract is upstream

The proxy↔control contract is `api/control.yaml` in the
[Hoplock Proxy repository](https://github.com/hoplock/proxy). This repo vendors a
pinned copy under `contract/` and treats it as read-only and generated (PLAN M1):

- `make contract-sync` pulls a new version and records the upstream commit.
- `make contract-check` fails if the local copy was edited or drifted. CI runs it.
- `make conform` runs `cmd/pdpconform`, a black-box conformance suite, against a
  running implementation. CI runs it against **this server and the proxy repo's
  mock control server** — a suite only ever run against the implementation it
  was written beside proves agreement with itself.

A contract change starts in the proxy repo, lands there, and arrives here
through `contract-sync`. Never the other way around.

## Repository layout

| Path | What lives there |
| --- | --- |
| `cmd/hoplock-control` | the server daemon (both listeners) |
| `cmd/pdpconform` | black-box contract conformance suite |
| `cmd/policyctl` | CLI: validate, simulate, explain, apply a policy bundle |
| `ext/` | **public** extension points — the seam Hoplock Enterprise implements |
| `ui/` | management console, embedded into the binary |
| `internal/` | implementation packages (see `docs/PLAN.md` §3) |
| `contract/` | **vendored** contract from the proxy repo — never edited here |
| `migrations/` | versioned, forward-only SQL |
| `deploy/` | docker-compose topology: this server + Postgres + a real proxy |
| `docs/` | plan, session protocol, and per-phase learnings |
| `prompts/` | queued and implemented phase prompts |

## Contributing

**Read [`docs/PROTOCOL.md`](docs/PROTOCOL.md) in full before doing any work.**
It defines how a session picks up a prompt, branches, what "done" means, and how
work is handed off to the next session. `docs/KICKOFF.md` has the exact prompt to
start a session with. If your change touches a surface another Hoplock
repository consumes, `docs/CROSS-REPO-PROTOCOL.md` covers that too.

Four rules are worth knowing before you read anything else:

1. **Never edit `contract/`** — it is vendored from the proxy repo (M1).
2. **Never import Hoplock Enterprise** (M15). The dependency runs one way. If
   you need something from Enterprise, you need an extension point in `ext/`
   with a real default here.
3. **`401` means deny, deliberately.** Every other failure is a `5xx`. A proxy
   turns a `401` into "access denied" for a real user, so returning one for a
   database timeout sends an operator to debug permissions during an outage (M11).
4. **Migrations are forward-only** and every table carries the tenant column,
   even though the prototype is single-tenant (M12).
