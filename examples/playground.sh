#!/usr/bin/env bash
# Playground example: builds a throwaway 24-commit repository with a
# planted regression, starts a halfstep session, and hands the keyboard to
# you. Judge each checkout with the printed one-liner and mark good/bad —
# the range bar shows the suspect list halving. Offline, self-contained.
set -euo pipefail

HALFSTEP="$(command -v halfstep || true)"
if [ -z "$HALFSTEP" ]; then
  echo "halfstep not on PATH — build it first: go build -o halfstep ./cmd/halfstep" >&2
  exit 1
fi

REPO="${1:-$(mktemp -d)}/playground"
export GIT_AUTHOR_NAME="Dev Example" GIT_AUTHOR_EMAIL="dev@example.test"
export GIT_COMMITTER_NAME="Dev Example" GIT_COMMITTER_EMAIL="dev@example.test"

git init -q -b main "$REPO"
cd "$REPO"

# 24 commits; the "regression" (a SLOW marker) lands at commit 17.
for i in $(seq 1 24); do
  if [ "$i" -ge 17 ]; then
    printf 'release %s\nlatency: SLOW\n' "$i" > perf.txt
  else
    printf 'release %s\nlatency: fast\n' "$i" > perf.txt
  fi
  git add perf.txt
  GIT_AUTHOR_DATE="2026-05-01T12:00:$(printf '%02d' "$i")Z" \
  GIT_COMMITTER_DATE="2026-05-01T12:00:$(printf '%02d' "$i")Z" \
  git commit -q -m "release $i"
done

"$HALFSTEP" start --bad HEAD --good HEAD~23

cat <<EOF

Playground ready in: $REPO

Judge the commit that is checked out (your "test suite"):

    cd $REPO
    grep latency perf.txt        # fast = good, SLOW = bad

Then tell halfstep and watch the bar close in:

    halfstep good     # or: halfstep bad
    halfstep status   # the bar, any time
    halfstep undo     # mis-marked? take it back
    halfstep log      # the shrink chart so far
    halfstep reset    # when you are done

Too impatient? Let the test drive:

    halfstep run -- sh -c '! grep -q SLOW perf.txt'
EOF
