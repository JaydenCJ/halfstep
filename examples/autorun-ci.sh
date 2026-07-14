#!/usr/bin/env bash
# Auto-run example: the unattended hunt a nightly job would do. Plants a
# bug in a 40-commit history, writes a test script with `git bisect run`
# exit-code semantics, lets `halfstep run` find the culprit, then verifies
# it via the JSON status. Offline and self-contained.
set -euo pipefail

HALFSTEP="$(command -v halfstep || true)"
if [ -z "$HALFSTEP" ]; then
  echo "halfstep not on PATH — build it first: go build -o halfstep ./cmd/halfstep" >&2
  exit 1
fi

REPO="${1:-$(mktemp -d)}/autorun"
export GIT_AUTHOR_NAME="Dev Example" GIT_AUTHOR_EMAIL="dev@example.test"
export GIT_COMMITTER_NAME="Dev Example" GIT_COMMITTER_EMAIL="dev@example.test"

git init -q -b main "$REPO"
cd "$REPO"

# 40 commits; the bug lands at commit 29.
for i in $(seq 1 40); do
  if [ "$i" -ge 29 ]; then printf 'build %s\nstatus: BROKEN\n' "$i" > app.txt
  else printf 'build %s\nstatus: ok\n' "$i" > app.txt; fi
  git add app.txt
  GIT_AUTHOR_DATE="2026-05-02T08:00:$(printf '%02d' "$((i % 60))")Z" \
  GIT_COMMITTER_DATE="2026-05-02T08:00:$(printf '%02d' "$((i % 60))")Z" \
  git commit -q -m "build $i"
done
EXPECTED="$(git rev-parse HEAD~11)"   # commit 29 of 40

# The same script you would hand to `git bisect run`: exit 0 = good,
# 1 = bad; exit 125 for commits that cannot be tested at all.
cat > test.sh <<'EOF'
#!/bin/sh
! grep -q BROKEN app.txt
EOF
chmod +x test.sh

"$HALFSTEP" start --bad HEAD --good HEAD~39
"$HALFSTEP" run -- ./test.sh

# What a CI job would assert: the culprit, machine-readably.
FOUND="$("$HALFSTEP" status --format json | grep '"culprit"' | cut -d'"' -f4)"
echo
echo "expected culprit: $EXPECTED"
echo "found culprit:    $FOUND"
[ "$FOUND" = "$EXPECTED" ] || { echo "MISMATCH" >&2; exit 1; }
echo "culprit verified — resetting"

"$HALFSTEP" reset
