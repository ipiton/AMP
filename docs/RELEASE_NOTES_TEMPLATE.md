# Release Notes Template

> How to use this template: see the "Release Process" section in `WORKFLOW.md`.
> Source of truth is `CHANGELOG.md`'s `[Unreleased]` block — copy from there, don't
> re-derive from git log or memory. Features may be condensed; **breaking changes
> must be carried verbatim**, including the epic/wave code that introduced them
> (`FU-*`, `AMP-PARITY-WAVE*`) so a reader can trace back to the full migration note.
> See `docs/RELEASE_NOTES_v0.1.0-draft.md` for a filled-in example.

---

# AMP vX.Y.Z Release Notes

**Release date:** YYYY-MM-DD
**Previous version:** vX.Y.Z-1

## Summary

One or two sentences: what does this release change for someone running AMP in
production? Lead with the single biggest behavior change, not the longest list.

## Features

New capabilities, condensed from `CHANGELOG.md`'s `### Added` section. One
bullet per epic/feature, sub-bullets only for details a reader needs to decide
whether to enable something.

- **Feature name** (`EPIC-CODE`): what it does, one sentence. Default: enabled/disabled.
  - Notable sub-behavior, kill switch, or new metric, if it changes how you'd operate it.

## Performance / Improvements

Non-breaking fixes and hardening, condensed from `### Changed` / `### Improved`.

- **Area**: what got better and why it matters operationally (latency, correctness
  under load, resource use). Skip internal refactors with no observable effect.

## Breaking Changes

**Copy verbatim from `CHANGELOG.md`'s `### Breaking changes / migration notes`
section — do not paraphrase.** Each entry needs: what changed, who is affected,
and the one-line fix. If a change has no safe one-line fix, say so explicitly
instead of implying one exists.

- `CHANGE-CODE`: verbatim breaking-change text from CHANGELOG, including the
  before → after shape and the revert/mitigation if one exists.

## Backward Compatibility

Explicitly list what is **unaffected** by this release, especially anything a
reader might reasonably worry about given the Breaking Changes section above
(e.g. "Kubernetes-Secret-provisioned publishing targets: unchanged" or
"webhook payload shape: unchanged, upstream does not template it either").

- Item — unaffected, and why (one line, so this doubles as a FAQ).

## Upgrade Steps

Turn every Breaking Change above into a concrete action. Ordered by how likely
an operator needs it (config file edits before "check your dashboards").

1. If you rely on `X`, do `Y` before upgrading (config diff, flag flip, etc.).
2. ...
3. Deploy. Watch `<specific metric/log line>` for `<specific duration>` post-upgrade.

## Known Gaps / Not Yet Supported

Anything explicitly out of scope for this release that a reader might expect,
carried from CHANGELOG's own "not closed" / "ledgered" notes (`FU-*` tracking
codes) rather than silently omitted.

- `FU-CODE` — what's missing, why it's tracked separately, not blocking.
