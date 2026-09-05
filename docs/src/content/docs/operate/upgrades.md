---
title: Upgrades
description: Not yet written. What the migration machinery does today.
sidebar:
  order: 4
---

:::caution[This page has not been written yet]
:::

## What will be here

How to move between versions: what the schema migration does, whether it can be
rolled back, and what to check before and after.

Some of this genuinely cannot be written yet. There has been no release, so
there is no upgrade path between two of them and no compatibility policy that
has been tested by having to keep it.

## What exists today

Schema changes are SQL migration files under `migrations/`, one directory per
dialect, applied by a runner that records what it has applied in a
`schema_migrations` table it creates itself. There is an up and a down for each,
so a rollback is expressible rather than theoretical.

There is currently a single baseline migration per dialect, which is the state
you would expect of a project that has not shipped: the schema has been built up
in the repository rather than migrated in anybody's production database.
