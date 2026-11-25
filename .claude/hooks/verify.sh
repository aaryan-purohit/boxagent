#!/bin/sh
# Stop hook: when Go files changed, block finishing until vet and unit tests pass.
# Exit 2 sends stderr back to Claude so it can fix the failure.
input=$(cat)

# One retry only, so a failure Claude can't fix doesn't loop forever.
[ "$(printf '%s' "$input" | jq -r '.stop_hook_active')" = "true" ] && exit 0

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
[ -f go.mod ] || exit 0
git status --porcelain -- '*.go' go.mod go.sum | grep -q . || exit 0

if ! out=$( { go vet ./... && go test -race ./...; } 2>&1 ); then
  printf 'go vet / go test failed. Fix before finishing:\n%s\n' "$out" | tail -n 80 1>&2
  exit 2
fi
