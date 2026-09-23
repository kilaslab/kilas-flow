---
id: BUG-t3p92b
title: '`skills check` flags every non-KilasFlow skill in the harness dir as drift; `install --scope project` resolves to cwd'
status: todo
priority: medium
labels:
    - skills
    - cli
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T03:06:00Z"
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
- [ ] `extra` is reported only for directories that are KilasFlow skills (carrying `kilasflow_skills_version` or the `kilasflow-` prefix)
- [ ] `--scope project` walks up to the git root, falling back to cwd, and the docs say which

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-bb4s6e

# Related Files

# Attachments
