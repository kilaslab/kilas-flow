---
title: Architecture
description: Not yet written. Where the accurate description of the architecture lives today.
---

:::caution[This page has not been written yet]
:::

## What will be here

One page per concept rather than one per Go package: the workflow document and
its versions, how a graph is compiled and executed, what an item is and how it
flows between nodes, expressions, credentials, triggers, and the event stream.

The material already exists and is good — it is in the doc comments on the
packages under `internal/`, which are the most carefully written prose in the
repository. The problem this page solves is not that the explanation is missing;
it is that reading it requires cloning the repository and opening files.

## What to read in the meantime

`README.md` has a `Layout` section that names every package and says in one line
what it is for, which is the fastest way to orient yourself. From there the doc
comment at the top of each package is the real explanation. The ones that repay
reading first are `internal/workflow`, `internal/engine`, `internal/node` and
`internal/expression`.

Two structural facts worth knowing before you start, because they explain a lot
of the layout: the engine does not import the HTTP layer or the AI layer — it
reaches persistence through interfaces and the agent runtime through an injected
one — and the editor is compiled into the binary rather than served beside it.
