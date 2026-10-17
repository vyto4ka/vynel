#!/usr/bin/env bash
# Prints the short hash of the last commit that changed what goes into a release (the binaries,
# the web UI, install.sh). Docs, tests and CI-only files do not count: a README fix does not make
# panels offer an update (docs/DEVELOPMENT.md "Releases"). Needs the full history (fetch-depth 0).
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
  ':(exclude).claude'
)

v="$(git log -1 --format=%h --abbrev=7 -- . "${not_code[@]}")"
if [ -z "$v" ]; then
  v="$(git rev-parse --short=7 HEAD)"
fi
echo "$v"
