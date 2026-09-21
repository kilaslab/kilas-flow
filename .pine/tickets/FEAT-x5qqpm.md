---
id: FEAT-x5qqpm
title: 'Agent skills bundle v1: the domain skills, generated index, honest ''Not shipped yet'' sections'
status: todo
priority: medium
labels:
    - agent
    - docs
deps:
    - FEAT-bp59m4
parent: EPIC-r0yg5q
phase: p1
created: "2026-09-20T07:47:52Z"
updated: "2026-09-20T07:47:52Z"
---

## Scope

Design §5 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`): `skills/<name>/SKILL.md` with `references/*.md`, frontmatter per §5.2, body conventions per §5.3, and the 12 domain skills of the §5.4 inventory (the router skill is its own ticket). Each skill states the CLI verb first and the HTTP operation second.

## Acceptance criteria

- [ ] The 12 domain skills exist with the frontmatter contract of §5.2 (`name`, `description`, `kilasflow_skills_version`, `kilasflow_commands`, `kilasflow_operations`, `kilasflow_nodes`, `kilasflow_expression_roots`, `kilasflow_not_shipped`).
- [x] `skills/index.json` is generated, not hand-written, and a test fails when it is stale.
- [ ] Every skill with a non-empty `kilasflow_not_shipped` carries a `## Not shipped yet` section naming each entry (§5.8: WASM packs, sidecar, agent tokens, tenant deletion, idempotency — updated to whatever has shipped by the time this lands).
- [ ] No skill instructs an agent to put a secret in a workflow document, CLI argument or chat.

## Implementation notes

Stage 1 of 4 (bundle machinery plus the first skill, `kilasflow-workflow-lifecycle`) is committed on this branch. Criteria 1, 3 and 4 are bundle-level and are deliberately left unticked: 1 needs all twelve skills, 3's §5.8 set spans skills written in stages 2-4, and 4's positive half is the sentence `kilasflow-credentials` must carry (stage 3). The rules that decide 3 and 4 are implemented, tested and currently satisfied by the one skill that exists; the notes below say exactly what was proven.

### What was added

- `internal/skills/skill.go` — the frontmatter contract. `SkillsVersion = 1`, `Skill`, `Parse`, `LoadDir`, `SplitFrontmatter`. All eight §5.2 keys are required; an unknown key, a missing key, a wrong type (naming want/got), a name that does not equal its directory, a stamp that is not `SkillsVersion`, and a description that does not start `Use when` / contain `Triggers on` are all refused. The frontmatter is parsed with `github.com/knadh/koanf/parsers/yaml` (`yaml.Parser().Unmarshal`), already a direct dependency (v1.1.1, MIT); **no dependency was added**. References come from the `references/*.md` listing, sorted, never from the frontmatter.
- `internal/skills/index.go` — `Index(skills)`: the canonical `skills/index.json` (stamp, `generatedBy`, one entry per skill; sorted and deduplicated lists, two-space indent, one trailing newline, no timestamp, no absolute path).
- `internal/skills/check.go` — pure `Check(skills)` with six rules: `section-order`, `not-shipped`, `references`, `declarations`, `secret-hygiene`, `not-shipped-commands`.
- `internal/skills/bundle_test.go`, `internal/skills/check_test.go`, and 17 planted bundles under `internal/skills/testdata/planted/**` plus one clean control under `internal/skills/testdata/clean/**`.
- `scripts/skills-index/main.go` — writes the index, refuses to write (or to pass `--check`) while `Check` reports findings, and on staleness prints a short diff summary plus `run: make generate-skills-index`. It lives in its own directory because `scripts/config-reference.go` already declares `func main` in package `main`.
- `Makefile` — `generate-skills-index` and `generate-skills-index-check`, appended after `generate-config-reference-check`.
- `skills/kilasflow-workflow-lifecycle/SKILL.md` with `references/VALIDATION_CHECKLIST.md` and `references/NAMING_AND_DESCRIPTIONS.md`, and the generated `skills/index.json`.

### Declaration cross-checks (authoring rule 3)

Run in the worktree, over `skills/kilasflow-workflow-lifecycle/SKILL.md` and both reference files:

- `go run ./cmd/kilasflow help | jq -r '.data[].path'` → 39 verbs; all 13 declared `kilasflow_commands` are in that list.
- `grep -rh 'OperationID: *"' internal/api/handlers/ | sed 's/.*OperationID: *"\([^"]*\)".*/\1/' | sort -u` → 74 operation ids; all 14 declared `kilasflow_operations` are in that list.
- a scratch checker walked every `kilasflow <verb>` token in the skill and its references (45 tokens, 8 of them inside fenced blocks) and resolved each against the command tree by longest prefix: 0 unresolved. No verb is named that does not exist; update, import and restore are taught only as operation ids through `kilasflow api`.
- `kilasflow_nodes: []` and `kilasflow_expression_roots: []` in this skill, so there is nothing to cross-check against `nodes.RegisterAll` or `expression.Roots()` in stage 1.

### Negative proofs (recorded as the stage requires)

- Planted staleness: hand-edited the committed `skills/index.json` (description changed) → `go test ./internal/skills/ -run TestIndexIsFresh` failed with `skills/index.json is stale: regenerate it with make generate-skills-index`, and `make generate-skills-index-check` exited 1 with `run: make generate-skills-index`; after restoring the file both pass.
- Planted violation: removed the `put the token` sentence from `testdata/planted/literal-token-in-prose` → `go test ./internal/skills/ -run TestCheckRejectsPlantedViolations` failed with `literal-token-in-prose: Check(...) reported nothing for a planted secret-hygiene violation`; after restoring the fixture it passes. (17 fixtures cover the six content rules and the six frontmatter refusals, and `TestPlantedFixturesAreAllUsed` fails if a fixture directory is not exercised.)

### Gates run, with outcomes

- `gofmt -l internal/skills/*.go scripts/skills-index/*.go` → no output.
- `go vet ./...` → clean. `go build ./...` → clean.
- `go test -race -count=1 ./internal/skills/... ./scripts/... ./internal/guardrails/...` → all ok.
- `make generate-skills-index-check` → `skills/index.json: 1 skills, up to date`, exit 0.
- `sh scripts/check-coordinates.sh` → exit 0.

### Status of the skill's own "not shipped" rows (today's tree)

- Idempotent runs: `grep -rn 'Idempotency-Key' --include=*.go internal | grep -v _test` is empty, so the row stands (FEAT-hj8pyx is in flight in this wave; re-check before the final commit).
- Update, import and restore from the CLI: `go run ./cmd/kilasflow help | jq -r '.data[].path' | grep -E 'workflow (update|import|restore|validate)'` is empty, so the row stands. The §5.8 row-by-row reconciliation for the other skills belongs to stage 4.

### Notes for the orchestrator

- Deviation from the plan's struct sketch: `Skill` carries an extra `ReferenceText map[string]string` (reference file text, keyed like `References`). `Check` must stay pure, and a secret-hygiene violation in a reference file is as wrong as one in a body, so the loader hands the text through instead of the checker reading the disk.
- The plan's stage 2 fixture list is implemented as 17 fixtures: the five frontmatter refusals cannot be `Check` findings (the bundle never loads that far), so they are asserted as loader errors, and the "bad description" case is split into prefix and `Triggers on` fixtures. Two extra fixtures (`secret-rule-missing`, `literal-token-in-reference`) cover the positive half of criterion 4 and the reference-file half of the denylist.
- Stage 4's plan text teaches export as `kilasflow workflow export <id> --out wf.json`; the real verb has `--format` only (no `--out` — that flag is on `kilasflow api`). Worth fixing when stage 4 is authored.
