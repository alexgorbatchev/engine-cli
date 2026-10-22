#!/usr/bin/env bash
# Exercises the publishing Git hooks against throwaway repositories with a local bare remote.
# Usage: bash scripts/test-git-hooks.sh <hooks-dir>, where <hooks-dir> is the directory core.hooksPath points to.
set -euo pipefail

hooks_dir="$(cd "${1:?usage: test-git-hooks.sh <hooks-dir>}" && pwd)"
mkdir -p .tmp
fixture="$(mktemp -d "$PWD/.tmp/git-hooks.XXXXXX")"
trap 'rm -rf "$fixture"' EXIT

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME="Hook Test" GIT_AUTHOR_EMAIL=hook@example.com
export GIT_COMMITTER_NAME="Hook Test" GIT_COMMITTER_EMAIL=hook@example.com
export SAVE_MARKER="$fixture/save-marker"
failures=0

expect_equal() {
    if [ "$2" = "$3" ]; then
        printf 'ok   %s\n' "$1"
    else
        printf 'FAIL %s: expected %s, got %s\n' "$1" "$3" "$2"
        failures=$((failures + 1))
    fi
}

commit() {
    git -C "$1" commit --quiet --allow-empty -m "$2"
}

# Creates a bare remote with one commit on main and clones it to $2 with the hooks installed.
clone_with_hooks() {
    local remote="$fixture/$1.git" seed="$fixture/$1-seed"
    git init --quiet --bare --initial-branch=main "$remote"
    git clone --quiet "$remote" "$seed" 2>/dev/null
    commit "$seed" "seed"
    git -C "$seed" push --quiet origin main
    git clone --quiet "$remote" "$2"
    git -C "$2" config core.hooksPath "$hooks_dir"
}

remote_main() {
    git -C "$fixture/$1.git" rev-parse main
}

clone_with_hooks direct "$fixture/direct"
commit "$fixture/direct" "feat: publish me"
expect_equal "commits on main push to origin/main" "$(remote_main direct)" "$(git -C "$fixture/direct" rev-parse HEAD)"

clone_with_hooks merge "$fixture/merge"
published="$(remote_main merge)"
git -C "$fixture/merge" worktree add --quiet -b feat/change "$fixture/merge-worktree" main
commit "$fixture/merge-worktree" "feat: worktree change"
expect_equal "worktree branch commits stay local" "$(remote_main merge)" "$published"
git -C "$fixture/merge" merge --quiet --ff-only feat/change
expect_equal "fast-forward merges into main push" "$(remote_main merge)" "$(git -C "$fixture/merge" rev-parse HEAD)"

clone_with_hooks local "$fixture/local"
published="$(remote_main local)"
git -C "$fixture/local" switch --quiet --create from-origin --track origin/main
commit "$fixture/local" "chore: branch tracking origin/main"
git -C "$fixture/local" switch --quiet --detach
commit "$fixture/local" "chore: detached"
expect_equal "detached commits and differently named upstreams stay local" "$(remote_main local)" "$published"

clone_with_hooks nested "$fixture/nested-remote-clone"
mkdir "$fixture/parent"
git -C "$fixture/parent" init --quiet --initial-branch=main
printf 'save:\n    git rev-parse --show-toplevel > "$SAVE_MARKER"\n' > "$fixture/parent/justfile"
git -C "$fixture/parent" -c protocol.file.allow=always submodule --quiet add "$fixture/nested.git" member
git -C "$fixture/parent/member" config core.hooksPath "$hooks_dir"
commit "$fixture/parent/member" "feat: publish me from a submodule"
expect_equal "submodule commits push" "$(remote_main nested)" "$(git -C "$fixture/parent/member" rev-parse HEAD)"
expect_equal "enclosing repositories are left alone" "$([ -e "$SAVE_MARKER" ] && echo touched || echo untouched)" untouched

[ "$failures" -eq 0 ] || exit 1
