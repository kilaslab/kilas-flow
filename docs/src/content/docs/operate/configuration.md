---
title: Configuration reference
description: Not yet written. This page will be generated from the configuration struct rather than typed.
sidebar:
  order: 2
---

:::caution[This page has not been written yet]
:::

## What will be here

Every configuration key, its type, its default and what it does — **generated**
from `internal/config/config.go` rather than written by hand.

Generating it is the point of the page, not an implementation detail. The
repository already has a worked example of what happens otherwise:
`config.example.yaml` documents seven sections while the code defines twelve,
and the five it has fallen behind on include `outbound`, `webhook` and `embed` —
which are precisely the ones that define the security boundary. A hand-written
reference decays the same way, only less visibly, because a reader has no way to
tell which parts of it are still true.

## What to read in the meantime

`internal/config/config.go` is the only complete list. `config.example.yaml` is
correct about what it covers and simply does not cover everything.

The rules for reading configuration are on the [Install](/start/install/) page:
environment beats file, file beats defaults, and only the first underscore after
the `KILASFLOW_` prefix separates the section from the key.
