// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package north serves the operator-facing API on the north-bound listener:
// policy authoring and lifecycle, inventory, simulation, explanation, and
// audit query (PLAN M2). Its authentication is OIDC for humans and scoped API
// tokens for automation, and it is never routable from the south-bound port.
package north
