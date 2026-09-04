#!/usr/bin/env bash
# Verify the per-file SPDX licence header on every Go file authored here.
#
# The header is defined verbatim in docs/LICENSE-HEADER.md (PLAN M14): two
# comment lines, then a blank line, then whatever the file says next. Files
# under contract/ are vendored from the Hoplock Proxy repository and are
# excluded — they are not authored in this repository.
set -euo pipefail

copyright_re='^// Copyright \(c\) [0-9]{4} Mauro Silva$'
spdx_line='// SPDX-License-Identifier: Apache-2.0'

mapfile -t files < <(
  find . -type f -name '*.go' \
    -not -path './.git/*' \
    -not -path './contract/*' \
    | sort
)

if [ ${#files[@]} -eq 0 ]; then
  echo "license-check: no Go files found" >&2
  exit 1
fi

fail=0
for f in "${files[@]}"; do
  first=$(sed -n '1p' "$f")
  second=$(sed -n '2p' "$f")
  third=$(sed -n '3p' "$f")

  if ! [[ $first =~ $copyright_re ]]; then
    echo "$f:1: missing or malformed copyright line (see docs/LICENSE-HEADER.md)" >&2
    fail=1
    continue
  fi
  if [ "$second" != "$spdx_line" ]; then
    echo "$f:2: expected '$spdx_line' (see docs/LICENSE-HEADER.md)" >&2
    fail=1
    continue
  fi
  if [ -n "$third" ]; then
    echo "$f:3: expected a blank line after the licence header (see docs/LICENSE-HEADER.md)" >&2
    fail=1
  fi
done

if [ $fail -ne 0 ]; then
  echo "license-check: ${#files[@]} file(s) checked, some are missing the header" >&2
  exit 1
fi

echo "license-check: ${#files[@]} file(s) OK"
