# Upstream requests

Needs this repository has raised for `hoplock/proxy` — a contract field,
endpoint or enum value the proxy has not defined (`docs/CROSS-REPO-PROTOCOL.md`
§3.2, §4.2, §4.3). Each file is the "Upstream request" block from the proxy's
`docs/KICKOFF.md`, filled in and queued by the PR that needed it.

- **The work is done in `hoplock/proxy`**, and what it produces there is a
  queued prompt, not the change: that is a later phase of the proxy's own.
- **Named `control-PR#<n>-<short-description>.md`**, after the PR that raised
  it, and committed to that PR once it is open — one file per need.
- **Waiting from the moment that PR merges.** Nothing here is numbered, and the
  default kickoff never reaches it: the "Next cross-repo request" kickoff in
  `docs/KICKOFF.md` answers the oldest request across all three repositories, in
  a session with all three checked out.
- **Moved to `implemented/`**, unchanged, by a PR here that merges after the
  proxy PR that answered it.

Requests made **of** this repository, by `hoplock/enterprise`, wait in *its*
`prompts/upstream/queued/`.
