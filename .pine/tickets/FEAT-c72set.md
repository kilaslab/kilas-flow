---
id: FEAT-c72set
title: 'Router skill: using-kilasflow-skills, the always-on non-negotiables and command reference'
status: doing
priority: medium
labels:
    - agent
    - docs
deps:
    - FEAT-x5qqpm
parent: EPIC-r0yg5q
phase: p1
created: "2026-09-20T07:47:52Z"
updated: "2026-09-20T07:47:52Z"
---

## Scope

Design §5.5 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`): the always-on router with the non-negotiables, red-flag rationalisations, skill index, compact command/tool reference and protocol order.

## Acceptance criteria

- [x] States the guardrails of FEAT-mha6a0: `activate` publishes a public endpoint; `delete` and `import` create or destroy authority; secrets are referenced by credential id only; an embed session can never activate/delete/import/manage datastores; anything unverifiable is fetched from the instance.
- [x] Idempotency guidance matches what shipped (FEAT-hj8pyx): `Idempotency-Key` on run and datastore writes.
- [x] The compact command reference is generated from the command tree, not typed.
- [x] Passes the same gates as the domain skills (skills drift gates ticket).

## Implementation notes

The router is `skills/using-kilasflow-skills/SKILL.md` — the thirteenth skill, one always-on file, no
reference files (§5.4 gives it none, so it has no `## Reference files` section). Its body uses the
six §5.3 sections and folds §5.5's eight items into them: the non-negotiables (1, 2), the strong
defaults (3), the red-flag table as `## Anti-patterns` (4), the skill index, the generated command
reference and the protocol order as three H3s under `## Decision tree` (5, 6, 7), and “reporting
skills used” (8) in `## Not shipped yet`, because no mutating verb records `--skills-used` in this
tree.

### The compact command reference is generated, not typed (criterion 3)

- `scripts/skills-command-reference/main.go` — renders the section into the SKILL.md between
  `<!-- generated:command-reference -->` and `<!-- /generated:command-reference -->`. The tree comes
  from `go run ./cmd/kilasflow help --json`, the document the binary hands an agent; nothing in the
  section is typed: `kilasflow <path>` is the tree's path, the operation column is the tree's
  `operation` (or `local` when the verb drives none), and the sentence is the tree's own summary.
  It lives in its own directory for the same reason `scripts/skills-index` does —
  `scripts/config-reference.go` already declares `func main` in package `main`.
- It refuses to write, or to pass `--check`, when the rendered document would break the bundle's
  rules (`skills.Parse` + `skills.Check` on the spliced document, the same guard `skills-index`
  applies) and when the frontmatter declares a command the tree does not implement. That second
  guard is why a splice cannot quietly satisfy a declaration: a verb that leaves the tree fails the
  generator instead of writing a reference that still teaches it.
- `Makefile` — `generate-skills-command-reference` and `generate-skills-command-reference-check`,
  appended after `generate-skills-index-check`, in the same shape (regenerate / fail if stale,
  printing `run: make generate-skills-command-reference`). No binary is built by hand: the generator
  builds and runs the CLI itself, so the targets need the Go toolchain only.
- `scripts/skills-command-reference/main_test.go` — the splice refuses a document with no marker, one
  marker, two sections or an inverted pair; render + splice is a fixed point that names every verb;
  `resolves` matches a whole verb path and not a prefix (`kilasflow workflow` is not a command).

### Idempotency guidance matches what shipped (criterion 2)

One sentence, worded exactly as `kilasflow-workflow-lifecycle`, `kilasflow-triggers` and
`kilasflow-datastore` word it: the server honours `Idempotency-Key` on `run-workflow` and on a
datastore row write, the same key with the same request replays the first outcome marked
`Idempotent-Replayed: true`, the same key with a different request is refused `409`, and no verb
carries the key yet, so a retry-safe run is
`kilasflow api run-workflow --path id=<workflowId> --header Idempotency-Key=<key> --body @input.json`
and a retry-safe write is the same header on `upsert-datastore-row`/`insert-datastore-row`.
`grep -rn 'Idempotency-Key' --include=*.go internal | grep -v _test` shows the surface the sentence
describes (handlers `datastores.go`, `idempotency.go`; `internal/idempotency/**`).

### Declaration cross-checks, against this worktree's tree

- `go run ./cmd/kilasflow help --json | jq -r '.data[].path' | sort > /tmp/paths.txt` → **39 verbs**.
- `grep -rh 'OperationID: *"' internal/api/handlers/ | sed 's/.*OperationID: *"\([^"]*\)".*/\1/' |
  sort -u` → **76 operation ids**.
- `comm -23 /tmp/decl.txt /tmp/paths.txt` → **0 unresolved** for the 13 declared `kilasflow_commands`
  (`api`, `context`, `credential list`, `datastore rows`, `exec get`, `exec trace`, `node describe`,
  `node list`, `node options`, `run`, `workflow create`, `workflow get`, `workflow list`).
- the same set difference against the 76 ids → **0 unresolved** for the 11 declared
  `kilasflow_operations`.
- a token walk over the whole body (longest prefix over the 39 paths) → **41 distinct tokens, 0
  unresolved**, including the generated section, whose every line is therefore a command the binary
  implements.

### Negative proofs

- Planted staleness: replaced one generated row with `kilasflow pack publish` →
  `make generate-skills-command-reference-check` exited 1 printing
  `run: make generate-skills-command-reference`; restoring the row made `--check` print
  `skills/using-kilasflow-skills/SKILL.md: 39 verbs, up to date`, exit 0.
- Planted declaration: renamed a declared command to `kilasflow datastore insert` → the generator
  refused with `using-kilasflow-skills [declarations] kilasflow_commands declares "kilasflow
  datastore insert", which the body never names`.
- Planted tree change: renamed the `context` verb in `internal/cli/context.go` to `briefing` → the
  generator refused with `using-kilasflow-skills declares 1 command(s) the binary does not implement:
  kilasflow context`, even though the prose still names it. Restored (`md5sum` identical).
- Planted prefix: declared `kilasflow node` instead of `kilasflow node list` → refused with
  `declares 1 command(s) the binary does not implement: kilasflow node`.

### Gates run, with outcomes

- `make generate-skills-index-check` → `skills/index.json: 13 skills, up to date`, exit 0.
- `make generate-skills-command-reference-check` → `skills/using-kilasflow-skills/SKILL.md: 39 verbs,
  up to date`, exit 0.
- `go test -count=1 ./internal/skills/...` → ok; `go test -count=1 ./scripts/skills-command-reference/...`
  → ok.
- `gofmt -l scripts/skills-command-reference/` → no output; `go vet` and `go build ./...` → clean.
- Criterion 4 is ticked for the gates that exist today: the two bundle generators' checks and the
  `internal/skills` suite, the same ones the twelve domain skills pass. Design §5.7's G1–G5 do not
  exist yet — they are FEAT-bb4s6e — and G1 (every declared command, and every `kilasflow …` inside a
  fenced block, resolves in the real tree) is the one this section's generator already enforces for
  the router. `make skills-check` should include `generate-skills-command-reference-check` when
  FEAT-bb4s6e wires it.

### Notes for the orchestrator

- The `## Not shipped yet` rows and the guardrail sentence are measured against **this worktree's**
  tree, where no verb is guarded: activation, deletion and import are described as
  `kilasflow api <operation-id>` calls with no `--yes` to lean on, and `--skills-used` as absent.
  FEAT-m4d2y1's guarded verbs (and FEAT-4jns31's `skills` verbs) land in parallel; when they do, the
  generated block is what goes stale first (`make generate-skills-command-reference-check` fails with
  the line that changed, and the generator adds `[guarded]` to a guarded verb's row automatically),
  and the prose rows need re-measuring by hand. The `--yes` rule itself is worded to survive that:
  it states the rule and lets the generated section say which verbs are guarded.
- The generated section is the only part of the bundle that is a product fact rather than prose; it
  is regenerated with `make generate-skills-command-reference`, and `skills/index.json` with
  `make generate-skills-index`.
- The throwaway cross-check script used above was not committed: the generator enforces the command
  half permanently, and the operation half is FEAT-bb4s6e's G2.
