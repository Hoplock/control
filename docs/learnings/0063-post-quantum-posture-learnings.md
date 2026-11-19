# 0063 — post-quantum posture — Learnings

## Summary
- **Shipped (`v0.50.0`, MINOR, `### Breaking`; no migration; north API and contract unchanged; `server` untouched):** each listener's own TLS (`internal/tlsconf`, `internal/daemon/wire.go`), decision **M29**, the `ext.KeyAlgorithm.String` fallback, the SSH-certificate tripwire, and TLS + the floor scenarios on 0061's topology.
- **Config keys:** `south.tls` and `north.tls`, each `{certificate_file, key_file, client_ca_file, require_hybrid_kex}`. No certificate = plaintext, exactly as before. `client_ca_file` = `RequireAndVerifyClientCert`, transport only (no credential change).
- **Default posture:** TLS 1.3 only, `CurvePreferences` nil (stdlib set, `X25519MLKEM768` first). **The one knob:** `require_hybrid_kex` (default false) refuses a handshake whose `ConnectionState.CurveID` is not `X25519MLKEM768`/`SecP256r1MLKEM768`/`SecP384r1MLKEM1024`, in `VerifyConnection`. A Go client sees `remote error: tls: bad certificate` on first read; the server logs the group and the setting.
- **Refuses the start:** cert without key, key without cert, an unreadable file, `require_hybrid_kex`/`client_ca_file` with no certificate — before the store is opened or a port bound (`daemon.buildWire` in `start`).
- **Start-up line per listener:** `tls`, `min_tls_version`, `client_certificate_required`, `post_quantum_kex` = `required | preferred | not-stated-plaintext-listener`.
- **`ext/` behaviour change:** `KeyAlgorithm.String()` renders an out-of-range value as `"unknown"` (was `"ed25519"`). Safe: nothing legitimately produces one (`generateKey` refuses it, so it is never stored), and `extdefault.parseAlgorithm` maps unknown codes independently. Listed under `### Breaking`.
- **No PQ `KeyAlgorithm` member**, on purpose: no OpenSSH PQ certificate signature exists. `internal/credential/pqtripwire_test.go` fails the build when `x/crypto/ssh` offers any unclassified signature name, and says what to add.
- **What the floor scenarios proved against the real proxy** (`hoplock/proxy@97e97de`): one target host, three sshds, **OpenSSH 10.2p1** (local run on an Ubuntu 26.04 base via `E2E_BASE_IMAGE`; CI's `debian:stable-slim` must be ≥ 9.9 and the scenario fails otherwise). `pq`:22 `pq-hybrid-kex` → stored `target_kex_algorithm: mlkem768x25519-sha256` beside `algorithm_floor: pq-hybrid-kex`; `pq-classical`:2222 (no hybrid) and `pq-sntrup`:2223 (sntrup761 only) → outage, `target.algorithm_policy_unmet` `key_exchange`/`floor`, nothing provisioned; no-floor routes to 2222 and 2223 → `curve25519-sha256`. Kex observations: `pq:22` `pq-hybrid-kex`, the other two `modern-kex`.
- **NEXT:** a phase touching routing has the floor to honour, proven on the wire above; one adding a listener calls `tlsconf.Build` with its own section. mTLS as the south-bound *credential* is still M2's, unbuilt.

## Details

### Why the block is a value, not a pointer
The first draft made `tls:` a `*TLSConfig` whose presence meant TLS. That
breaks two repository conventions: `config.example.yaml` shows every key with
its default (as `supervisor.endpoint: ""` does), and
`TestTheExampleDocumentsEveryKey` walks struct fields, not pointers. So
`TLSConfig` is a value, `Enabled()` is "names a certificate or a key", and an
example block with every key empty is plaintext. The refusals moved to match:
a `require_hybrid_kex` or a `client_ca_file` on a listener with no
certificate is refused, because otherwise it reads as enforced and enforces
nothing — the same failure as a silent fallback, from the other side.

### Where the TLS is built, and why there
`daemon.start` calls `buildWire(cfg)` right after `prepareExtensions` and
before `store.Open`, so a wrong path fails before anything is opened or
bound (`TestATLSBlockThatCannotBeHonouredRefusesTheStart` runs with a DSN
that answers nothing). `serve` takes the result and sets `TLSConfig` on each
`http.Server`; `serveListener` calls `ServeTLS(ln, "", "")` because the daemon
already splits listen from serve (for `self.SetListener`). The certificate is
read once; rotation is a restart. A reloading `GetCertificate` would be a
later phase's if anyone needs it.

`tlsconf.Build` re-runs `TLSConfig.Validate` (exported for it) even though the
loader already did: it is the last thing between a wrong setting and a
listener, and a caller may hand it a block that never went through the loader.

### The refusal and where it shows
`VerifyConnection` runs after the group is chosen, on every handshake,
resumptions included. In TLS 1.3 the client's `Handshake()` returns before the
server checks the client's Finished, so the client learns of the refusal on its
first read (`remote error: tls: bad certificate`, alert 42). The server's side
is `http: TLS handshake error … tlsconf: the handshake negotiated X25519, which
is not a hybrid post-quantum key exchange, and south.tls.require_hybrid_kex is
set`, which reaches the JSON log at INFO because `slog.SetDefault` also routes
the `log` package (http.Server's default `ErrorLog`). Nothing here set
`ErrorLog`, deliberately: doing so would change a plaintext deployment's logs,
and the acceptance criterion is that one behaves exactly as before.

`CurvePreferences` stays nil. Since Go 1.24 the default includes
`X25519MLKEM768`; since 1.26, the two NIST hybrids too; and in 1.27 the list's
order is ignored anyway (the library uses its own preference order), so a
typed list could only ever freeze the *set*. `GODEBUG=tlsmlkem=0` would remove
the hybrids from the default — the start-up line cannot see that, and
`require_hybrid_kex` is what catches it.

`hybridGroups` is a slice, not a map, so `exhaustive`'s map check does not
demand every `tls.CurveID` member, and a group the stdlib adds later is
classical until it is added here — erring towards refusing under
`require_hybrid_kex`.

### Tests
- `internal/tlsconf`: builder refusals (seven cases), the defaults (1.3, nil
  curves, no verifier, no client auth), the hybrid set, a handshake per section
  through `httptest.NewUnstartedServer` asserting `CurveID ==
  X25519MLKEM768`, `require_hybrid_kex` in both directions, the refusal's
  error, `client_ca_file`, and the posture attributes. `tlstest` writes a
  fresh CA and certificates per test.
- `internal/config/tls_test.go`: no block is plaintext with nothing defaulted;
  the blocks are independent; the load-time refusals.
- `internal/daemon/wire_test.go`: the start refused with nothing bound (no
  database), and — with Postgres — `Main` serving south TLS requiring the
  hybrid with north plaintext, then the reverse, the posture asserted from
  each listener's log line.
- `ext/identity_test.go`: every member's code, and three out-of-range values
  rendering `unknown`.

### The topology (`deploy/`, `test/e2e/posture_test.go`)
- `gen-material.sh` signs one certificate (`control`, `localhost`,
  `127.0.0.1`) from a throwaway CA with `openssl` and deletes the CA key. The
  README now lists `openssl` as a requirement.
- South `require_hybrid_kex: true`, north preferred. The proxies trust the CA
  through `SSL_CERT_FILE` (Go reads it on Linux), so no proxy option was
  needed and nothing upstream is owed; their `base_url` is `https://`.
- The south listener is not reachable from the host, and testing it from the
  host by publishing it would weaken the segmentation 0061 asserts. So a
  `probe` service (client image + `openssl`, profile `probe`, `south-edge`
  only) is run per handshake with `compose run --rm`. OpenSSL 3.5's `s_client
  -brief` prints `Negotiated TLS1.3 group:`; "served" means an HTTP status line
  came back, because of the late TLS 1.3 refusal above.
- The target runs sshds on 2222 (classical only, the upstream list) and 2223
  (`sntrup761x25519-sha512@openssh.com` + the same classical list). Five new
  target rows / aliases and five rules, two of which have no floor so each
  outage is attributable to the floor alone. Ephemeral-user, so the met floor
  also produces the host's rung observation and `nothing_leaked` covers the new
  sessions.
- **Building locally behind this session's egress proxy:** the Debian mirror is
  refused, so the images were built on a local base (`ubuntu:26.04` from
  `mirror.gcr.io`, packages preinstalled) through `E2E_BASE_IMAGE`, exactly
  what the README documents. CI builds the default. All 21 scenarios passed
  locally; the first run's one failure was the new north check sending no
  bearer token.

### Where an operator reads the posture
- This server's listeners: each one's start-up line (above).
- The proxy→target leg: per route, the floor in force is policy (active
  bundle; `GET …/fleet/algorithms` and the console's algorithms view, 0058);
  per session, the negotiated exchange is the stored
  `target.algorithms_negotiated` record (`GET …/audit/negotiated`, the session's
  records, the console's audit and explain views, 0051); per target, the
  key-exchange observation (`GET …/fleet/target-observations/key-exchanges`).
  No configuration knob was added for the leg: a floor is route policy.

### Decisions
- **M29 added** (register row; rendered in §3, §8, §9, §10). PLAN §6 gains the
  CA-algorithms paragraph and §8 the "What already survives a quantum
  adversary" subsection; neither is a decision. M2 is not amended: client
  certificates authenticate the transport only.

### Cross-repo
- `hoplock/enterprise`: a sync is owed (the `ext` behaviour change, the new
  config keys a host's file may carry, the pin). Queued in
  `prompts/downstream/queued/` (the PR names the file).
- `hoplock/proxy`: none. Nothing in the contract was needed.

### Follow-ups (not queued)
- Certificate reload without restart, if an operator asks for it.
- mTLS as the south-bound credential remains M2's intended production form;
  `client_ca_file` is its transport half only.
