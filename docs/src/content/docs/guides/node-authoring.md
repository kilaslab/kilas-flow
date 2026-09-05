---
title: Authoring nodes
description: Not yet written. How nodes are defined today and what has to exist before a guide is useful.
---

:::caution[This page has not been written yet]
:::

## What will be here

How to write a node someone else can install: the property description language,
the declarative routing format, testing it, and packaging it.

## Why it is not written yet

A guide to authoring community nodes needs a way to install one, and there is
not one yet. The pack format itself is fully data-driven — a pack is JSON, and
the loader takes bytes rather than a file path — but the only path that reaches
the registry today is a Go source file with a `go:embed` directive and a rebuild
of the binary. Writing an authoring guide before that changes would teach a
workflow that is about to be replaced.

## What exists today

Two kinds of node coexist, and both are real.

**Compiled nodes** live in `nodes/`. A node is a description — its properties,
their types, how they are displayed and when — plus an executor function. The
description language is `internal/property`; the contract and registry are
`internal/node`.

**Declarative packs** live in `packs/` as JSON and are interpreted at run time by
`internal/routing`, which turns the metadata on an operation into an HTTP
request. Nothing in a pack is Go. Two ship today: Telegram, written by hand, and
WAHA, generated from a vendored OpenAPI document by `cmd/nodepackgen` — which is
the more interesting of the two, because it shows that a pack can be produced
mechanically from an API specification.

`internal/nodepack` documents the on-disk format, and `packs/waha/README.md`
describes how the generated pack is produced and what the generator does with
the parts of an OpenAPI document that do not map cleanly.
