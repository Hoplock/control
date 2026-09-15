#!/usr/bin/env bash
# Pull the contract from the Hoplock Proxy repository and record what was taken.
#
#   make contract-sync                 # take the tip of the default branch
#   make contract-sync REF=<sha|tag>   # pin a specific revision
#
# The contract is owned upstream (PLAN M1). This script is the only supported
# way for it to change in this repository: it replaces contract/control.yaml and
# rewrites contract/UPSTREAM, so the diff a reviewer reads is the whole change.
#
# It must be cheap to run twice in a week. Upstream revises this document often
# — seven revisions landed while phase 0002 was queued — so a sync that needs a
# ceremony is a sync that stops happening.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dir="$root/contract"

REPO="${CONTRACT_REPO:-https://github.com/hoplock/proxy}"
REF="${REF:-main}"
SRC="${CONTRACT_PATH:-api/control.yaml}"
DEST_NAME="$(basename "$SRC")"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "contract-sync: fetching $SRC from $REPO at $REF" >&2

# A blobless partial clone of one ref: enough to resolve the commit and read one
# file, without dragging the proxy's history in.
git clone --quiet --filter=blob:none --no-checkout --depth 1 \
  --branch "$REF" "$REPO" "$work/proxy" 2>/dev/null \
  || git clone --quiet --filter=blob:none --no-checkout "$REPO" "$work/proxy"

commit="$(git -C "$work/proxy" rev-parse --verify "$REF^{commit}" 2>/dev/null \
  || git -C "$work/proxy" rev-parse --verify HEAD)"
commit_date="$(git -C "$work/proxy" show -s --format=%cI "$commit")"

if ! git -C "$work/proxy" cat-file -e "$commit:$SRC" 2>/dev/null; then
  echo "contract-sync: $SRC does not exist in $REPO at $commit" >&2
  exit 1
fi

mkdir -p "$dir"
git -C "$work/proxy" show "$commit:$SRC" > "$dir/$DEST_NAME"

sum="$(sha256sum "$dir/$DEST_NAME" | cut -d' ' -f1)"
today="$(date -u +%Y-%m-%d)"

cat > "$dir/UPSTREAM" <<EOF
# The provenance of everything in this directory.
#
# contract/ is VENDORED from the Hoplock Proxy repository and is generated, not
# authored (PLAN M1). This file records exactly what was taken and from where,
# and \`make contract-check\` recomputes the checksum below and fails if the
# vendored copy has drifted from it. See contract/README.md.
#
# Rewritten by \`make contract-sync REF=<ref>\`; do not edit it by hand.

repository=$REPO
path=$SRC
ref=$REF
commit=$commit
commit_date=$commit_date
synced_at=$today
file=$DEST_NAME
sha256=$sum
EOF

echo "contract-sync: contract/$DEST_NAME now at $commit ($sum)" >&2
echo "contract-sync: review 'git diff contract/' — that diff is the contract change" >&2
