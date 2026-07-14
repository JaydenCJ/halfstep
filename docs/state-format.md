# halfstep on-disk state format

halfstep persists exactly one file per repository:

```
<git dir>/halfstep/state.json
```

`<git dir>` is whatever `git rev-parse --absolute-git-dir` returns, so
worktrees and submodules each get their own session. The file lives inside
the git directory on purpose: it can never dirty the working tree, never be
committed by accident, and vanishes with the clone.

halfstep never writes `refs/bisect`, `BISECT_LOG`, or any other file raw
`git bisect` owns — the two tools can coexist in one repository.

## Writing

Writes are atomic: the document is serialized to a temp file in the same
directory and renamed over `state.json`. A crash mid-save leaves the
previous state intact. `halfstep reset` deletes the file (idempotently).

## Document layout (`schema_version: 1`)

```json
{
  "schema_version": 1,
  "original_ref": "main",
  "initial_bad": "0a0cc1c…40 hex…",
  "initial_goods": ["230428b…40 hex…"],
  "initial_count": 23,
  "bad": "2d2465b…40 hex…",
  "goods": ["230428b…", "452e794…"],
  "skipped": [],
  "current": "b29b86f…40 hex…",
  "steps": [
    {
      "commit": "682dcd8…40 hex…",
      "verdict": "bad",
      "before": 23,
      "after": 12,
      "at": "2026-07-13T09:15:04Z"
    }
  ]
}
```

| Field | Meaning |
|---|---|
| `schema_version` | layout version; halfstep refuses to load any other value |
| `original_ref` | branch name (or detached sha) to restore on `reset` |
| `initial_bad` / `initial_goods` | the endpoints fixed at `start`; the range bar is always drawn against this span |
| `initial_count` | candidates at start (denominator for all visuals) |
| `bad` / `goods` / `skipped` | the current frontier; recomputed marks, not history |
| `current` | commit checked out for testing; empty once the hunt is done or inconclusive |
| `steps` | append-only verdict history; `before`/`after` are candidate counts around that verdict |

Everything else — the next commit to test, the remaining count, done /
inconclusive — is **derived** from these marks via `git rev-list` on every
command, so the state can never disagree with the repository. `undo` works
by dropping the last step and replaying the rest from the initial
endpoints.

## Compatibility promise

The field set above and the `status --format json` envelope built from it
are compatibility surfaces. Fields may be added within schema version 1;
renaming or removing any of them bumps `schema_version`, and `halfstep`
will then ask the user to `reset` rather than misread an old session.
