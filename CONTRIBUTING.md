# Contributing to halfstep

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22, git ≥2.30, and a POSIX shell; nothing else.

```bash
git clone https://github.com/JaydenCJ/halfstep && cd halfstep
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, plants a bug in a real 30-commit
repository, hunts it both manually (start/mark/undo) and automatically
(`run`), and checks the JSON status, the shrink-chart log, and every
exit-code contract; it must finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (90 deterministic tests, no network, no sleeps).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (`render` and `autorun` never touch git or the filesystem;
   `engine` owns all session semantics; only `gitx` shells out).

## Ground rules

- Keep runtime dependencies at zero — halfstep is Go standard library
  plus the git binary the user already has. Adding a module needs strong
  justification in the PR.
- No network calls, ever, and no telemetry. halfstep only reads the
  repository and writes its own state file under `.git/halfstep/`.
- Never touch `refs/bisect` or any other git state a concurrent
  `git bisect` could own; halfstep must always be able to coexist.
- Determinism first: tests build repositories from pinned fast-import
  streams (fixed identities, fixed timestamps) so shas are reproducible;
  tests must not sleep or race the clock.
- The state file layout and the `status --format json` envelope are
  compatibility surfaces: changing either needs a `schema_version` bump
  and a migration note in `docs/state-format.md`.
- Code comments and doc comments are written in English.

## Reporting bugs

Include the output of `halfstep version`, `git --version`, the full
command you ran, and `halfstep status --format json` from the affected
repository. For wrong-culprit reports, the shape of the history matters:
`git log --graph --oneline good..bad` (or a synthetic repro built with
commits/merges) makes the difference between a guess and a fix.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
