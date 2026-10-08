#!/usr/bin/env bash
# The release check: hold every change to the release decision (PLAN M23).
#
# A release of this module is an annotated tag vMAJOR.MINOR.PATCH, cut by CI
# from the merged PR whose CHANGELOG.md names a version with no tag yet, and
# never moved once published: the module is public (M14), so a version's
# content is fixed the first time anyone fetches it. Hoplock Enterprise pins
# those versions (its E3) and implements ext's interfaces (M15), so what a
# version number promises has to be checked, not intended. This is the check.
# It reads three things and trusts nothing else:
#
#   - CHANGELOG.md, whose newest version heading, when it has no tag, is the
#     PENDING release: what merging this tree releases;
#   - the repository's v* tags, which are the releases;
#   - the tree: prompts/implemented/, and the public packages. Those are every
#     package in the module that is neither under an internal/ directory nor
#     package main, and they are DERIVED with `go list` rather than listed, so a
#     public package added later is held to the rule without anyone editing
#     this script.
#
# It refuses a changelog it cannot parse or that disagrees with the tags; a
# pending release that is not the next version the rule names; an incompatible
# change released without ### Breaking; and a phase that releases nothing.
# "Incompatible" is Go's API-compatibility rules read from the implementer's
# side as well as the caller's: a method added to an exported interface is a
# break, because Enterprise implements ext's interfaces. golang.org/x/exp's
# apidiff reports exactly that, so it is the diff tool, pinned by
# APIDIFF_VERSION in the Makefile.
#
# Usage:
#   release-check.sh [check]         the check; ends with one line for a reviewer
#   release-check.sh notes VERSION   VERSION's changelog section, without heading
#   release-check.sh plan [REV [BEFORE]]
#                                    what the release job does for a push of REV
#                                    (default HEAD) onto BEFORE (default REV's
#                                    first parent): "release vX.Y.Z", "resume
#                                    vX.Y.Z" or "nothing". It refuses to tag a
#                                    push that did not introduce the version
#
# Run it with `make release-check`. `make release-check-guard` proves it
# refuses what it must, against a throwaway repository.
set -euo pipefail
# A failure inside $(...) must fail the check: without this, bash ignores one,
# and an API diff that never ran would read as an API with no changes.
shopt -s inherit_errexit

cd "$(dirname "$0")/.."
export LC_ALL=C

changelog=CHANGELOG.md
semver_re='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'

say() { printf 'release-check: %s\n' "$*"; }

refusals=()
refuse() { refusals+=("$*"); }

# report_refusals prints every refusal and exits non-zero if there was one.
report_refusals() {
  [ ${#refusals[@]} -eq 0 ] && return 0
  local r
  for r in "${refusals[@]}"; do
    say "REFUSED: $r"
  done
  say "refused: nothing is released until every refusal above is fixed (PLAN M23)"
  exit 1
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# headings prints "<line>\t<text>" for every level-2 heading of the Markdown on
# stdin. Fenced code blocks are skipped, so an example in the changelog's
# header is not a section.
headings() {
  awk '
    { sub(/\r$/, "") }
    /^[ ]*(```|~~~)/ { fence = !fence; next }
    fence { next }
    /^## / { h = substr($0, 4); sub(/[ \t]+$/, "", h); print NR "\t" h }
  '
}

# section_body FILE VERSION prints VERSION's section: the lines between its
# heading and the next level-2 heading, without blank lines at either end.
section_body() {
  awk -v want="$2" '
    { sub(/\r$/, "") }
    /^[ ]*(```|~~~)/ { fence = !fence }
    !fence && /^## / {
      h = substr($0, 4); sub(/[ \t]+$/, "", h)
      if (in_section) { done = 1 }
      else if (h == want) { in_section = 1; next }
    }
    in_section && !done { lines[++n] = $0 }
    END {
      s = 1; while (s <= n && lines[s] ~ /^[ \t]*$/) s++
      e = n; while (e >= s && lines[e] ~ /^[ \t]*$/) e--
      for (i = s; i <= e; i++) print lines[i]
    }
  ' "$1"
}

# breaking_entries counts the list items under "### Breaking" in the section on
# stdin, and prints "absent" when the section has no such heading.
breaking_entries() {
  awk '
    /^[ ]*(```|~~~)/ { fence = !fence; next }
    fence { next }
    /^### / { h = substr($0, 5); sub(/[ \t]+$/, "", h); in_breaking = (h == "Breaking"); if (in_breaking) seen = 1; next }
    in_breaking && /^[ \t]*[-*+] / { n++ }
    END { if (seen) print n + 0; else print "absent" }
  '
}

# version_cmp A B prints 1, 0 or -1 as A is greater than, equal to, or less
# than B. Both must be vMAJOR.MINOR.PATCH.
version_cmp() {
  local a b i
  [[ $1 =~ $semver_re ]] && a=("${BASH_REMATCH[@]:1:3}")
  [[ $2 =~ $semver_re ]] && b=("${BASH_REMATCH[@]:1:3}")
  for i in 0 1 2; do
    if ((a[i] > b[i])); then echo 1; return; fi
    if ((a[i] < b[i])); then echo -1; return; fi
  done
  echo 0
}

# count N NOUN prints "1 change" or "2 changes".
count() { if [ "$1" -eq 1 ]; then echo "$1 $2"; else echo "$1 $2s"; fi; }

next_minor() { [[ $1 =~ $semver_re ]] && echo "v${BASH_REMATCH[1]}.$((BASH_REMATCH[2] + 1)).0"; }
next_patch() { [[ $1 =~ $semver_re ]] && echo "v${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.$((BASH_REMATCH[3] + 1))"; }

# heading_at REV VERSION succeeds when CHANGELOG.md at REV has VERSION's heading.
heading_at() {
  local text
  text=$(git show "$1:$changelog" 2>/dev/null) || return 1
  printf '%s\n' "$text" | headings | cut -f2 | grep -x -F -- "$2" >/dev/null
}

# introduced_by VERSION REV prints the commit on REV's first-parent history —
# main's merges — that added VERSION's heading: the one the release job tags.
introduced_by() {
  local c
  for c in $(git rev-list --first-parent "$2"); do
    if ! git rev-parse -q --verify "$c^1" >/dev/null || ! heading_at "$c^1" "$1"; then
      printf '%s\n' "$c"
      return 0
    fi
  done
  return 1
}

# phase_words turns sorted four-digit phase numbers on stdin into "0015",
# "0015, 0017" or "0001–0014".
phase_words() {
  awk '
    function span(a, b) { return a == b ? sprintf("%04d", a) : sprintf("%04d–%04d", a, b) }
    { n = $1 + 0 }
    NR > 1 && n == last + 1 { last = n; next }
    NR > 1 { out = out sep span(first, last); sep = ", " }
    { first = n; last = n }
    END { if (NR) print out sep span(first, last) }
  '
}

# implemented_phases [REV] prints the phase numbers in prompts/implemented/, in
# the tree or at REV.
implemented_phases() {
  if [ $# -eq 0 ]; then
    local f
    for f in prompts/implemented/[0-9][0-9][0-9][0-9]-*.md; do
      if [ -e "$f" ]; then basename "$f"; fi
    done
  else
    git ls-tree --name-only "$1" -- prompts/implemented/ | sed 's#^prompts/implemented/##'
  fi | sed -n 's/^\([0-9][0-9][0-9][0-9]\)-.*\.md$/\1/p' | sort -u
}

# public_packages DIR prints the import path of every public package of the
# module rooted at DIR: neither under an internal/ directory, nor package main,
# nor a directory with no non-test Go files (which nothing can import).
public_packages() {
  local listed
  listed=$(cd "$1" && GOWORK=off go list \
    -f '{{if and (or .GoFiles .CgoFiles) (ne .Name "main")}}{{.ImportPath}}{{end}}' ./...) || return 1
  printf '%s\n' "$listed" | sed -E -e '\#(^|/)internal(/|$)#d' -e '/^$/d' | sort
}

apidiff_bin=""
ensure_apidiff() {
  [ -n "$apidiff_bin" ] && return 0
  if [ -n "${APIDIFF:-}" ]; then
    apidiff_bin=$APIDIFF
    return 0
  fi
  if [ -z "${APIDIFF_VERSION:-}" ]; then
    say "APIDIFF_VERSION is not set: run this as \`make release-check\`, which pins the diff tool" >&2
    exit 1
  fi
  if ! GOFLAGS='' GOWORK=off GOBIN="$work/bin" go install "golang.org/x/exp/cmd/apidiff@$APIDIFF_VERSION"; then
    say "could not install golang.org/x/exp/cmd/apidiff@$APIDIFF_VERSION" >&2
    exit 1
  fi
  apidiff_bin=$work/bin/apidiff
}

# api_diff TAG prints one "<incompatible|compatible>\t<package>: <change>" line
# per change to the public packages between TAG and the tree. A public package
# that is gone is incompatible; one that is new is compatible.
api_diff() {
  local tag=$1 module old_pkgs new_pkgs pkg rel
  mkdir -p "$work/old"
  git archive --format=tar "$tag" | tar -x -C "$work/old"
  module=$(GOWORK=off go list -m)
  old_pkgs=$(public_packages "$work/old")
  new_pkgs=$(public_packages .)
  ensure_apidiff
  for pkg in $(printf '%s\n%s\n' "$old_pkgs" "$new_pkgs" | sed '/^$/d' | sort -u); do
    rel=${pkg#"$module"}
    rel=${rel#/}
    rel=${rel:-.}
    if ! grep -x -F -- "$pkg" >/dev/null <<<"$new_pkgs"; then
      printf 'incompatible\t%s: package removed\n' "$rel"
      continue
    fi
    if ! grep -x -F -- "$pkg" >/dev/null <<<"$old_pkgs"; then
      printf 'compatible\t%s: package added\n' "$rel"
      continue
    fi
    (cd "$work/old" && GOWORK=off "$apidiff_bin" -w "$work/old.export" "$pkg")
    GOWORK=off "$apidiff_bin" -w "$work/new.export" "$pkg"
    "$apidiff_bin" "$work/old.export" "$work/new.export" | awk -v rel="$rel" '
      /^Incompatible changes:/ { kind = "incompatible"; next }
      /^Compatible changes:/ { kind = "compatible"; next }
      /^- / && kind != "" { print kind "\t" rel ": " substr($0, 3) }
    '
  done
}

check() {
  if [ "$(git rev-parse --is-shallow-repository)" = true ]; then
    say "warning: this clone is shallow, so tags and history may be missing; \`git fetch --unshallow --tags\` first"
  fi
  if [ ! -f "$changelog" ]; then
    refuse "there is no $changelog at the module root"
    report_refusals
  fi

  # --- The changelog parses: ## Unreleased, then one ## vX.Y.Z per release.
  local entry line h first=1
  local -a versions=()
  local -A section_line=()
  while IFS= read -r entry; do
    line=${entry%%$'\t'*}
    h=${entry#*$'\t'}
    if [ $first -eq 1 ]; then
      first=0
      if [ "$h" = Unreleased ]; then
        continue
      fi
      refuse "$changelog:$line: the first section is \"## Unreleased\", not \"## $h\""
    fi
    if [[ ! $h =~ $semver_re ]]; then
      refuse "$changelog:$line: \"## $h\" is not a version heading: a release is ## vMAJOR.MINOR.PATCH"
      continue
    fi
    if [ -n "${section_line[$h]:-}" ]; then
      refuse "$changelog:$line: $h already has a section, at line ${section_line[$h]}"
      continue
    fi
    section_line[$h]=$line
    versions+=("$h")
  done < <(headings <"$changelog")
  if [ $first -eq 1 ]; then
    refuse "$changelog has no sections: it opens with \"## Unreleased\", then one \"## vX.Y.Z\" per release"
  fi
  report_refusals

  local i
  for ((i = 1; i < ${#versions[@]}; i++)); do
    if [ "$(version_cmp "${versions[i - 1]}" "${versions[i]}")" != 1 ]; then
      refuse "$changelog:${section_line[${versions[i]}]}: ${versions[i - 1]} is above ${versions[i]}: versions run newest first"
    fi
  done

  # --- The tags agree with it: every tag has a section, and at most the newest
  # version has no tag.
  local tag latest=""
  local -A tagged=()
  while IFS= read -r tag; do
    [ -n "$tag" ] || continue
    tagged[$tag]=1
    if [ -z "${section_line[$tag]:-}" ]; then
      refuse "tag $tag has no section in $changelog: every release is listed there"
    fi
    if [[ $tag =~ $semver_re ]] && { [ -z "$latest" ] || [ "$(version_cmp "$tag" "$latest")" = 1 ]; }; then
      latest=$tag
    fi
  done < <(git tag --list 'v*')

  local v pending=""
  local -a untagged=()
  for v in "${versions[@]}"; do
    [ -n "${tagged[$v]:-}" ] || untagged+=("$v")
  done
  if [ ${#versions[@]} -gt 0 ] && [ -z "${tagged[${versions[0]}]:-}" ]; then
    pending=${versions[0]}
  fi
  # A version with a section and no tag that is not the pending one is a release
  # left behind: its job failed or never ran, and only a re-run on the commit
  # that added it can cut it. Name that commit.
  local left="" by
  for v in "${untagged[@]}"; do
    [ "$v" = "$pending" ] && continue
    by=$(introduced_by "$v" HEAD 2>/dev/null || true)
    left+=" $v was added by ${by:-a commit this clone does not have};"
  done
  if [ -n "$left" ]; then
    if [ ${#untagged[@]} -gt 1 ]; then
      refuse "more than one version has no tag (${untagged[*]}), and only the newest may be pending:$left re-run the release job on that commit, or fetch this clone's tags (\`git fetch --tags\`)"
    else
      refuse "${untagged[0]} has a section but no tag, and it is not the newest version:$left re-run the release job on that commit, or fetch this clone's tags (\`git fetch --tags\`)"
    fi
  fi

  # --- What the pending release may be called.
  local body=""
  if [ -n "$pending" ]; then
    if [[ $pending =~ $semver_re ]] && [ "${BASH_REMATCH[1]}" != 0 ]; then
      refuse "$pending: MAJOR stays 0 until a decision of its own declares the public packages stable; every major after v1 changes the module path"
    fi
    if [ -z "$latest" ]; then
      [ "$pending" = v0.1.0 ] || refuse "there is no release yet, so the first one is v0.1.0, not $pending"
    elif [ "$(version_cmp "$pending" "$latest")" != 1 ]; then
      refuse "the pending release $pending is not greater than the latest release, $latest"
    fi
    body=$(section_body "$changelog" "$pending")
    [ -n "$body" ] || refuse "$pending's section is empty: a release says what it contains"
    if [ -n "${GITHUB_BASE_REF:-}" ] && heading_at "refs/remotes/origin/$GITHUB_BASE_REF" "$pending"; then
      by=$(introduced_by "$pending" "refs/remotes/origin/$GITHUB_BASE_REF" 2>/dev/null || true)
      refuse "$pending is already on $GITHUB_BASE_REF with no tag, so this change does not release it: the release job on ${by:-the commit that added it} did not tag it. Re-run that job on that commit first; merging this first puts a red release job on $GITHUB_BASE_REF"
    fi
  fi
  report_refusals

  say "latest release: ${latest:-none}"

  # --- What changed since the latest release: phases, and the public API.
  local gained gained_words="" phases_noun=phase
  if [ -n "$latest" ]; then
    gained=$(comm -13 <(implemented_phases "$latest") <(implemented_phases))
  else
    gained=$(implemented_phases)
  fi
  if [ -n "$gained" ]; then
    gained_words=$(phase_words <<<"$gained")
    [ "$(wc -l <<<"$gained")" -eq 1 ] || phases_noun=phases
    say "phases implemented since ${latest:-the start}: $gained_words"
  else
    say "phases implemented since ${latest:-the start}: none"
  fi

  local pkgs module
  module=$(GOWORK=off go list -m)
  pkgs=$(public_packages .)
  say "public packages (derived: not under internal/, not package main): $(sed -e "s#^$module/##" -e "s#^$module\$#.#" <<<"$pkgs" | paste -sd, - | sed 's/,/, /g')"

  local diff="" n_incompatible=0 n_compatible=0 api_words
  if [ -n "$latest" ]; then
    diff=$(api_diff "$latest")
    n_incompatible=$(grep -c '^incompatible' <<<"$diff" || true)
    n_compatible=$(grep -c '^compatible' <<<"$diff" || true)
    if [ -z "$diff" ]; then
      say "public API since $latest: no change"
      api_words="no change to the public API"
    else
      say "public API since $latest:"
      awk -F'\t' '{ printf "release-check:   %-13s %s\n", $1, $2 }' <<<"$diff"
      if [ "$n_incompatible" -gt 0 ]; then
        api_words="$(count "$n_incompatible" "incompatible change") to the public API"
      else
        api_words="$(count "$n_compatible" "compatible change") to the public API"
      fi
    fi
  else
    say "no previous release: nothing to diff the public packages against"
    api_words="no previous release to diff"
  fi

  # --- Whether the number says what the release is.
  local breaking=absent kind="" expected
  if [ -n "$pending" ]; then
    breaking=$(breaking_entries <<<"$body")
    [ "$breaking" = 0 ] && refuse "$pending has a ### Breaking heading with nothing listed under it"
  fi
  if [ -z "$pending" ]; then
    if [ -n "$gained" ]; then
      refuse "prompts/implemented/ gained $gained_words since ${latest:-the start}, but $changelog names no pending release: a phase releases in its own PR. Add \"## $(if [ -n "$latest" ]; then next_minor "$latest"; else echo v0.1.0; fi)\" with this phase's entry"
    fi
    if [ "$n_incompatible" -gt 0 ]; then
      say "note: the public packages changed incompatibly since $latest, so the release that ships this is MINOR and lists each change under ### Breaking"
    fi
  elif [ -z "$latest" ]; then
    kind=MINOR
  else
    local -a needs=()
    [ -n "$gained" ] && needs+=("$phases_noun $gained_words")
    [ "$n_incompatible" -gt 0 ] && needs+=("$(count "$n_incompatible" "incompatible change") to the public API")
    [ "$n_incompatible" -eq 0 ] && [ "$breaking" != absent ] && needs+=("a breaking change declared under ### Breaking")
    local why
    why=$(printf '%s\n' "${needs[@]}" | paste -sd';' - | sed 's/;/; /g')
    if [ ${#needs[@]} -gt 0 ]; then
      kind=MINOR
      expected=$(next_minor "$latest")
    else
      kind=PATCH
      expected=$(next_patch "$latest")
    fi
    if [ "$pending" != "$expected" ]; then
      if [ $kind = MINOR ] && [ "$pending" = "$(next_patch "$latest")" ]; then
        refuse "$pending is a PATCH release, but it contains $why: MINOR is for a phase or an incompatible change, so it is $expected"
      elif [ $kind = PATCH ] && [ "$pending" = "$(next_minor "$latest")" ]; then
        refuse "$pending is a MINOR release, but it contains no phase and no incompatible change: PATCH is for anything else, so it is $expected"
      else
        refuse "$pending is not the next release after $latest: as a $kind release it is $expected"
      fi
    fi
    if [ "$n_incompatible" -gt 0 ] && [ "$breaking" = absent ]; then
      refuse "$pending changes the public API incompatibly and has no ### Breaking: list each of these under ### Breaking in its section — $(grep '^incompatible' <<<"$diff" | cut -f2 | paste -sd';' - | sed 's/;/; /g')"
    fi
  fi
  report_refusals

  if [ -z "$pending" ]; then
    echo "on merge this releases nothing"
    return 0
  fi

  local reason
  if [ -n "$gained" ]; then
    reason="$gained_words implemented"
  elif [ $kind = MINOR ]; then
    reason="a breaking change, under ### Breaking"
  else
    reason="no phase"
  fi
  say "on merge, the release job tags the merge commit $pending, annotated with this section, and publishes it as a GitHub Release:"
  printf '    ## %s\n    \n' "$pending"
  while IFS= read -r line; do printf '    %s\n' "$line"; done <<<"$body"
  say "none of that can be proven before merge: the tag is cut only by the release job, on the merge commit, once every other job has passed there"
  echo "on merge this releases $pending ($kind: $reason; $api_words)"
}

notes() {
  local version=${1:-}
  if [[ ! $version =~ $semver_re ]]; then
    say "usage: release-check.sh notes vMAJOR.MINOR.PATCH" >&2
    exit 2
  fi
  local body
  body=$(section_body "$changelog" "$version")
  if [ -z "$body" ]; then
    say "$changelog has no section for $version, or it is empty" >&2
    exit 1
  fi
  printf '%s\n' "$body"
}

# plan decides what the release job does for a push of REV onto BEFORE, the
# branch head before the push. The push introduced the newest version when its
# heading is absent at BEFORE. Asking about the push rather than about REV's
# parent is what keeps a rebase merge working: its heading may arrive in a
# commit that never gets a run of its own.
plan() {
  local sha prior newest="" text by tagged h
  sha=$(git rev-parse --verify "${1:-HEAD}^{commit}")
  prior=${2:-$sha^1}
  text=$(git show "$sha:$changelog" 2>/dev/null || true)
  # Matched here, not in awk: awk -v processes escapes in what it is given, so
  # the pattern's \. would arrive as "any character", with a warning from gawk.
  while IFS= read -r h; do
    if [ -z "$newest" ] && [[ $h =~ $semver_re ]]; then newest=$h; fi
  done < <(printf '%s\n' "$text" | headings | cut -f2)
  if [ -z "$newest" ]; then
    echo nothing
    return 0
  fi
  tagged=$(git rev-parse -q --verify "refs/tags/$newest^{commit}" || true)
  if git rev-parse -q --verify "$prior^{commit}" >/dev/null && heading_at "$prior" "$newest"; then
    if [ -n "$tagged" ]; then
      echo nothing
      return 0
    fi
    by=$(introduced_by "$newest" "$sha" || true)
    say "REFUSED: $newest was introduced by an earlier push, at ${by:-a commit before this one}, and has no tag: the release job there failed or never ran. Re-run it on that commit. Tagging $sha instead would publish content the release's section does not describe (PLAN M23)" >&2
    exit 1
  fi
  if [ -z "$tagged" ]; then
    echo "release $newest"
  elif [ "$tagged" = "$sha" ]; then
    echo "resume $newest"
  else
    say "REFUSED: $newest is already tagged at $tagged, not at $sha, the commit that introduced it. A published version is never moved: supersede it with a new release and retract it in go.mod (PLAN M23)" >&2
    exit 1
  fi
}

case "${1:-check}" in
check) check ;;
notes) notes "${2:-}" ;;
plan) plan "${2:-HEAD}" "${3:-}" ;;
*)
  say "usage: release-check.sh [check | notes VERSION | plan [REV [BEFORE]]]" >&2
  exit 2
  ;;
esac
