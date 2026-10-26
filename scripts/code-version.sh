#!/usr/bin/env bash
# Prints the short hash of the last commit that changed what goes into a release (the binaries,
# the web UI, install.sh). Docs, tests and CI-only files do not count: a README fix does not make
# panels offer an update (docs/DEVELOPMENT.md "Releases"). Needs the full history (fetch-depth 0).
#   scripts/code-version.sh        3f2a1bc
#   scripts/code-version.sh --dev  v0.1.1-dev.3f2a1bc: the version of a dev build, after the last
#                                  release (v0.1.0) and before the next one; needs the tags too
set -euo pipefail

# Keep in step with paths-ignore in .github/workflows/edge.yml.
not_code=(
  ':(exclude)*.md'
  ':(exclude)docs'
  ':(exclude)LICENSE'
  ':(exclude)*_test.go'
  ':(exclude)internal/e2e'
  ':(exclude)*/testdata'
  ':(exclude).github/workflows/ci.yml'
  ':(exclude).github/workflows/release.yml'
  ':(exclude).github/dependabot.yml'
  ':(exclude).claude'
  ':(exclude)scripts/code-version.sh'
  ':(exclude)scripts/release-key.sh'
  ':(exclude)scripts/load-memory.sh'
  ':(exclude)scripts/test-install.sh'
)

v="$(git log -1 --format=%h --abbrev=7 -- . "${not_code[@]}")"
if [ -z "$v" ]; then
  v="$(git rev-parse --short=7 HEAD)"
fi
if [ "${1:-}" != --dev ]; then
  echo "$v"
  exit 0
fi

# The next patch after the last release (v0.1.0 -> v0.1.1); after a pre-release (v0.2.0-rc1) the
# version it leads to (v0.2.0).
last="$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || echo v0.0.0)"
if [[ "$last" == *-* ]]; then
  next="${last%%-*}"
else
  IFS=. read -r major minor patch <<<"${last#v}"
  next="v$major.$minor.$((patch + 1))"
fi
echo "$next-dev.$v"
