# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- `start` wizard: prompts for the bad and good endpoints when not given
  as flags, validates that good is an ancestor of bad, refuses to trample
  a dirty working tree (override with `--force`), and checks out the
  first midpoint immediately.
- Verdict commands `good` / `bad` / `skip` (on the commit under test or
  any explicit revision), each printing the range bar, the shrink delta
  (`23 → 11 candidates`), and a `~N steps to go` halving estimate.
- `undo`: take back the most recent verdict by replaying the remaining
  marks from the initial endpoints — the escape hatch raw `git bisect`
  lacks.
- `run -- <command>`: fully automated hunts with the exact `git bisect
  run` exit-code contract (0 good, 125 skip, 1–127 bad, 128+ aborts
  without recording a verdict), one progress line per step.
- Range visualization: a bucket-compressed bar mapping surviving
  candidates onto the initial range (a lone survivor in a 1000-commit
  range never disappears), plus a `log` shrink chart with one scaled row
  per verdict.
- Bisection over git's own ranking (`rev-list --bisect-all`), so merges
  and skips behave exactly as git would pick them; contradiction
  detection rejects impossible marks without corrupting the session.
- Honest endgame reporting: a culprit box (author, date, subject, steps
  taken) with HEAD parked on the first bad commit, or an explicit
  `inconclusive` suspect list when only skipped commits remain.
- Session state as one JSON document under `.git/halfstep/` (documented
  in `docs/state-format.md`), atomic writes, `schema_version: 1`; never
  touches `refs/bisect`, so it coexists with raw `git bisect`.
- `status --format json` with a stable envelope for scripts and
  dashboards, `reset` that restores the original branch and is
  idempotent, ANSI color with `--color auto|always|never`, and a global
  `-C <dir>` like git's.
- Runnable examples (`examples/playground.sh`,
  `examples/autorun-ci.sh`) and an on-disk format reference.
- 90 deterministic offline tests (unit + in-process CLI integration
  against real repositories built with pinned fast-import streams) and
  `scripts/smoke.sh` covering a full manual and automated hunt end to
  end.

[0.1.0]: https://github.com/JaydenCJ/halfstep/releases/tag/v0.1.0
