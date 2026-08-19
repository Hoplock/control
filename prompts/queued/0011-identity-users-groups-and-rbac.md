# 0011 — Identity, users, groups, roles & RBAC

## Read first
- `docs/PROTOCOL.md` — session workflow, especially §3 (never edit `contract/`)
  and §5 (cross-repo dependencies belong in your summary).
- `docs/PLAN.md` — especially **§2 (M7)**, §6 (federation, MFA, credential
  brokerage).
- `docs/learnings/` — read summaries; open `0007` (the identity-resolution and
  MFA provider interfaces you now implement), `0005` (how claims and groups
  become policy inputs), `0003` (identity tables).
- In the **Hoplock Proxy repository**, `docs/PLAN.md` **D4** (why the proxy is
  identity-shaped) and **D6a** (`target_auth` and its extensibility).

> Scope note: this phase carries the **open-source** identity story — local
> users, groups, roles, RBAC, and OIDC/SAML federation good enough to run a
> company on. SCIM provisioning and multi-IdP/advanced enterprise SAML are
> Hoplock Enterprise extensions behind `ext.IdentitySync` (0004).

## Objective
Make identity real and short-lived: broker OIDC and SAML, map claims and groups
into policy attributes through an explicit versioned mapping, and stand up the
credential brokerage that removes long-lived target credentials from proxy
disks.

## In scope

### Users, groups, roles, RBAC
- Local users and groups as first-class records, so a deployment works before
  any IdP is connected — and so break-glass access exists when the IdP is down.
- **Roles** with a fixed, documented permission set, and RBAC enforced in one
  place that both the API (0013) and the console (0014) go through. At minimum:
  auditor (read-only), policy author, grant admin, fleet admin, admin.
- Group membership from either source (local or mapped IdP claims) feeds policy
  attributes identically — a rule must not care where a group came from.


### Federation (`internal/identity`)
- OIDC and SAML brokers behind one interface, implementing 0007's
  identity-resolution seam. The proxy still never talks to an IdP.
- **An explicit, versioned claim mapping**: IdP claims and groups → policy
  attributes. Never trust a raw claim name straight through — a claim an IdP
  admin can set is an attribute a policy rule matches on, and the mapping is
  where that trust is granted deliberately. The mapping is data, validated like
  policy is, and it appears in the decision record (M4/§6) because "why did
  Alice match the `sre` rule" is answered by the mapping as often as by the rule.
- Real out-of-band **MFA/step-up** behind 0007's provider interface, keeping the
  deterministic test provider intact for CI.
- **Local credentials are development and break-glass only**: flagged as such in
  every decision and audit record they touch, and never the production path
  (M7). A break-glass login that looks like a normal one is an audit failure.

### Credential brokerage (`internal/credential`)
- An **SSH CA** issuing short-lived, narrowly-scoped target certificates per
  session, so a long-lived management certificate need not sit on every
  proxy's disk. Scope each certificate to the session's principal, target, and
  validity window — the shorter and narrower, the less a stolen one is worth.
- CA key handling: generation, storage, rotation, and what happens to
  outstanding certificates when a key rotates. Say it explicitly; "we rotate"
  without an answer for outstanding certificates is not a rotation story.
- **This needs a contract change in the Hoplock Proxy repository** to reach a proxy:
  `target_auth` is extensible for exactly this (proxy D6a), but the new method
  and its parameters must be added there and synced here (M1). So:
  - build the CA and its API surface here, exercised by tests and by
    `policyctl`;
  - **do not** invent a `target_auth` method locally;
  - write the required upstream change into your learnings as a named,
    precise cross-repo dependency, and tell the user.

## Out of scope
- Editing the contract (M1).
- Device posture collection — the policy model has a slot; nothing collects it.
- **SCIM provisioning/de-provisioning and advanced enterprise IdP features**
  (multi-IdP federation, complex claim-transformation rules, IdP-driven
  de-provisioning): Hoplock Enterprise, behind `ext.IdentitySync`. Define that
  interface's needs here if this phase learns something it must carry.
- A UI for mapping authoring (0013 exposes the API; 0014 builds the console).

## Acceptance criteria
- OIDC and SAML flows are tested against a local test IdP, producing an identity
  whose groups and claims match expectations.
- A claim the mapping does not cover **does not** become a policy attribute —
  test that an unmapped claim cannot influence a decision. This is the phase's
  security test.
- The mapping is versioned; a decision record names the mapping version used.
- Break-glass local authentication is flagged in the decision and audit records —
  asserted, not assumed.
- The CA issues a certificate scoped to principal, target, and window; an
  expired one is rejected; rotation is exercised and the fate of outstanding
  certificates matches what you documented.
- No IdP client secret, private key, or token appears in any log, error, or
  stored row.
- 0007's conformance assertions still pass with the real providers wired in.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0011-identity-federation-and-credentials-learnings.md`. Summary
block MUST give the broker interface, the mapping format and its versioning, the
break-glass flag, the CA's issuance API and rotation story, and — stated as its
own line — **the exact contract change the Hoplock Proxy repository needs** before brokered
certificates can reach a proxy.
