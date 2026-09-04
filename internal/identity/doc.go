// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package identity brokers federated identity — OIDC and SAML — and maps IdP
// claims and groups into policy attributes through an explicit, versioned
// mapping (PLAN M7). It also orchestrates MFA. The proxy never talks to an
// IdP; everything identity-shaped that it reports is resolved here.
package identity
