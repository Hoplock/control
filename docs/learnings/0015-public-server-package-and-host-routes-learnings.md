# 0015 — public server package and host routes — Learnings

## Summary
- **What shipped:** public `server/` (M15): `Main` is Control's whole command line, `Run` the daemon; a host adds a registry, config sections, north-bound routes and error codes. `hoplock-control` is `server.Main(ctx, os.Args[1:], os.Stdout, os.Stderr, server.Options{})` plus its signals. Cuts **`v0.2.0`**.
- **Exported, verbatim** (no divergence from the prompt's block):
  ```go
  func Main(ctx context.Context, args []string, stdout, stderr io.Writer, o Options) error
  func Run(ctx context.Context, config io.Reader, o Options) error // logs to os.Stderr, becomes slog's default
  type Options struct { Provider string; Registry *ext.Registry; HostSections []string; HostConfig func(section string, raw []byte) error; Routes []Route; ErrorCodes []string }
  type Route struct { Method, Pattern, Permission, Summary string; Handler http.Handler } // at /api/v1/tenants/{tenant}/<Pattern>, AccessTenant
  func CallerFrom(ctx context.Context) (Caller, bool)
  type Caller struct { Tenant ext.Tenant; Subject ext.Subject; Principal string; BreakGlass bool; CorrelationID string } // Subject: ID+Groups, zero for a token; Username/ExternalID never filled
  func WriteError(w http.ResponseWriter, r *http.Request, status int, code string, params map[string]any, message string)
  ```
- **Behaviour beyond the block:** `HostConfig` runs before the registry is sealed (a host may register what it builds from its section); every subcommand accepts host sections, only the daemon calls `HostConfig`.
- **Permissions, as merged:** approval inbox list `grant:read`; approve/reject `grant:approve`; archive search, reports read `audit:read`; report schedules/campaigns change **`report:write`** (new, `admin` only); licence state **`license:read`** (new, every role but `integration`); in-tenant governance `identity:read`/`identity:write`; policy change proposed/approved (Enterprise 0009, row added) `policy:write`/`policy:publish`; cross-tenant reporting (E11): **not served**.
- **Start-up refusals** (all before a file is read or a port bound): routes/sections/codes with no `Provider`; `Provider == ext.ProviderControl`; sections without `HostConfig`; a section not lower-case snake, repeated, or a key Control defines; a code not snake_case, repeated, or Control's; a route with empty/unknown method (GET POST PUT PATCH DELETE only), empty pattern, leading `/`, `{tenant}`/`{tenant...}`, `..`, a space/method prefix, a permission Control lacks, nil handler, a duplicate, a pattern on a path Control serves, or a mux conflict (an error, never a panic). `HostConfig`'s error fails `Run` before any bind.
- **`WriteError` renders `500 internal` and logs instead for:** a call outside a host route; an undeclared code; status outside 400–599; `401` or code `unauthenticated`; `internal` with a non-5xx; an empty message.
- **Moved** with `git mv`, `cmd/hoplock-control` → `internal/daemon`: `accesscontext auditread auditverify grants identityctl main migrate north publish seed serve version` `.go` and their five `_test.go`. `run` became `daemon.Main`; `-X` is now `…/internal/daemon.version`.
- **Decisions:** none added; **M15 revised in place** (names `server/`; register row gains §6, §8). §3, §6, §8, §10, PROTOCOL §3 revised. **Migrations:** none.
- **Cross-repo:** sync for `hoplock/enterprise` queued as `prompts/downstream/queued/control-PR#56-public-server-package.md`; request to `hoplock/proxy` (CROSS-REPO §1 table + stale paragraph, enterprise#8's half) as `prompts/upstream/queued/control-PR#56-shared-surfaces-table.md`. **Enterprise's next request:** a cross-tenant access class for E11.

## Details

### Where this differs from what the prompt sketched, and why

- **The config decoder keeps today's line numbers.** The prompt said: decode into
  a `yaml.Node`, remove the host keys, decode the rest strictly. Re-encoding a
  node renumbers the document (blank lines go), so every other refusal in an
  Enterprise deployment would name a line that is not that line of the file.
  `config.ParseHost` instead strict-decodes the bytes the operator wrote and drops
  exactly the refusals yaml.v3 raises for the declared top-level keys, spelled the
  way yaml.v3 spells them (`line N: field K not found in type config.Config`,
  computed from the node's own line). It fails **closed**: if a yaml.v3 release
  rewords that line, nothing is dropped and the host section is refused at
  start-up, which `TestAHostSectionIsAcceptedAndHandedBackUndecoded` catches.
  The node is still decoded once, to find and re-encode each section.
  `config.Parse`/`Load` keep their signatures and call `ParseHost`/`LoadHost`
  with no sections.
- **Host codes are per server, not appended to `north.AllCodes`.** A process
  that builds two servers (every test binary here does) must not leak one's codes
  into the other. `north.Server` holds Control's codes plus the host's.
- **`WriteError` refuses three more things than the prompt listed**: the code
  `unauthenticated` (the same M11 rule as the `401`), `internal` with a non-5xx
  status (it always means an outage), and an empty message (M21 requires one).
- **A host may not add a method to a path Control serves** (e.g. `PUT grants`).
  The prompt asked for duplicates to be refused; a new method on Control's own
  path is not a duplicate, but it would make the 405's `Allow` and the listing
  describe a path that is half Control's.
- **Validation runs twice, from one implementation.** `daemon.Host.Validate`
  calls `north.CheckHost`, which performs the same registration `north.New` does
  against a table nothing serves (`north.Table`). So a host binary that can never
  start fails before the file is read, and there is still exactly one place a
  route is accepted.
- **Signals moved to `main`, and subcommands now take the context.** Before,
  only the daemon handled SIGINT/SIGTERM; a subcommand died with the process.
  Now `main` cancels a context every subcommand uses (it used
  `context.Background()`), so `migrate` interrupted mid-file rolls that file back
  and returns an error. Stated in `CHANGELOG.md`.
- **`server.Run` logs to `os.Stderr`.** The prompt's `Run` has no writer, and
  adding an `Options` field for it would widen the promise for a test's sake.
  `daemon.Run` takes the writer; `server.Main` passes its `stderr`.

### Checked against Enterprise's prompts

Item 4 asked for the table to be checked against `hoplock/enterprise`'s queued
prompts (read at `14b5671`). The rows hold. One row was missing: Enterprise
0009's "approval before activation" proposes and approves policy changes, which
are `policy:write` and `policy:publish` — codes that already exist, so nothing
new was needed. Enterprise 0003's "list mine" is a requester's own view, which is
0016's self-scoped route, not a host route.

### What a host does not get (deliberately)

A cross-tenant route (E11's operator reporting needs an access class M18 has not
decided; Enterprise will raise it), a self-scoped route (0016's `AccessSelf` is
Control's own), a permission or role of its own, a subcommand of its own (it
dispatches before `server.Main`), a south-bound route, or a write into Control's
audit chain.

### Tests worth knowing about

- `server/server_test.go` starts a host for real — migrated schema, two free
  ports, a fake `ArchiveStore`, one section, two routes — and reads the start-up
  log for the sealed set. It also proves `migrate` with a host section, a
  `HostConfig` error failing `Run` with both ports still free, and that every
  refusal fires with a configuration path that does not exist.
- North tests now build every server with two host routes, so the two isolation
  tests enumerate them; each asserts it did. `TestNoRouteOfTheNorthBoundTableIsReachableHere`
  (south) walks `north.Table` with host routes and expects 404 for each.
- Three import guards in `architecture_test.go`, with a throwaway-tree negative
  case: `server` imported only by `cmd/hoplock-control` and itself; `ext` imports
  nothing from `server`; `cmd/hoplock-control`'s non-test files import `server`
  and the standard library only.
- `ext/README.md`'s host example is `server/example_test.go` verbatim, held by
  `TestTheReadmeExampleIsTheCompiledOne` in `server/`.
- Conformance run locally against the rebuilt binary: 36/36.
