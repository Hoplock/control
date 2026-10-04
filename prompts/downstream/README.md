# Downstream syncs

Syncs this repository's merged changes owe `hoplock/enterprise` — a change to
`ext/` (M15), or to any other surface Enterprise consumes
(`docs/CROSS-REPO-PROTOCOL.md` §3.1, §4.1, §4.3). Each file is the "Downstream
sync" block from `docs/KICKOFF.md`, filled in and queued by the PR that owes it.

- **The work is done in `hoplock/enterprise`.**
- **Named `control-PR#<n>-<short-description>.md`**, after the PR that queued
  it, and committed to that PR once it is open.
- **Waiting from the moment that PR merges.** Nothing here is numbered, and the
  default kickoff never reaches it: the "Next cross-repo request" kickoff in
  `docs/KICKOFF.md` answers the oldest request across all three repositories, in
  a session with all three checked out.
- **Moved to `implemented/`**, unchanged, by a PR here that merges after the
  sync PR that answered it.

Syncs owed **to** this repository, by `hoplock/proxy`, wait in the proxy's
`prompts/downstream/queued/`.
