# Kickoff — starting an implementation session

Copy one of the prompts below into a **fresh** Claude Code session (the repo is
cloned fresh per session). The prompts in `prompts/queued/` are self-contained;
`docs/PROTOCOL.md` tells the session how to pick up and deliver the work.

## Default: implement the next queued prompt

```
Read docs/PROTOCOL.md and follow it. Implement the lowest-numbered prompt
in prompts/queued/. Do not start any other prompt in this session.
```

## Specific prompt (run out of order)

```
Read docs/PROTOCOL.md and follow it. Implement prompts/queued/<NNNN-name>.md.
Do not start any other prompt in this session.
```

## Rules of thumb

- **One session = one prompt = one PR.** Start a fresh session for each queued
  prompt. The session ends when its PR is merged (see `docs/PROTOCOL.md`).
- **Respect dependencies / ordering.** Prompts are numbered in implementation
  order and later ones assume earlier ones are merged. A fresh session branches
  off `main`, so it only sees **merged** work — kick off the next prompt after
  the previous PR merges. Only run prompts in parallel when they genuinely don't
  depend on each other.
- **Don't paste prompt bodies.** Point the session at the file in the repo so it
  reads the canonical version (numbers can change under the invariants in
  `docs/PROTOCOL.md` §6; the file is always current).
- **Hoplock Enterprise is downstream.** A phase here may define an extension
  point that Enterprise implements; it may never import Enterprise or assume it
  is installed (PLAN M15).
- **The Hoplock Proxy repository is a sibling, not a dependency to edit.** A session
  here may need to *read* the proxy's plan or its mock server; it may never
  change them, and it may never edit `contract/` (see `docs/PLAN.md` M1). If a
  phase turns out to need a contract change, that is a separate piece of work in
  the Hoplock Proxy repository — stop and tell the user.
- **Phase 0002 is worth doing early and well.** After it lands, every later phase
  has a conformance suite it did not write itself telling it whether the server
  is correct. Before it lands, "correct" is an opinion.
