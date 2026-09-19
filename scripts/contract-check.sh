#!/usr/bin/env bash
# Verify the vendored contract is exactly what contract/UPSTREAM says it is.
#
# This is the check that catches a local edit of contract/control.yaml (PLAN
# M1). It is a CI job of its own so that the failure names itself rather than
# arriving as a line in the middle of a build log.
#
# It compares bytes against the recorded SHA-256 and nothing else. In
# particular it never reads a version out of the document: the document version
# and the negotiated policy vocabulary are two independent numbers, and the
# document version does not only ever rise (see contract/README.md).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dir="$root/contract"
upstream="$dir/UPSTREAM"

if [ ! -f "$upstream" ]; then
  echo "contract-check: $upstream is missing; run 'make contract-sync'" >&2
  exit 1
fi

# Read the key=value lines, ignoring comments and blanks.
field() {
  sed -n -E "s/^$1=(.*)$/\1/p" "$upstream" | tail -n 1
}

file="$(field file)"
want="$(field sha256)"
commit="$(field commit)"

for pair in "file:$file" "sha256:$want" "commit:$commit"; do
  if [ -z "${pair#*:}" ]; then
    echo "contract-check: $upstream has no '${pair%%:*}' field" >&2
    exit 1
  fi
done

target="$dir/$file"
if [ ! -f "$target" ]; then
  echo "contract-check: $target is missing; run 'make contract-sync'" >&2
  exit 1
fi

got="$(sha256sum "$target" | cut -d' ' -f1)"

if [ "$got" != "$want" ]; then
  cat >&2 <<MSG
contract-check: contract/$file does not match contract/UPSTREAM.

  recorded: $want  (from $commit)
  actual:   $got

The vendored contract is generated, not authored (PLAN M1). If you edited it
here, revert that edit: the change belongs in the Hoplock Proxy repository, and
arrives here through 'make contract-sync REF=<ref>'. If you meant to take a new
upstream revision, run that target so UPSTREAM is rewritten with it.
MSG
  exit 1
fi

echo "contract-check: contract/$file matches $commit ($want)"
