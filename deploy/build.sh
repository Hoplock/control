#!/bin/sh
# Builds the Linux binaries the end-to-end topology's images copy in
# (deploy/README.md):
#
#   deploy/bin/hoplock-proxy    the REAL proxy, at the commit PROXY_REVISION pins
#   deploy/bin/hoplock-control  this server, as the e2e test host
#                               (test/e2e/host: cmd/hoplock-control plus the fake
#                               extensions three scenarios drive)
#
# Binaries are built on the host rather than in a builder stage, for the reason
# the proxy repository gives: the toolchain and module cache are already here, in
# CI as well as locally, and a second Go environment inside Docker would be a
# second thing to keep on the version policy.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
checkout="$here/.proxy"

repository=$(sed -n -E 's/^repository=(.*)$/\1/p' "$here/PROXY_REVISION")
commit=$(sed -n -E 's/^commit=(.*)$/\1/p' "$here/PROXY_REVISION")
if [ -z "$repository" ] || [ -z "$commit" ]; then
	echo "build: deploy/PROXY_REVISION names no repository or commit" >&2
	exit 1
fi

# The checkout is reused when it is already at the pinned commit, so a local
# re-run costs nothing; anything else is fetched afresh. HOPLOCK_PROXY_SRC points
# at an existing checkout instead (CI's actions/checkout, or a proxy branch under
# development), and is then used exactly as it is.
if [ -n "${HOPLOCK_PROXY_SRC:-}" ]; then
	checkout=$HOPLOCK_PROXY_SRC
	commit=$(git -C "$checkout" rev-parse HEAD)
	echo "build: using the proxy checkout at $checkout ($commit)"
elif [ "$(git -C "$checkout" rev-parse HEAD 2>/dev/null || true)" != "$commit" ]; then
	rm -rf "$checkout"
	git init -q "$checkout"
	git -C "$checkout" remote add origin "$repository"
	git -C "$checkout" fetch -q --depth 1 origin "$commit"
	git -C "$checkout" checkout -q FETCH_HEAD
fi

# The proxy must speak the contract this server implements. The pin is a
# binary, not a document, so nothing else would notice if it did not.
want=$(sed -n -E 's/^sha256=(.*)$/\1/p' "$root/contract/UPSTREAM")
got=$(sha256sum "$checkout/api/control.yaml" | awk '{print $1}')
if [ "$want" != "$got" ]; then
	echo "build: the proxy at $commit speaks a different contract than contract/control.yaml" >&2
	echo "       (api/control.yaml sha256 $got, contract/UPSTREAM sha256 $want)." >&2
	echo "       Re-vendor first (make contract-sync) or pin a proxy that matches." >&2
	exit 1
fi

mkdir -p "$here/bin"
CGO_ENABLED=0 GOOS=linux go -C "$checkout" build -trimpath \
	-o "$here/bin/hoplock-proxy" ./cmd/proxy
CGO_ENABLED=0 GOOS=linux go -C "$root" build -trimpath -tags e2e \
	-o "$here/bin/hoplock-control" ./test/e2e/host
echo "build: hoplock-proxy @ $commit, hoplock-control @ $(git -C "$root" rev-parse --short HEAD)"
