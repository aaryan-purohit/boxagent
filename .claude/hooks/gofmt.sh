#!/bin/sh
# PostToolUse hook: format a Go file after Claude edits or writes it.
file=$(jq -r '.tool_input.file_path // empty')
case "$file" in
  *.go) ;;
  *) exit 0 ;;
esac
[ -f "$file" ] || exit 0

if command -v goimports >/dev/null 2>&1; then
  goimports -w "$file" 1>&2
else
  gofmt -w "$file" 1>&2
fi
