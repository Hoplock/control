# 0066 — Management console 13: a visual policy editor

## Read first
- `docs/PROTOCOL.md` — session workflow, and §8's "A console phase shows what
  it built".
- `docs/PLAN.md` — §2: **M3** (policy is data compiled into a decision
  program: a closed input and output vocabulary, which is what makes a
  structured editor possible at all), **M4**, **M11**, **M20**, **M21**
  (operator-authored content — rule ids, labels, policy source — is data, never
  translated) and **M23**; §5.1 (inputs), §5.2 (outputs), §5.3 (evaluation:
  the first rule that matches answers, and default deny) and §5.6 (the
  lifecycle: a stored version is its source and that source's digest).
- **`ui/DESIGN.md` — in full.** §7 on composites and on keyboard paths, §8 on
  accessibility (a drag has a keyboard equivalent), §9 on identifiers.
- `docs/learnings/` — read summaries; open `0053-console-policy` **in full**
  (the Policy screens, the candidate editor this builds beside, the source and
  diff views, how a rejection and a finding render from their codes, and
  `ui/test/policy-codes.json` as the pattern for holding the console to the
  server), `0049` and `0050` (registering a screen with every harness), `0005`
  (the bundle schema and every rejection) and `0060` (the consistency audit,
  which ran before this phase — see "Why last").

## Why last
The operator asked for this after 0053 shipped a text editor: "it would be
good to have a more visual way of editing policies". It is queued after the
console series, so **0060's consistency audit has not seen it**. This phase
holds its screen to everything 0060 checked, using 0060's own checklist and
tests, and says so in its learnings.

## Objective
Author a policy without writing YAML. Show a bundle as what it is: rules in
the order they are tried, each one a match and an effect, with what it routes
to. Let an author build and change one through choices the closed vocabulary
(M3) already enumerates. The result must be the same candidate 0053's editor
validates, compares, simulates, uploads and activates. **Nothing about storage
or the API changes: a version is still its source (§5.6).**

## In scope
- **A visual view of a candidate**, beside 0053's source view, on the same
  candidate (`/console/policy/candidate`, with `?view=visual` the deep link,
  and "Edit visually" from a version). The two are one candidate shown two
  ways. An edit in either is the candidate. A source that does not parse
  disables the visual view and shows the rejections at their lines, as 0053
  does.
- **The rule list in evaluation order**, with the default deny shown as the
  last, implicit rule. Each rule is a card showing its id, its effect (in the
  status vocabulary), its match axes as chips (subject, device, context,
  target, grant), a one-line summary of its route, and its obligations and
  cache hint. Reordering is by drag **and** by keyboard (move up and down,
  announced). Because the first match answers (§5.3), the view says when a
  move makes a rule unreachable: the compiler's `rule.unreachable` rejection
  renders on the card it names.
- **Editing through closed choices.** Every enumerated field is a choice taken
  from the vocabulary, never typed free text: effect, intent, channel types,
  request types, filter mode, credential method, rungs, obligations and
  cache-key components. Label keys and values, and groups, come from the
  bundle's own `labels:` and `groups:` declarations, editable in a vocabulary
  panel of their own. Free text only where the schema is free: ids, reasons,
  descriptions, hostnames, CIDRs, executables. Each free-text field is checked
  where the server checks it, with the server's code and parameters.
- **The vocabulary comes from the server, never from a list typed by hand.**
  Generate it from `internal/policy/model`'s `Kind` enums into a file the
  console builds from, and hold that file to the server with a Go test, as
  0053's `policy-codes.json` is held. A north-bound route is the alternative.
  It moves the API version (M19) and is the heavier choice; if taken, say why.
- **Round trip without loss.** The editor works on the source's document,
  not on a re-serialised copy. An edit must never silently drop a comment,
  reorder unrelated keys, or change a value it did not touch: a version's
  digest is its source's (§5.6), and a reviewer reads the diff. Use a
  comment-preserving YAML document model; choosing and pinning it is this
  phase's dependency decision, within DESIGN.md §11's bundle budget. A part of
  the schema the visual view does not model — a field it cannot edit — is
  shown read-only with "edit in source", never dropped.
- **Findings and rejections on the cards.** Each rejection and publish-time
  finding that names a rule renders on that rule's card, from its code (0053's
  `policyCodes.ts`); one at document scope heads the list.
- **"Where would this land?"** — a trace panel. The author picks a subject,
  its groups, a target (hostname, labels, zone) and an instant. The
  candidate's dry-run (`POST …/policy/simulate` with `cases`, `policy:read`,
  which 0053 did not use) answers, and the visual view marks the rule that
  matched, the terms that matched, and the effect — or the default deny.
  0053's replay stays where it is, on the candidate's Simulate tab.
- **Components** the editor needs that the set lacks (a sortable list with a
  keyboard path, a chip editor bound to a closed set) join the component set
  with every state in the gallery (0050). None is built inside the screen.

## Out of scope
- Any change to the bundle schema, the compiler, policy semantics or how a
  version is stored, and any route besides the optional vocabulary route.
- Activation and rollback, which are 0053's dialog, unchanged.
- Collaborative or concurrent editing, drafts stored on the server, and any
  locale other than English.

## Acceptance criteria

**Function**
- **A round trip is lossless.** For every bundle in the browser fixture
  (`ui/e2e/fixture/policy*.yaml`), and a corpus this phase adds that uses
  every section of the schema at least once, loading it into the visual view
  and serialising it back yields byte-identical source. After one
  edit through the visual view, the server's `POST …/policy/diff` against the
  original names exactly the rule or document key that was edited, and no
  comment is lost. This runs as a test.
- **End-to-end, headless:** a candidate is built visually — a rule added,
  matched on a group and a label, routed, reordered above another — then
  validated, traced to show which rule a request lands on, uploaded, and
  activated through 0053's dialog.
- An edit that makes a rule unreachable shows `rule.unreachable` on that rule's
  card. A source that does not parse disables the visual view and shows its
  rejections at their lines.
- **Every enumerated choice is the server's:** the generated vocabulary file
  equals the Go enums (a Go test), and no option list in the editor is written
  by hand (a test over the source).
- **RBAC as 0053:** every role that reads policy may build, validate and
  trace; only `policy:write` is offered Upload. The API refusal is asserted,
  not just the hidden button.

**Design and localisation**
- The screen is registered with every browser check, and each passes: axe,
  the screenshot baseline (both themes, loading, empty, error, partial),
  offline, reduced motion, and both pseudolocale runs. Rule ids, labels and
  source stay LTR, mono and untranslated.
- Reordering, adding, removing and editing are each fully keyboard operable,
  and each announces its result.
- 0060's consistency checklist, applied to this screen, passes. The learnings
  record anything it found.

## Definition of Done & hand-off
**Screenshots in the PR** (`docs/PROTOCOL.md` §8): every state, light and dark
side by side, both pseudolocales, and the trace panel.

Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0066-console-visual-policy-editor-learnings.md`. Its summary
block gives the YAML document library chosen and why, how the vocabulary
reaches the console, and what the visual view does not model, so a later
phase can extend it. Record any change to `ui/DESIGN.md` with its reason. A
change to the component set or the API owes a downstream sync for
`hoplock/enterprise` (`docs/CROSS-REPO-PROTOCOL.md` §4.1).

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
