#!/bin/sh
# Fail when a tracked or new Go file is not gofmt-formatted. Ignored paths, including linked worktrees, are skipped.
set -eu
unformatted=$(git ls-files --cached --others --exclude-standard -- "*.go" | xargs gofmt -l)
if [ -n "$unformatted" ]; then
    printf "These Go files are not formatted; run just fmt:\n%s\n" "$unformatted" >&2
    exit 1
fi
