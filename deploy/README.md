# Deploy: the end-to-end topology

A **real** Hoplock Proxy enforcing decisions from a **real** Hoplock Control,
over Postgres and real `sshd` targets, on Docker networks that make the
segmentation claims checkable (PLAN §9). The proxy repository proves it
enforces what a mock tells it; this repository proves it decides correctly in
isolation; only here do "decides" and "enforces" meet.

## Running it

```
make e2e        # build, up, run the suite, tear down
make e2e-up     # build and bring it up, and leave it running
go test -tags e2e -count=1 -v ./test/e2e/                           # the suite, against a live topology
go test -tags e2e -count=1 -v -run 'TestTopology/multi_hop' ./test/e2e/
make e2e-down   # stop it and delete everything it generated
```

You need Docker with the compose plugin, Go, `git`, `ssh-keygen` and `openssl`.
`deploy/build.sh` fetches the proxy at the pinned commit into `deploy/.proxy/`;
set `HOPLOCK_PROXY_SRC=<checkout>` to build from a checkout you already have
(CI does). The north-bound API is on `https://127.0.0.1:18081`, over the TLS
this server terminates itself (below), with the token
`ht.default.e2e-operator-secret`, which is how the suite reads this server's
side and how you debug a failure:

```
docker compose -f deploy/compose.yaml logs control proxy-edge proxy-relay
curl -s --cacert deploy/keys/tls_ca.pem -H 'Authorization: Bearer ht.default.e2e-operator-secret' \
  https://127.0.0.1:18081/api/v1/tenants/default/sessions/<sess-…>/decisions
```

Behind an egress proxy that refuses the Debian mirror, `E2E_BASE_IMAGE` names
another base for every image (each Dockerfile's `BASE_IMAGE`); CI uses the
default, `debian:stable-slim`.

## The nodes and networks

| Service | Role | Networks |
| --- | --- | --- |
| `postgres` | the store | `db` |
| `control-init` | migrates, seeds, prints the CA bundle, exits — migrations are an explicit step (PLAN §8) | `db` |
| `control` | this server: `test/e2e/host`, i.e. `hoplock-control` exactly, started as a host starts it (through `internal/daemon`, which `server.Main` forwards to); both listeners serve TLS of their own | `north`, `db`, `south-edge`, `south-zone` |
| `proxy-edge` | the proxy users reach; reaches `core` itself and accepts the relay registration | `south-edge`, `edge`, `core`, `relay` |
| `proxy-relay` | the protected zone's proxy: SSH bound to loopback, no published port, reached only over the registration it opens **outbound** (D11) | `south-zone`, `relay`, `zone` |
| `target` | three sshds (22, 2222, 2223), under the names `target`, `appliance`, `certified`, `ladder`, `restricted`, `cached`, `pq`, `pq-classical`, `pq-sntrup`, `classical`, `sntrup` | `core` |
| `target-zone` | the same image as `zone-host`, inside the protected zone | `zone` |
| `client` | the OpenSSH client every scenario runs | `edge` |
| `probe` | the client image, run by `wire_posture` for one TLS handshake at a time (`compose run --rm probe`); profile `probe`, so `make e2e-up` never starts it | `south-edge` |

Every network but `north` is `internal`. The client is not on `core`, so a
target is reachable only through a proxy; `proxy-edge` is not on `zone`, so a
session that reached `zone-host` went through `proxy-relay`; and the protected
zone shares nothing with the edge but `relay`. The `isolation` scenario asserts
each of these rather than inferring them from a session that worked.

A target is one sshd under several names because a route is selected by the
name the user types: each name is a target row (`control/seed.template.yaml`)
and one rule (`control/policy.yaml`). Its accounts: `root` with the management
key (`ephemeral-user`, D6), `netadmin` with the fleet key (`brokered-key`, D6a,
the appliance stand-in) and `causer`, which trusts this server's tenant CA
through `TrustedUserCAKeys` and has no key at all (`brokered-certificate`). The
CA bundle is what `hoplock-control ca show` printed at start-up, through the
`ca` volume. Its OpenSSH is 9.9 or later, so the default algorithm profile
reaches it (Hoplock/proxy#66) and its sshd on 22 offers `mlkem768x25519-sha256`.

The same host runs two more sshds with the same accounts and keys, differing
only in key exchange (`target/entrypoint.sh`): **2222** offers classical
exchanges only, and **2223** offers `sntrup761x25519-sha512@openssh.com` as its
only hybrid, as OpenSSH 9.0 to 9.8 do. The `pq*` names' routes carry
`algorithm_floor: pq-hybrid-kex` against 22, 2222 and 2223; `classical` and
`sntrup` reach 2222 and 2223 with no floor, so a floor's outage there is the
floor's alone.

## TLS on this server's listeners

Both listeners serve TLS from `control/control.yaml` (PLAN M29), with a
certificate `gen-material.sh` signs from a throwaway CA for `control` and
`127.0.0.1`; the CA's private half is deleted once it has signed. **South
requires** a hybrid post-quantum key exchange (`require_hybrid_kex: true`), so
every request a proxy makes in the run is a handshake that negotiated one;
**north prefers** one, so the suite sees `X25519MLKEM768` with a default client
and a classical-only client still served. The proxies trust the CA through
`SSL_CERT_FILE` (`compose.yaml`), which Go reads on Linux, so nothing on the
proxy is configured for it, and their `base_url` is `https://control:8080`.

## Seed and fixtures

`gen-material.sh` generates every key into `deploy/keys/` — the listeners'
TLS certificate and its CA among them — the tenant CA's
key-encryption key into `deploy/.env`, and renders `control/seed.yaml` from
`control/seed.template.yaml` — none of it committed. `seed` is the
database-direct bootstrap (0019): four subjects (`alice` sre, `bob` contractor,
`carol` sre for the lock scenario, `svc` automation), the targets, the fleet
graph with each proxy's chain identity, one south-bound token per proxy (M22),
one operator API token, and `control/policy.yaml`, one rule per scenario,
each saying which. `control/control.yaml` is the server's configuration;
`proxy/*.yaml` the proxies'.

## The scenarios (`test/e2e/`)

One ordered run, `TestTopology`, because some scenarios restart a component and
the uid assertion spans all of them.

| Scenario | What it proves |
| --- | --- |
| `isolation` | the network restrictions the multi-hop claim rests on |
| `direct_route` | exec and a shell; the decision record explains the allow; the session's output is replayable from the store |
| `multi_hop_relay` | the relay route with no inbound rule; the edge decided `nexthop`/`relay`, the inner proxy authorized for itself with `hop_trail: [proxy-edge]` and was answered with the user's identity; both legs in the audit trail |
| `denied_and_explained` | the vague message to the user, the exact rule to the operator through `explain` (M4) |
| `outage_is_not_a_deny` | a stopped server is an outage with a session id, never "access denied" (M11) |
| `policy_axes` | `sftp` refused beside a shell; one tunnel permitted, another refused; `tcpip-forward` refused with no listener |
| `filtering_tiers` | filtered exec's honest limit (`sh -c` walks past it), restricted exec refusing the same wrapper, each record naming its tier |
| `credential_methods` | `ephemeral-user` creates and removes the account; `brokered-key` changes nothing |
| `uid_floor_survives_restarts` | the uid is strictly higher after a proxy restart, and after a server restart (0007) |
| `brokered_certificate` | a certificate minted per session, its serial on the record resolving to the row, two serials under one cached decision |
| `failed_issuance_is_an_outage` | a cached ladder with this server stopped: an outage, and no `brokered-key` session |
| `revocation` | an operator kills a live session and the user is told why; an invalidated cache is re-decided |
| `jit_grant` | denied, granted, allowed — with the grant on the showcase row — then denied again at expiry (M10) |
| `audit` | the showcase join; the provisioning record's names; the negotiated record and its by-value query; both target observations |
| `declared_algorithm_profiles` | the recorded declaration names every contract profile, every axis non-empty (Hoplock/proxy#72) |
| `wire_posture` | north negotiates `X25519MLKEM768` (from `ConnectionState.CurveID`), still serves a classical-only client, and answers no plaintext; south negotiates `X25519MLKEM768` with the probe and refuses an `X25519`-only handshake, logging why; each listener's start-up line states its posture (M29) |
| `algorithm_floor` | `pq-hybrid-kex` met on the default sshd, the stored negotiated record naming `mlkem768x25519-sha256` beside the floor; an outage, never a deny, with `target.algorithm_policy_unmet` (`key_exchange`, cause `floor`) and nothing provisioned, against the classical sshd and the `sntrup761`-only one; no floor connects to both on `curve25519-sha256`; each sshd's key-exchange observation stored by host and port, beside the host's rungs (Hoplock/proxy#69) |
| `no_uid_issued_twice` | every uid the run produced, strictly increasing per target |
| `nothing_leaked` | no ephemeral account or home survives; the standing accounts are byte-identical |
| `no_refused_gap` | no `logging.gap` with `gap_cause: refused`: nothing the real proxy emitted was refused |
| `audit_chain_verifies` | `audit-verify` over every stream after the run |

The scenarios that need a test host's fake extensions — an inbound action, the
directory's de-provisioning, two directories — and the uid-lease refusal, the
fleet configuration round trip and the stream-unhealthy hint are phase 0072.

## The proxy pin

`deploy/PROXY_REVISION` names the proxy commit the topology builds. It is not
`contract/UPSTREAM`, which pins the document this server implements, but
`deploy/build.sh` refuses a pair whose `api/control.yaml` differ, so a failure
is attributable to one side. **Moving the pin:** put the new commit in the file,
run `make e2e`, and commit the pin with whatever the suite needed, in one PR. A
pin past a contract change needs the re-vendor first (`make contract-sync`).

## What this topology cannot prove

- **A relay registration's liveness.** The contract has no way for a proxy to
  tell this server which registrations it holds, so the seed writes the one
  registration and `fleet.relay_registration_ttl` is a year. A registration
  that drops is invisible to routing until that TTL, and a real deployment has
  no way to keep one believed at all. Raised upstream by this phase's PR.
- **Which session a chain leg belongs to.** The inner proxy mints its own
  session id for its leg; `explain` on the id the user quotes shows the edge
  leg only, and the inner leg is found by subject, target and time.
- **Multi-node event fan-out, HA, external anchoring of the audit chain,
  device posture, real scale and geography.** One node, one region, one
  Postgres, an audit chain anchored nowhere. These are the known gaps that seed
  the next prompts (PLAN §11).
