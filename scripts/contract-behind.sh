#!/bin/sh
# Reports whether the vendored contract is behind upstream (PLAN M1; 0061).
#
#   scripts/contract-behind.sh <path to a full hoplock/proxy checkout>
#
# It reports the GAP, never a verdict. A pin that is behind is legitimate
# whenever a prompt names the re-vendor (prompts/audit/cross-repo-impact.md §5):
# a sync vendors nothing, and the owning phase re-vendors. So this names the
# pinned commit, the newest upstream commit that changed api/control.yaml, and
# the commits in between, and leaves "is that owed?" to the prompts.
#
# It is not a substitute for the cross-repo audit either: it sees staleness of
# the ARTIFACT, and the audit sees drift of the TEXT, which no checksum can.
#
# Exit 0 always, unless it cannot answer (exit 2). Under GitHub Actions a gap is
# a warning annotation plus the job summary — amber, not red — because a red
# check would be read as something this repository did wrong.
set -eu

upstream=${1:?usage: contract-behind.sh <proxy checkout>}
here=$(cd "$(dirname "$0")/.." && pwd)
pinned=$(sed -n -E 's/^commit=(.*)$/\1/p' "$here/contract/UPSTREAM")
path=$(sed -n -E 's/^path=(.*)$/\1/p' "$here/contract/UPSTREAM")
want=$(sed -n -E 's/^sha256=(.*)$/\1/p' "$here/contract/UPSTREAM")
if [ -z "$pinned" ] || [ -z "$path" ] || [ -z "$want" ]; then
	echo "contract-behind: contract/UPSTREAM names no commit, path or sha256" >&2
	exit 2
fi
if ! git -C "$upstream" cat-file -e "$pinned^{commit}" 2>/dev/null; then
	echo "contract-behind: $pinned is not in $upstream (a shallow clone?)" >&2
	exit 2
fi

head=$(git -C "$upstream" log -1 --format=%H -- "$path")
got=$(git -C "$upstream" show "HEAD:$path" | sha256sum | awk '{print $1}')

summary() {
	if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
		printf '%s\n' "$@" >> "$GITHUB_STEP_SUMMARY"
	fi
	printf '%s\n' "$@"
}

if [ "$got" = "$want" ]; then
	summary "The vendored contract is current: upstream $path last changed at $head," \
		"and its bytes equal contract/control.yaml (pinned at $pinned)."
	exit 0
fi

between=$(git -C "$upstream" log --format='- %h %s' "$pinned..HEAD" -- "$path")
summary "## The vendored contract is behind upstream" "" \
	"- pinned: \`$pinned\` (contract/UPSTREAM)" \
	"- upstream \`$path\` last changed at: \`$head\`" "" \
	"Commits that changed it since the pin:" "" "$between" "" \
	"This is a notice, not a fault. If a queued prompt names the re-vendor, nothing is owed; otherwise the next sync or phase that touches the contract re-vendors with \`make contract-sync REF=$head\`."
if [ -n "${GITHUB_ACTIONS:-}" ]; then
	echo "::warning title=contract behind upstream::contract/ is pinned at $pinned; upstream $path changed since, last at $head"
fi
