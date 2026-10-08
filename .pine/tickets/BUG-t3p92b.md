---
id: BUG-t3p92b
title: '`skills check` flags every non-KilasFlow skill in the harness dir as drift; `install --scope project` resolves to cwd'
status: done
priority: medium
labels:
    - skills
    - cli
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-10-08T15:45:21Z"
---

# Description

In a real project the skills directory is shared with other skills (Pine, for example), so `skills check` always fails, and `--force` cannot clear it.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: cli-skills; finding ids: CLI-7, CLI-16). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## CLI-7: `skills check` counts every non-KilasFlow skill in the harness directory as drift, so it fails in any real project

*bug · medium · agent-skills*

**Steps to reproduce:**

1. In a git project: `kilasflow skills install` (to `./.agents/skills`). 2. Add another skill, e.g. `.agents/skills/pine/SKILL.md`; this repository keeps pine in both `.agents/skills` and `.claude/skills`. 3. `kilasflow skills check`. 4. `kilasflow skills install --force`, then `check` again.

**Actual:**

Exit 1, `skills_drift`: `"pine is installed but this binary ships no such skill"` (`kind: extra`). `--force` cannot clear it. The only way to get exit 0 is to delete the user's other skills. A CI gate on `skills check` is unusable in a shared harness directory.

**Expected:**

Report `extra` only for directories whose SKILL.md carries `kilasflow_skills_version` or uses the `kilasflow-` prefix, i.e. a KilasFlow skill this build no longer ships.

**Suggested fix:**

Filter extras by the bundle's frontmatter stamp or name prefix. Keep `--strict` for the current behaviour.

**Evidence:**

cs/proj/ (the scratch project), the transcript output, internal/cli/verbs_skills.go:853-880 (`extraSkills` treats every directory with a SKILL.md as extra).

**Related:**

FEAT-bb4s6e


## CLI-16: `skills install --scope project` resolves under the current directory, not the checkout root

*ux · low · agent-skills*

**Steps to reproduce:**

`cd proj/sub/deeper && kilasflow skills install --target claude --dry-run`

**Actual:**

`path: …/proj/sub/deeper/.claude/skills`. cli.md:606-609 and the flag help say project scope "resolves under the checkout". Codex scans `.agents/skills` between the repo root and cwd, and Claude Code expects `<root>/.claude/skills`, so an install from a subdirectory lands in a nested copy the team doesn't share.

**Expected:**

Walk up to the git root (fall back to cwd), or say "cwd" in the docs.

**Suggested fix:**

Resolve the project root with `git rev-parse --show-toplevel` semantics (the nearest `.git`).

**Evidence:**

internal/cli/verbs_skills.go:173-180 (`os.Getwd()`).

**Related:**

none


# Acceptance Criteria
- [x] `extra` is reported only for directories that are KilasFlow skills (carrying `kilasflow_skills_version` or the `kilasflow-` prefix)
- [x] `--scope project` walks up to the git root, falling back to cwd, and the docs say which

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-bb4s6e

# Fix

- **CLI-7.** `extraSkills` now skips any skill directory that is not KilasFlow's own. `kilasflowSkill` treats a directory as KilasFlow's when its name starts with `kilasflow-` or its `SKILL.md` frontmatter has the `kilasflow_skills_version` key, whatever the value. The router, `using-kilasflow-skills`, has no prefix, so the stamp is what covers it. The key check is `skills.Stamped`, next to `DeclaredVersion` in `internal/skills/skill.go`. Pine, or any skill without frontmatter, sitting next to the bundle no longer counts as drift. A stamped or `kilasflow-` skill that this build doesn't ship is still reported as `extra`.
- **CLI-16.** In project scope, `skillsScopeRoot` now walks up from the working directory to the nearest directory containing `.git`. That can be a directory (a clone) or a file (a linked worktree or submodule), which matches `git rev-parse --show-toplevel` without needing git installed. Outside a checkout it falls back to the working directory. `install`, `check` and a relative `dir:<path>` all resolve through the same root. The `--scope` flag help for install and check now uses one shared string that describes this. `docs/src/content/docs/reference/cli.md` documents the root and the fallback, plus the rule for what counts as KilasFlow's own skill in `check`.
- No `--strict` flag was added. The finding suggested one to keep the old behaviour, but the acceptance criteria don't ask for it. If someone needs the old "every SKILL.md is an extra" gate, it should be a follow-up.
- Tests: `TestSkillsCheckLeavesOtherSkillsAlone` covers pine and a skill without frontmatter (check passes), then a stamped skill without the prefix (reported, and pine is not named). `TestSkillsProjectScopeResolvesToTheCheckoutRoot` runs from `proj/sub/deeper`, with `.git` as a directory and as a file, through `--target claude`, a relative `dir:<path>` and `check`. `TestStampedTellsKilasFlowSkillsFromOthers` checks the marker table and that every shipped skill carries the stamp. `go test ./internal/skills/ ./internal/cli/` and `go vet` on both pass, and `make generate-skills-command-reference-check` reports the reference up to date.

# Related Files

- internal/cli/verbs_skills.go
- internal/cli/verbs_skills_test.go
- internal/skills/skill.go
- internal/skills/bundle_test.go
- docs/src/content/docs/reference/cli.md

# Attachments
