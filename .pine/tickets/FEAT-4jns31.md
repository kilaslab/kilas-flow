---
id: FEAT-4jns31
title: 'Embed the skills bundle and add the skills verbs: list, show, install, check, export'
status: doing
priority: medium
labels:
    - agent
    - cli
deps:
    - FEAT-x5qqpm
parent: EPIC-r0yg5q
phase: p1
created: "2026-09-20T07:47:52Z"
updated: "2026-09-21T00:00:00Z"
---

## Scope

Design §5.6 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`): the bundle is embedded with `//go:embed` (the way `internal/web` embeds the SPA) so `kilasflow skills install` works from the distroless image with no checkout.

## Acceptance criteria

- [x] `skills list|show|install|check|export` per §5.6 and the CLI surface block of FEAT-mha6a0.
      Registered in `internal/cli/command.go` (one line), all five with `Operation: ""` and
      pinned in `TestPhaseOneCommandTree`; driven by `internal/cli/verbs_skills.go` and proved by
      `go test -count=1 -run TestSkills ./internal/cli/...`.
- [x] Install targets `claude`, `codex`, `agents` and `dir:<path>`, scopes `user` and `project`; a
      round-trip test installs into a temp directory and asserts the file layout.
      `TestSkillsInstallAndCheckNeedNoCheckout` (every embedded file byte-identical on disk, every
      skill directory with its `SKILL.md` and references, `index.json` present) and
      `TestSkillsInstallDefaultsToTheCheckout` (project scope + `agents` target is `./.agents/skills`;
      `--scope user --target claude` is `$HOME/.claude/skills`).
- [x] `skills check` compares the installed bundle's version stamp with the binary; mutating a stamp
      makes it report drift and exit non-zero.
      `TestSkillsCheckReportsDriftWhenAStampChanges` (exit 1, `error.code = "skills_drift"`, message
      naming the file and both versions); out-of-band: the built binary on a mutated stamp prints
      `kilasflow-triggers/SKILL.md declares bundle v0; this binary ships bundle v1` and exits 1.
- [x] Works from the shipped image (no filesystem beyond the install target).
      `skills/embed.go` embeds the bundle; `go build -o /tmp/kf ./cmd/kilasflow` run from an empty
      working directory with a temporary `HOME` installs 13 skills into `dir:<temp>`, and
      `diff -r <temp> skills --exclude=embed.go` is empty. The same build against the real Docker
      context (`FROM golang:1.27-alpine`, `COPY . .`) compiles `./skills`, and exporting the context
      shows `skills/` arrives byte for byte, so `.dockerignore` needs no change: its `*.md` matches
      root-level markdown only.

## Implementation notes

**The embed lives in the bundle.** `skills/embed.go` is a one-directive package standing in
`skills/`, because `go:embed` can only reach a package's own directory and the tree below it. The
design's §5.1 sketch (a Makefile sync into `internal/skills/bundle/`) was not taken: a generated,
gitignored copy means a fresh clone and a bare `go build` produce a binary with no bundle at all,
and a committed copy means the thirteenth skill silently goes missing until somebody runs the sync.
Embedding the source tree has neither failure mode, and it is what makes the router skill that
landed with FEAT-c72set arrive automatically.

The three patterns spell the bundle's *shape* — `index.json`, `*/SKILL.md`,
`*/references/*.md` — rather than naming today's skills, and together they exclude the directive's
own Go source. `internal/skills/embed_test.go` asserts the embedded tree and `skills/` hold the
same files byte for byte, in both directions, so a file no pattern matches fails a test rather
than shipping missing. `internal/skills` now reads the embedded tree with the same loader as the
checkout (`BundleFS`, `LoadBundle`, `loadFS`/`loadSkills`), so a verb cannot install a bundle the
checker refuses.

**The five verbs are local.** `Operation: ""`, no HTTP, like `pack validate`; `internal/cli/doc.go`
records `internal/skills` as the second sanctioned exception to "the CLI talks HTTP". Shapes:
`list` reads the frontmatter (names, triggers, declarations, references; `--quiet` = one name per
line). `show` prints the document raw — on a pipe too, so `skills show <name> > SKILL.md` works —
with `--json` returning it in `data.content` and `--quiet` printing its path. `install` resolves
`--target claude|codex|agents|dir:<path>` under `--scope project` (default) or `user`, is
idempotent for identical files, refuses to overwrite a differing file with exit 5
(`install_conflict`) unless `--force`, and `--dry-run` writes nothing. `check` compares every file
byte for byte and every installed `SKILL.md` stamp, reports `missing`/`unreadable`/`version`/
`content`/`extra` under `error.detail.issues`, and exits 1 (`skills_drift`); it never repairs.
`export` writes the shipped index (`--format json`, default) or a deterministic tarball
(`--format tar`); `--out -` streams.

Two deliberate divergences, both recorded in the CLI reference: `cursor` is not a target (the
parent ticket's CLI surface and this ticket's criteria name claude, codex, agents and `dir:<path>`;
`dir:` already reaches that directory), and `show` streams on a pipe rather than emitting the
envelope, which is why it is listed with the other byte-writing verbs in
`docs/src/content/docs/reference/cli.md`.

**The bundle's generated half was refreshed.** The five verbs make the router skill's compact
command reference stale, so `make generate-skills-command-reference` was run — the section is
generated from `kilasflow help --json` by FEAT-c72set's generator, and the check refuses a stale
one. Only that generated section changed (5 lines in `skills/using-kilasflow-skills/SKILL.md`); no
skill prose or frontmatter was edited, so `make generate-skills-index-check` still reports
`13 skills, up to date`.

## Files

```
 .pine/tickets/FEAT-4jns31.md                       | this ticket
 CHANGELOG.md                                       | the five verbs under [Unreleased] Added
 docs/src/content/docs/reference/cli.md             | new `kilasflow skills` section, tree row,
                                                      local-verb list, failure codes, streamed verbs
 internal/cli/command.go                            | registry: skillsVerbs()
 internal/cli/command_test.go                       | the five verbs in the phase-1 tree and its locals
 internal/cli/doc.go                                | internal/skills as a sanctioned exception
 internal/cli/verbs_skills.go                       | the five verbs, their payloads and human forms
 internal/cli/verbs_skills_test.go                  | round-trip, defaults, idempotence, drift, export
 internal/skills/bundle.go                          | BundleFS() and LoadBundle()
 internal/skills/skill.go                           | LoadDir over the shared fs.FS loader; DeclaredVersion
 internal/skills/embed_test.go                      | the embedded tree equals skills/, byte for byte
 skills/embed.go                                    | the //go:embed directive and its patterns
 skills/using-kilasflow-skills/SKILL.md             | the regenerated command-reference section
```

## Evidence

- `go test -count=1 ./internal/cli/... ./internal/skills/... ./cmd/...` — ok (also `./scripts/...`).
- `go build ./... && go vet ./...` — clean; `gofmt -l` on every touched file — silent.
- `make generate-skills-index-check` — `skills/index.json: 13 skills, up to date`, exit 0.
- `make generate-skills-command-reference-check` — `skills/using-kilasflow-skills/SKILL.md: 58
  verbs, up to date`, exit 0.
- Built binary from `/tmp` with a temporary `HOME`, `diff -r` against `skills/` — identical;
  `skills check --quiet` — `true`, exit 0; after mutating a stamp — exit 1, `skills_drift`.
- Docker context export (`FROM scratch` + `COPY .`) — `skills/` byte for byte;
  `go build ./skills` in `golang:1.27-alpine` with the real context — succeeds.
