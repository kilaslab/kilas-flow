---
title: API contract and stability
description: Not yet written. There is no stability promise yet, which is itself worth stating.
sidebar:
  order: 2
---

:::caution[This page has not been written yet]
:::

## What will be here

Which parts of the API are public, what may change without notice, how a
breaking change would be signalled, and how long a deprecated operation would
keep working.

## The honest position today

There is no stability promise, because there has been no release to make one
about. The version reported by the binary and carried in the OpenAPI document is
derived from `git describe`, and with no tags in the repository that resolves to
a commit hash. Anything you build against the API today should expect it to
move.

The one thing that is already true and will not change quietly: the OpenAPI
document is generated from the Go types that serve the requests, so it cannot
describe an API the server does not implement. Whatever the contract turns out
to say, it will be checked against a real binary rather than against a
checked-in file — the repository already fails its own build if a generated
client drifts from a freshly built server.
