# halfstep examples

Two runnable scripts, both offline and self-contained. Each builds its own
throwaway repository under `mktemp -d` with a deliberately planted
regression, and expects `halfstep` on `PATH` — build it with
`go build -o halfstep ./cmd/halfstep` first.

## playground.sh

A practice repository for the manual workflow. Creates 24 commits where a
"performance regression" lands somewhere in the middle, starts a hunt, and
leaves you inside the repo with the first midpoint checked out. You judge
each checkout with the one-liner the script prints (`grep` stands in for
your real test), mark `halfstep good` / `halfstep bad`, and watch the bar
close in. Mis-mark on purpose and try `halfstep undo`.

```bash
bash examples/playground.sh
```

## autorun-ci.sh

The unattended flow, end to end: plants a bug in a 40-commit history,
writes a `test.sh` with `git bisect run` exit-code semantics (0 good,
1 bad, 125 skip), and lets `halfstep run` drive the whole hunt. It then
verifies the culprit with `halfstep status --format json` — the same
check a nightly job would do — and resets.

```bash
bash examples/autorun-ci.sh
```

Both scripts pin commit identities and timestamps, so the shas and every
printed range bar are identical on every machine.
