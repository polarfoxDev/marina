---
name: changelog-release
description: Cut a new Marina release - bump VERSION, add a CHANGELOG.md entry, and update the comparison links at the bottom of the file. Use whenever the user asks to "update the changelog", "bump the version", "prepare a release", or similar, or whenever a commit is about to bump VERSION.
---

# Changelog / release checklist

This repo's `.github/workflows/release.yml` auto-tags and publishes a GitHub
release whenever `VERSION` changes on a push to `main`. It reads the new
version from `VERSION` and extracts the matching `## [X.Y.Z]` section from
`CHANGELOG.md` verbatim as release notes. Getting the mechanical parts right
matters more than usual here because a mistake ships straight to a GitHub
release.

Do these steps **in order**, every time:

1. **Check for a pre-existing `## [Unreleased]` section** with entries under
   it. If there is content there, fold it into the new version's section
   instead of duplicating it.

2. **Pick the version number.** This project is pre-1.0 and follows semver
   loosely:
   - Dependency-only bumps, non-breaking fixes, small additive features →
     bump **minor** (`0.11.0` → `0.12.0`). This has been the convention even
     for changes as small as "updated dependencies" (see `0.10.0`).
   - Breaking config/API changes → still bump **minor** pre-1.0, but mark
     every breaking bullet with **Breaking** (see `0.11.0` for the pattern).
   - Pure docs/CI tweaks with no user-facing change generally don't warrant
     a release at all — ask before bumping VERSION for those.
   - Get the current version from `VERSION` (not just the latest git tag —
     they should match, but `VERSION` is the source of truth the release
     workflow reads).

3. **Update `VERSION`** to the new number (bare number, no `v` prefix, no
   trailing content beyond the version and a newline).

4. **Add the new `## [X.Y.Z] - YYYY-MM-DD` section to `CHANGELOG.md`**,
   directly under `## [Unreleased]`, using today's actual date. Follow Keep a
   Changelog subsections (`### Added` / `### Changed` / `### Fixed` /
   `### Removed`) and this project's terse bullet style — look at recent
   entries for tone and level of detail. Call out anything a maintainer would
   otherwise be surprised by later (e.g. a dependency intentionally held back
   and why), not just what changed.

5. **Update the comparison links at the very bottom of `CHANGELOG.md`.**
   This is the step that's easy to forget — it's a separate section, off
   the bottom of the visible diff, and nothing will fail loudly if you skip
   it. Concretely:
   - Change `[Unreleased]: .../compare/vOLD...HEAD` to
     `[Unreleased]: .../compare/vNEW...HEAD`
   - Insert a new line right below it:
     `[vNEW]: .../compare/vPREVIOUS...vNEW`
   - Leave every older link untouched.
   After editing, `tail -30 CHANGELOG.md` and visually confirm the new
   version has both a body section above and a link line below before
   moving on.

6. **Verify before committing**: re-read the full diff of `VERSION` and
   `CHANGELOG.md` together. The version number in `VERSION`, the heading in
   `CHANGELOG.md`, and the new link line must all agree exactly (same
   number, no `v` mismatches).

7. **Check `git status`/`git fetch` before committing or amending anything**
   in this repo. This project sometimes has other sessions or the user
   working on `develop` concurrently — never `git commit --amend` without
   first confirming `HEAD` is still the commit you think it is (`git log -1
   --format=%H` before and after any commit is a cheap sanity check), and
   never assume a locally-dirty file you didn't touch is safe to leave
   alone without asking.

8. Only commit when the user has asked for a commit; only push/tag if
   explicitly asked — the release workflow triggers automatically once
   `VERSION` lands on `main` via the normal PR/merge flow, so pushing
   directly to `main` is not this skill's job.
