# `cmd/policyctl`

The operator CLI for policy bundles: validate, simulate, explain, and apply.
It is a client of the north-bound API (PLAN M2), not a second way into the
database — anything it can do, an operator can do over the API, which is what
keeps GitOps and the console telling the same story.

Built alongside the north-bound policy lifecycle in **phase 0014**.
