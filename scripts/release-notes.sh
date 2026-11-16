#!/usr/bin/env bash
# Prints markdown release notes for a tag, built from conventional commit
# subjects since the previous tag.
#
# Usage: scripts/release-notes.sh <tag>   (e.g. v0.2.0, defaults to HEAD)
# NOTES_NO_INSTALL=1 leaves out the install section (used by changelog.sh).
set -euo pipefail

tag="${1:-HEAD}"
prev="$(git describe --tags --abbrev=0 "$tag^" 2>/dev/null || true)"
range="${prev:+$prev..}$tag"

# CHANGELOG.md updates made by the release workflow are not release content.
subjects="$(git log --no-merges --format='%s (%h)' "$range" | grep -v '^chore(release):' || true)"

# section <title> <regex> [-v]: lists commits matching (or with -v, not matching) the regex.
section() {
  local lines
  lines="$(grep -E ${3:-} "$2" <<<"$subjects" | sed -E 's/^[a-z]+(\([^)]*\))?!?: *//; s/^/- /' || true)"
  [[ -n "$lines" ]] && printf '### %s\n\n%s\n\n' "$1" "$lines"
  return 0
}

section "Breaking changes" '^[a-z]+(\([^)]*\))?!:'
section "Features" '^feat(\([^)]*\))?:'
section "Fixes" '^fix(\([^)]*\))?:'
section "Other changes" '^(chore|refactor|docs|perf|test|build|ci)(\([^)]*\))?:'
section "Uncategorized" '^(feat|fix|chore|refactor|docs|perf|test|build|ci)(\([^)]*\))?!?:' -v

[[ -n "${NOTES_NO_INSTALL:-}" ]] && exit 0

cat <<'NOTES'
### Install

- Arch: `sudo pacman -U smon-*.pkg.tar.zst`

Then log out and back in (or run `sudo udevadm trigger`) so the uinput rule lets smon create input devices.
NOTES

[[ -n "$prev" ]] && printf '\n**Full changelog:** `%s...%s`\n' "$prev" "$tag"
exit 0
