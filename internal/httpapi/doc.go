// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package httpapi holds the two HTTP surfaces and nothing they share by
// accident. South-bound (proxies) and north-bound (humans, CI, GitOps) never
// share a port, a middleware chain, or a credential type (PLAN M2); the split
// into subpackages is what makes that structural rather than aspirational.
package httpapi
