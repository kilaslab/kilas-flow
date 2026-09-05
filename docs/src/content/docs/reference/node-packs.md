---
title: Node pack format
description: Not yet written. Where the format is currently specified.
sidebar:
  order: 3
---

:::caution[This page has not been written yet]
:::

## What will be here

The pack file format: every field, what the routing interpreter does with it,
and how an operation description becomes an HTTP request.

## What exists today

A pack is JSON. It describes resources, operations and properties, and it
carries enough routing metadata for `internal/routing` to build the request at
run time. There is no Go in a pack, which is what makes it possible for one to
be generated — `cmd/nodepackgen` produces the WAHA packs from a vendored OpenAPI
document, and `make node-packs` regenerates them.

Three pack node types ship today: Telegram, hand-written; and WAHA plus its
trigger, generated, at two API versions each.

The format is specified by `internal/nodepack`, whose doc comment is the current
reference, and `packs/waha/README.md` describes the generator's behaviour
including what it does with the parts of an OpenAPI document that do not map
cleanly onto a node property.

:::note
Packs are loaded from inside the binary today. Installing one without rebuilding
is planned and is a prerequisite for this page being much use to anyone outside
the repository.
:::
