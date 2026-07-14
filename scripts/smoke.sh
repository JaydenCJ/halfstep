#!/usr/bin/env bash
# End-to-end smoke test for halfstep: builds the binary, plants a bug in a
# real 30-commit git repository, hunts it manually (start/mark/undo) and
# automatically (run), checks the JSON status and the shrink-chart log,
# then resets. No network, idempotent, finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/halfstep"
export GIT_AUTHOR_NAME="Dev Example" GIT_AUTHOR_EMAIL="dev@example.test"
export GIT_COMMITTER_NAME="Dev Example" GIT_COMMITTER_EMAIL="dev@example.test"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/halfstep) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" version | grep -qx "halfstep 0.1.0" || fail "version mismatch"

echo "3. plant a bug in a 30-commit repository (breaks at commit 23)"
REPO="$WORKDIR/repo"
git init -q -b main "$REPO"
cd "$REPO"
for i in $(seq 1 30); do
  if [ "$i" -ge 23 ]; then printf 'v%s BUG\n' "$i" > lib.txt; else printf 'v%s\n' "$i" > lib.txt; fi
  git add lib.txt
  GIT_AUTHOR_DATE="2026-03-01T00:00:$(printf '%02d' "$i")Z" \
  GIT_COMMITTER_DATE="2026-03-01T00:00:$(printf '%02d' "$i")Z" \
  git commit -q -m "change $i"
done
CULPRIT="$(git rev-parse HEAD~7)"   # commit 23 of 30
CULPRIT7="$(git rev-parse --short=7 HEAD~7)"

echo "4. start shows the range bar and the first midpoint"
"$BIN" start --color never --bad HEAD --good HEAD~29 > start.out || fail "start failed"
grep -q "29 candidates · ~5 steps to go" start.out || fail "candidate count/estimate missing"
grep -q "\[█" start.out || fail "range bar missing"
grep -q "(step 1)" start.out || fail "first checkout missing"

echo "5. a manual verdict shrinks the range"
if grep -q BUG lib.txt; then V=bad; else V=good; fi
"$BIN" "$V" --color never > mark.out || fail "mark failed"
grep -q "29 → 1[45] candidates" mark.out || fail "shrink delta wrong: $(cat mark.out)"

echo "6. undo restores the full range"
"$BIN" undo --color never > undo.out || fail "undo failed"
grep -q "back to 29 candidates" undo.out || fail "undo did not restore"

echo "7. auto-run hunts the planted commit down exactly"
"$BIN" run --color never -- sh -c '! grep -q BUG lib.txt' > run.out || fail "run failed"
grep -q "first bad commit: $CULPRIT7" run.out || fail "wrong culprit: $(cat run.out)"
grep -q "narrowed to 1" run.out || fail "final summary missing"
[ "$(git rev-parse HEAD)" = "$CULPRIT" ] || fail "HEAD should rest on the culprit"

echo "8. log renders the shrink chart"
"$BIN" log --color never > log.out
grep -q "step  verdict  commit   candidates" log.out || fail "log header missing"
grep -q "0  start" log.out || fail "baseline row missing"
grep -q "first bad commit: $CULPRIT7" log.out || fail "culprit missing from log"

echo "9. status --format json is machine-readable and correct"
"$BIN" status --format json > status.json
grep -q '"tool": "halfstep"' status.json || fail "json envelope missing"
grep -q '"schema_version": 1' status.json || fail "schema version missing"
grep -q '"done": true' status.json || fail "done flag missing"
grep -q "\"culprit\": \"$CULPRIT\"" status.json || fail "culprit missing from json"

echo "10. reset returns to the branch and is idempotent"
"$BIN" reset > reset1.out || fail "reset failed"
grep -q "back on main" reset1.out || fail "reset did not restore main"
[ "$(git symbolic-ref --short HEAD)" = "main" ] || fail "not back on main"
"$BIN" reset > reset2.out || fail "second reset failed"
grep -q "nothing to reset" reset2.out || fail "second reset should be a no-op"

echo "11. the wizard prompts for missing endpoints"
printf '\nHEAD~29\n' | "$BIN" start --color never > wizard.out || fail "wizard start failed"
grep -q "Bad commit — one where the problem happens \[HEAD\]:" wizard.out || fail "bad prompt missing"
grep -q "29 candidates" wizard.out || fail "wizard did not start the hunt"
"$BIN" reset > /dev/null

echo "12. a script that cannot test anything ends inconclusive, exit 1"
"$BIN" start --color never --bad HEAD~26 --good HEAD~29 > /dev/null
set +e
"$BIN" run --color never -- sh -c 'exit 125' > skip.out
rc=$?
set -e
[ "$rc" -eq 1 ] || fail "all-skip run should exit 1, got $rc"
grep -q "inconclusive:" skip.out || fail "suspects block missing"
"$BIN" reset > /dev/null

echo "13. usage and session errors use distinct exit codes"
set +e
"$BIN" run true > /dev/null 2>&1
[ $? -eq 2 ] || fail "missing '--' should exit 2"
"$BIN" nonsense > /dev/null 2>&1
[ $? -eq 2 ] || fail "unknown command should exit 2"
"$BIN" status > /dev/null 2>&1
[ $? -eq 1 ] || fail "status without a session should exit 1"
set -e

echo "SMOKE OK"
