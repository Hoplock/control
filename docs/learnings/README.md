# Learnings

One file per implemented prompt, named `NNNN-short-description-learnings.md`
matching the prompt it corresponds to (see `docs/PROTOCOL.md` §5).

Each file **must** open with a `## Summary` block so future sessions can decide
whether to read the full body. Sessions read every summary block here at startup
and only open a full file when it's relevant to their prompt.

This folder is the durable hand-off channel between otherwise-independent fresh
sessions. Keep summaries tight; put depth under `## Details`.

Two things belong in a summary here that do not appear in Hoplock Proxy's
equivalent, because they are the two ways a session in this repo gets blocked
by something it cannot see:

- **Database tables and migrations** your phase added.
- **Cross-repo dependencies** — anything that needs a contract change in the
  Hoplock Proxy repository. Name the exact field. A later session must not discover it
  by being blocked halfway through its phase.
