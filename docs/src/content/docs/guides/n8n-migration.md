---
title: Migrating from n8n
description: Not yet written. What the importer does today and where its limits are recorded.
---

:::caution[This page has not been written yet]
:::

## What will be here

A migration guide: which n8n node types map onto which KilasFlow nodes, what
happens to the ones that do not, and how to read the result of an import that
came through only partly.

## What exists today

KilasFlow's workflow document is a reimplementation of n8n's interchange format
in Go. No n8n source is used and no n8n package appears in any manifest — the
project is Apache-2.0 and n8n's packages are not, so the format is reimplemented
from its observable behaviour rather than adapted. A test in
`internal/guardrails` enforces that boundary on every run rather than trusting
anyone to remember it.

Import and export exist as API operations — `POST /api/v1/workflows/import` and
`GET /api/v1/workflows/{id}/export` — and are covered by tests. The mapping
table currently recognises 32 n8n node types.

An import that meets a node type with no equivalent does not fail. It
substitutes a placeholder node that preserves the original parameters and
refuses to run, so the shape of the workflow survives and the gaps are visible
in the editor instead of being silently dropped. An n8n Code node, which is
JavaScript or Python, is preserved the same way rather than being translated.

:::note
As of today neither operation is reachable from the editor — they exist on the
API and in the generated clients, but no button calls them. Putting them in the
editor is planned.
:::

## What to read in the meantime

`internal/interop/n8n` holds the converter and its doc comment, and
`internal/interop/n8n/corpus/BASELINE.md` scores the importer against a corpus of
real workflows, which is the honest measure of how far it currently gets. Note
that most of that corpus is not in the repository: the fixtures are third-party
workflows under licences this project cannot redistribute, so they are fetched
locally and never committed.
