#!/usr/bin/env bash
# Prove that the `exhaustive` linter actually rejects an unhandled enum member.
#
# M3 promises a closed policy vocabulary, and Go holds none of that promise:
# there are no sum types and no exhaustive matching, so `exhaustive` in
# .golangci.yml is the only thing standing between "the vocabulary is closed"
# and "somebody added an obligation kind and three switches kept their old
# behaviour" (PLAN M13). A linter that is enabled but silent is worse than none,
# because it is a guarantee everyone believes in and nobody checks.
#
# So this script writes a deliberately non-exhaustive switch AND a deliberately
# incomplete lookup map into internal/policy/model, runs the repository's own
# linter over that package, and fails unless the linter names both. The `default`
# clause in the switch is part of the fixture: defaulting must NOT count as
# handling the missing member, and that is a setting somebody could silently
# relax.
#
# Run it with `make exhaustive-guard`. The lint job in CI runs it too.
set -euo pipefail

cd "$(dirname "$0")/.."

GOLANGCI_LINT=${GOLANGCI_LINT:-golangci-lint}
if ! command -v "$GOLANGCI_LINT" >/dev/null 2>&1; then
  echo "exhaustive-guard: $GOLANGCI_LINT is not installed; this guard needs it" >&2
  exit 1
fi

guard=internal/policy/model/zz_exhaustive_guard.go
cleanup() { rm -f "$guard"; }
trap cleanup EXIT

cat > "$guard" <<'EOF'
// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

// Written by scripts/exhaustive-guard.sh and deleted by it. If this file is in
// your working tree, that script was interrupted: delete it.

func exhaustiveGuardSwitch(k ObligationKind) string {
	switch k {
	case ObligationRecordSession:
		return "record"
	case ObligationRequireStepUp:
		return "step up"
	default:
		return "something else"
	}
}

var exhaustiveGuardMap = map[Effect]string{
	EffectAllow: "allow",
}

var _, _ = exhaustiveGuardSwitch, exhaustiveGuardMap
EOF

set +e
output=$("$GOLANGCI_LINT" run --enable-only exhaustive ./internal/policy/model/ 2>&1)
status=$?
set -e

if [ $status -eq 0 ]; then
  echo "exhaustive-guard: the linter accepted a switch and a map with a missing enum member." >&2
  echo "exhaustive-guard: the guard M3 depends on is not working (see .golangci.yml)." >&2
  exit 1
fi

fail=0
for want in ObligationRequireApproval EffectDeny; do
  if ! grep -q "$want" <<<"$output"; then
    echo "exhaustive-guard: the linter failed, but never named the missing member $want:" >&2
    echo "$output" >&2
    fail=1
  fi
done
[ $fail -eq 0 ] || exit 1

echo "exhaustive-guard: OK — an unhandled enum member is rejected in a switch and in a map"
