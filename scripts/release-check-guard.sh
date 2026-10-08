#!/usr/bin/env bash
# Prove that the release check refuses what it must (PLAN M23).
#
# scripts/release-check.sh is the only thing holding a version number to what
# the change behind it is. Hoplock Enterprise pins those numbers (its E3) and
# implements ext's interfaces (M15), so a check that is wired into CI but
# silent would be worse than none: every release would carry a guarantee that
# nobody checks. This script checks the check, in the style of
# exhaustive-guard.sh.
#
# It cannot do that in this repository. The cases need tags, and a tag created
# here is one `git push --tags` away from being a published version, which can
# never be taken back. So it builds a throwaway repository instead: a module
# with a stand-in for `ext` (an interface another module implements), a public
# package that no list names (`widget`), an `internal/` package, a
# `package main`, one implemented prompt, a changelog, and v0.1.0. It copies
# the real script in, then runs it once per case, each case starting from that
# released state, and fails unless every refusal fires with the reason named
# and every passing case passes. Then it drives `plan`, the release job's
# decision, through a main branch's first-parent history.
#
# Run it with `make release-check-guard`. CI runs it in the release-check job.
set -euo pipefail
shopt -s inherit_errexit

cd "$(dirname "$0")/.."
export LC_ALL=C

if [ -z "${APIDIFF_VERSION:-}" ]; then
  echo "release-check-guard: APIDIFF_VERSION is not set: run this as \`make release-check-guard\`" >&2
  exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Install the pinned apidiff once, and hand it to every run of the check.
GOFLAGS='' GOWORK=off GOBIN="$tmp/bin" go install "golang.org/x/exp/cmd/apidiff@$APIDIFF_VERSION"
export APIDIFF=$tmp/bin/apidiff
# The cases set what they need. A pull request's base branch is one of those
# things, and CI sets it for the guard's own run.
unset GITHUB_BASE_REF

repo=$tmp/repo
g() { git -C "$repo" "$@"; }

git init -q -b main "$repo"
g config user.name release-check-guard
g config user.email release-check-guard@example.invalid
g config commit.gpgsign false
g config tag.gpgsign false
mkdir -p "$repo"/{scripts,ext,widget,internal/impl,cmd/tool,prompts/implemented}
cp scripts/release-check.sh "$repo/scripts/"

cat >"$repo/go.mod" <<'EOF'
module example.com/releaseguard

go 1.21
EOF
cat >"$repo/ext/ext.go" <<'EOF'
// Package ext stands in for Control's ext: its interfaces are implemented by
// another module.
package ext

// Sink is implemented outside this module, as Enterprise implements ext's.
type Sink interface {
	Export(batch []string) error
}
EOF
cat >"$repo/widget/widget.go" <<'EOF'
// Package widget is public, and no list names it: the check has to find it.
package widget

// Spin is part of the public API.
func Spin(n int) int { return n }
EOF
cat >"$repo/internal/impl/impl.go" <<'EOF'
// Package impl is internal: nothing outside this module can import it.
package impl

// Helper may change freely.
func Helper(n int) int { return n }
EOF
cat >"$repo/cmd/tool/main.go" <<'EOF'
// Command tool is package main: nothing can import it.
package main

// Exported may change freely.
func Exported(n int) int { return n }

func main() { _ = Exported(1) }
EOF
echo "# 0001 — first" >"$repo/prompts/implemented/0001-first.md"

released='## v0.1.0

- 0001.'

# changelog writes CHANGELOG.md: the header, ## Unreleased, then each argument
# as a section, in order.
changelog() {
  {
    printf '# Changelog\n\nWhat each release contains.\n\n## Unreleased\n'
    local s
    for s in "$@"; do printf '\n%s\n' "$s"; done
  } >"$repo/CHANGELOG.md"
}

changelog "$released"
g add -A
g commit -q -m "the released state"
g tag -a v0.1.0 -m v0.1.0
base=$(g rev-parse HEAD)

# reset puts the repository back to the released state: v0.1.0 its only tag.
reset() {
  g checkout -q -f main
  g reset -q --hard "$base"
  g clean -q -fdx
  local t
  for t in $(g tag --list); do g tag -d "$t" >/dev/null; done
  g tag -a v0.1.0 -m v0.1.0 "$base"
  g update-ref -d refs/remotes/origin/main 2>/dev/null || true
}

add_phase() { echo "# $1" >"$repo/prompts/implemented/$1.md"; }
# edit FILE OLD NEW replaces OLD with NEW in FILE (sed -i is not portable).
edit() {
  local text
  text=$(<"$repo/$1")
  printf '%s\n' "${text//"$2"/"$3"}" >"$repo/$1"
}
add_flush() {
  cat >"$repo/ext/ext.go" <<'EOF'
// Package ext stands in for Control's ext: its interfaces are implemented by
// another module.
package ext

// Sink is implemented outside this module, as Enterprise implements ext's.
type Sink interface {
	Export(batch []string) error
	Flush() error
}
EOF
}
add_function() {
  cat >>"$repo/ext/ext.go" <<'EOF'

// Version is new, and adding a function breaks no caller and no implementer.
func Version() string { return "v0.1.1" }
EOF
}

cases=0
failed=0
fail() {
  echo "release-check-guard: FAIL: $*" >&2
  failed=$((failed + 1))
}

# expect NAME refuse|pass WANT... runs the check on the repository as it stands
# and fails the case unless it refused (exit 1) or passed (exit 0) as named,
# with every WANT in its output.
expect() {
  local name=$1 want=$2 out status w
  shift 2
  cases=$((cases + 1))
  set +e
  out=$("$repo/scripts/release-check.sh" 2>&1)
  status=$?
  set -e
  case $want in
  refuse) [ $status -eq 1 ] || { fail "$name: the check exited $status, want 1 (refused)"; echo "$out" >&2; return; } ;;
  pass) [ $status -eq 0 ] || { fail "$name: the check exited $status, want 0 (passed)"; echo "$out" >&2; return; } ;;
  esac
  for w in "$@"; do
    if ! grep -F -- "$w" >/dev/null <<<"$out"; then
      fail "$name: the output does not say: $w"
      echo "$out" >&2
      return
    fi
  done
  echo "release-check-guard: ok — $name"
}

# --- The changelog parses, and agrees with the tags. ---------------------------

reset
changelog '## v0.2

- 0002.' "$released"
expect "a version heading that is not vMAJOR.MINOR.PATCH" refuse \
  '"## v0.2" is not a version heading'

reset
changelog "$released" "$released"
expect "a version heading twice" refuse 'v0.1.0 already has a section'

reset
changelog "$released" '## v0.1.1

- 0002.'
expect "headings out of order" refuse 'v0.1.0 is above v0.1.1: versions run newest first'

reset
printf '# Changelog\n\n%s\n' "$released" >"$repo/CHANGELOG.md"
expect "no ## Unreleased first" refuse 'the first section is "## Unreleased", not "## v0.1.0"'

reset
g tag v0.1.1
expect "a tag with no section" refuse 'tag v0.1.1 has no section in CHANGELOG.md'

reset
changelog '## v0.1.2

- 0003.' '## v0.1.1

- 0002.' "$released"
expect "more than one version without a tag" refuse \
  'more than one version has no tag (v0.1.2 v0.1.1), and only the newest may be pending'

# --- What the pending release may be called. -----------------------------------

reset
changelog '## v0.2.0

- 0002.' "$released"
g add -A && g commit -q -m "v0.2.0 is released" && g tag -a v0.2.0 -m v0.2.0
changelog '## v0.1.1

- a fix.' '## v0.2.0

- 0002.' "$released"
expect "a pending release not greater than the latest tag" refuse \
  'the pending release v0.1.1 is not greater than the latest release, v0.2.0'

reset
changelog '## v1.0.0

- stable.' "$released"
expect "a pending release whose MAJOR is above 0" refuse 'v1.0.0: MAJOR stays 0'

reset
g tag -d v0.1.0 >/dev/null
changelog '## v0.2.0

- 0001.'
expect "a first release that is not v0.1.0" refuse 'the first one is v0.1.0, not v0.2.0'

reset
changelog '## v0.1.1' "$released"
add_function
expect "a pending release with an empty section" refuse "v0.1.1's section is empty"

reset
add_phase 0002-second
changelog '## v0.3.0

- 0002.' "$released"
expect "a version that skips the next one" refuse \
  'v0.3.0 is not the next release after v0.1.0: as a MINOR release it is v0.2.0'

# --- A phase releases, and MINOR is for a phase or a break. --------------------

reset
add_phase 0002-second
expect "a prompt moved into implemented with no pending release" refuse \
  'prompts/implemented/ gained 0002 since v0.1.0' 'a phase releases in its own PR'

reset
add_phase 0002-second
changelog '## v0.1.1

- 0002.' "$released"
expect "a PATCH release that contains a phase" refuse \
  'v0.1.1 is a PATCH release, but it contains phase 0002' 'so it is v0.2.0'

reset
add_flush
changelog '## v0.1.1

- ext: Sink.Flush added.' "$released"
expect "a method added to an ext interface, released as PATCH" refuse \
  'v0.1.1 is a PATCH release, but it contains 1 incompatible change to the public API' \
  'incompatible  ext: Sink.Flush: added'

reset
add_flush
changelog '## v0.2.0

- ext: Sink.Flush added.' "$released"
expect "the same method released as MINOR with no ### Breaking" refuse \
  'v0.2.0 changes the public API incompatibly and has no ### Breaking' 'ext: Sink.Flush: added'

reset
add_flush
changelog '## v0.2.0

### Breaking

### Changed

- ext: Sink.Flush added.' "$released"
expect "a ### Breaking with nothing under it" refuse 'v0.2.0 has a ### Breaking heading with nothing listed under it'

reset
changelog '## v0.1.1

### Breaking

- ext: Sink.Export is now called once per batch, not once per record.' "$released"
expect "a PATCH release that declares a breaking change" refuse \
  'v0.1.1 is a PATCH release, but it contains a breaking change declared under ### Breaking'

reset
add_function
changelog '## v0.2.0

- ext: Version added.' "$released"
expect "a MINOR release that nothing in it needs" refuse \
  'v0.2.0 is a MINOR release, but it contains no phase and no incompatible change' 'so it is v0.1.1'

# --- The public packages are derived, not listed. ------------------------------

if grep -i widget scripts/release-check.sh >/dev/null; then
  fail "scripts/release-check.sh names the guard's widget package, so the next case proves nothing"
fi
reset
edit widget/widget.go 'func Spin(n int) int { return n }' 'func Spin(n, m int) int { return n + m }'
changelog '## v0.1.1

- widget: Spin takes two numbers.' "$released"
expect "a public package no list names is diffed" refuse \
  'public packages (derived: not under internal/, not package main): ext, widget' \
  'incompatible  widget: Spin: changed from func(int) int to func(int, int) int'

reset
g rm -q -r widget
changelog '## v0.1.1

- widget removed.' "$released"
expect "a public package removed" refuse 'incompatible  widget: package removed'

reset
edit internal/impl/impl.go 'func Helper(n int) int { return n }' 'func Helper() {}'
edit cmd/tool/main.go 'func Exported(n int) int { return n }' 'func Exported() int { return 0 }'
edit cmd/tool/main.go '_ = Exported(1)' '_ = Exported()'
expect "internal/ and package main are not public" pass \
  'public API since v0.1.0: no change' 'on merge this releases nothing'

# --- What passes. --------------------------------------------------------------

reset
expect "the released state releases nothing" pass 'on merge this releases nothing'

reset
add_function
changelog '## v0.1.1

- ext: Version added.' "$released"
expect "a new exported function in ext, released as PATCH, with no phase" pass \
  'compatible    ext: Version: added' \
  'on merge this releases v0.1.1 (PATCH: no phase; 1 compatible change to the public API)'

reset
add_flush
changelog '## v0.2.0

### Breaking

- ext: Sink.Flush added; an implementation must add it.' "$released"
expect "an incompatible change released as MINOR, under ### Breaking" pass \
  'on merge this releases v0.2.0 (MINOR: a breaking change, under ### Breaking; 1 incompatible change to the public API)'

reset
add_phase 0002-second
changelog '## v0.2.0

- 0002.' "$released"
expect "a phase released as MINOR" pass \
  'on merge this releases v0.2.0 (MINOR: 0002 implemented; no change to the public API)'

reset
g tag -d v0.1.0 >/dev/null
expect "the first release" pass 'no previous release: nothing to diff the public packages against' \
  'on merge this releases v0.1.0 (MINOR: 0001 implemented; no previous release to diff)'

# --- A pull request whose base already holds an untagged release. -------------

reset
add_function
changelog '## v0.1.1

- ext: Version added.' "$released"
g add -A && g commit -q -m "v0.1.1, merged and never tagged"
g update-ref refs/remotes/origin/main HEAD
cases=$((cases + 1))
set +e
out=$(GITHUB_BASE_REF=main "$repo/scripts/release-check.sh" 2>&1)
status=$?
set -e
if [ $status -ne 1 ] || ! grep -F 'v0.1.1 is already on main with no tag, so this change does not release it' >/dev/null <<<"$out"; then
  fail "a release left behind on the base branch: the check exited $status"
  echo "$out" >&2
else
  echo "release-check-guard: ok — a release left behind on the base branch"
fi

# --- The release job's decision, along main's first-parent history. ------------

# plan_is NAME "REV [BEFORE]" WANT checks what `plan` prints for a push of
# REV onto BEFORE, or with WANT "refuse: <text>", that it refuses and says
# <text>.
plan_is() {
  local name=$1 rev=$2 want=$3 out status
  cases=$((cases + 1))
  set +e
  # shellcheck disable=SC2086 # REV may carry a BEFORE: "<rev> <before>"
  out=$("$repo/scripts/release-check.sh" plan $rev 2>&1)
  status=$?
  set -e
  case $want in
  refuse:*)
    if [ $status -eq 0 ] || ! grep -F -- "${want#refuse: }" >/dev/null <<<"$out"; then
      fail "plan, $name: exited $status, want a refusal saying: ${want#refuse: }"
      echo "$out" >&2
      return
    fi
    ;;
  *)
    if [ $status -ne 0 ] || [ "$out" != "$want" ]; then
      fail "plan, $name: exited $status and printed \"$out\", want \"$want\""
      return
    fi
    ;;
  esac
  echo "release-check-guard: ok — plan, $name"
}

reset
# A pull request's branch adds v0.1.1, and main merges it: the merge commit is
# what main's push builds, so it is the one the job tags.
g checkout -q -b feature
add_function
changelog '## v0.1.1

- ext: Version added.' "$released"
g add -A && g commit -q -m "add Version"
g checkout -q main
g merge -q --no-ff -m "Merge pull request #2" feature
merge=$(g rev-parse HEAD)
echo later >"$repo/LATER"
g add -A && g commit -q -m "a later change that releases nothing"
later=$(g rev-parse HEAD)

# The walk follows main's first parents, so it finds the merge and not the
# branch commit beneath it: had it found the branch commit, this would refuse.
plan_is "the merge that introduced v0.1.1 tags it" "$merge" "release v0.1.1"
plan_is "a later push refuses, and names the merge to re-run" "$later" \
  "refuse: v0.1.1 was introduced by an earlier push, at $merge"
cases=$((cases + 1))
if [ "$(cd "$repo" && git checkout -q "$merge" && ./scripts/release-check.sh notes v0.1.1)" = "- ext: Version added." ]; then
  echo "release-check-guard: ok — notes print the release's section"
else
  fail "notes v0.1.1 did not print the section's body"
fi
g checkout -q main

g tag -a v0.1.1 -m v0.1.1 "$merge"
plan_is "a re-run on the tagged merge resumes it" "$merge" "resume v0.1.1"
plan_is "a later commit after the tag releases nothing" "$later" "nothing"

g tag -d v0.1.1 >/dev/null
g tag -a v0.1.1 -m v0.1.1 "$later"
plan_is "a tag on another commit is never moved" "$merge" "refuse: v0.1.1 is already tagged at $later"

reset
# A rebase merge pushes a pull request's commits onto main at once, and only
# the head gets a run: the push introduced v0.1.1 although the head's parent
# already has it. Asked about the push, the plan tags the head.
add_function
changelog '## v0.1.1

- ext: Version added.' "$released"
g add -A && g commit -q -m "add Version"
echo second >"$repo/SECOND"
g add -A && g commit -q -m "the pull request's second commit"
head=$(g rev-parse HEAD)
plan_is "a rebase merge's head, asked about the push, tags the head" "$head $base" "release v0.1.1"

if [ $failed -ne 0 ]; then
  echo "release-check-guard: $failed of $cases cases failed: the release check does not hold PLAN M23" >&2
  exit 1
fi
echo "release-check-guard: OK — $cases cases: every refusal fires with its reason, and what should pass passes"
