---
id: FEAT-cwz4ac
title: Ship a pack authoring toolchain
status: todo
priority: medium
labels:
    - packs
    - platform
deps:
    - FEAT-czbzs6
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:57:40Z"
updated: "2026-09-05T11:57:40Z"
---

## Scope

Once V2-p10-15 makes a pack installable, an author needs a way to produce one that is not "read `internal/nodepack/nodepack.go` and guess".

There is no CLI to hang this on. `cmd/kilasflow` parses exactly two flags, `-config` and `-version`, and has no subcommands. `cmd/nodepackgen` exists and is a build-time generator for one job — an OpenAPI document plus a manifest into a pack, driven by `make node-packs` with five path flags — not an authoring tool.

The feedback an author gets today is decoder output. `nodepack.Decode` uses `DisallowUnknownFields`, which is the right strictness — a pack using a feature this build does not implement must fail loudly rather than silently do nothing — but the message a Go JSON decoder produces for an unknown field names the field and nothing else. An author who wrote `parameters` where the schema wants `properties`, or who put a `routing` block at the wrong nesting depth, gets a sentence about an unknown field and no indication of what was expected there.

There is also no published schema. `schemas/` holds one file, `workflow-v1.schema.json`, a JSON Schema draft 2020-12 document for the workflow format whose only consumer is a test in `internal/workflow/document_test.go`. Nothing equivalent describes a pack, so an author gets no editor completion, no inline validation, and no way to check a file before handing it to a server.

And the two existing packs are not equally useful as examples. `packs/waha/*.json` is generated, large, and carries a `generator` provenance block an author should not imitate. `packs/telegram/pack.json` is hand-written and is the real model, but nothing says so.

## Acceptance criteria

- [ ] The binary gains a `pack` subcommand group without changing how the server is started, so existing invocations and the Dockerfile entrypoint are unaffected.
- [ ] `pack init` produces a minimal working pack — one resource, one operation, one credential reference — that loads and executes without further editing.
- [ ] `pack validate` reports every problem in a file at once with a JSON path and an explanation of what was expected, rather than aborting on the first unknown field.
- [ ] `pack validate` catches the errors the server would refuse at registration, not only decoding errors: unknown property kinds, invalid visibility conditions, malformed subtitles, unsafe icons, duplicate keys, and refused routing features.
- [ ] A JSON Schema for the pack format is published in `schemas/` and is generated from or verified against the Go types, so it cannot describe a format the decoder does not accept.
- [ ] `pack report` emits the coverage report convention the WAHA packs already use, naming what a pack does not cover.
- [ ] The checksum record V2-p10-15's loader verifies is produced by this tooling, so an operator is never asked to compute a digest by hand.
- [ ] `packs/telegram/pack.json` is identified as the reference hand-written example, and the generated WAHA packs are marked as generated so an author does not copy the provenance block.

## Implementation Plan

Add the subcommand group carefully, because the entrypoint is load-bearing in two places: `ENTRYPOINT ["/app/kilasflow"]` in the `Dockerfile` with no arguments, and `./bin/kilasflow -config ''` in the smoke scripts. A bare invocation must still start the server. Recommend dispatching on a first non-flag argument and leaving the flag-only path exactly as it is, rather than adopting a subcommand framework that would make `kilasflow` with no arguments print help — that would break `smoke-docker` and every deployment at once.

The validator is the part that decides whether authoring is pleasant, and it should not be a second implementation of the rules. `validateDefinition` in `internal/node/registry.go` already enforces most of them — known property kinds, per-level duplicate keys, valid visibility, subtitle templates limited to `{{ $parameter.… }}`, XSS-safe icons, known connection kinds, non-empty executor. Validation should run the real registration path against a throwaway registry and report what it refuses, so the tool and the server can never disagree. The only thing to add is collecting multiple errors instead of returning the first, which is the difference between one round trip per mistake and one round trip total.

For the schema, generate it from the Go types rather than writing it by hand, and check it in CI the way the API client is checked. A hand-written schema beside a `DisallowUnknownFields` decoder is two definitions of one format, and the drift is invisible until an author trusts the wrong one.

`pack init` should scaffold from the Telegram pack's shape rather than an invented minimal example, because Telegram is the pack that demonstrates the format's real ergonomics — a hand-written declarative pack with resources, operations, a credential type and an options cascade.

One thing to decide rather than default: whether `pack build` exists at all. There is nothing to compile — a pack is JSON that the server reads — so `build` would only bundle a directory and write a checksum. Recommend naming it for what it does, so authors are not left looking for a compilation step that does not exist and cannot be broken.

Keep `cmd/nodepackgen` where it is. It is a build-time generator for OpenAPI-derived packs invoked by `make node-packs`, and folding it into the server binary would put a generator on the deployment artifact for no benefit. V2-p10-18's converter belongs beside it, not here.

## References

- Roadmap plan, p10 section, entry V2-p10-16: `.pine/roadmap.md`.
- `cmd/kilasflow/main.go` — the two-flag interface this ticket extends without breaking.
- `cmd/nodepackgen/` — `main.go`, `generate.go`, `openapi.go`, the existing generator and its report output.
- `internal/nodepack/nodepack.go` — `Pack`, `Decode` with `DisallowUnknownFields`, `Load`, `Register`, and the `Provenance` block authors should not imitate.
- `internal/node/registry.go` — `validateDefinition`, `validateSubtitle`, `knownGroups` and `ValidateIcon`, the rules the validator must reuse rather than restate.
- `internal/property/property.go` — the closed `PropertyKind` set and `ValidateVisibility`.
- `internal/routing/routing.go` — the routing subset, and the registration-time refusal of `preSend` and function-form `postReceive`.
- `packs/telegram/pack.json` — the reference hand-written pack.
- `packs/waha/REPORT-202409.md` — the coverage report convention `pack report` reproduces.
- `schemas/workflow-v1.schema.json` — the existing schema's shape and location.
- `Dockerfile`, `scripts/smoke-docker.sh`, `scripts/smoke-sqlite.sh` — the invocations a subcommand group must not break.
