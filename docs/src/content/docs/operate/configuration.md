---
title: Configuration
description: How KilasFlow reads configuration, and what happens when keys are missing.
sidebar:
  order: 2
---

KilasFlow runs with no configuration file at all. Everything has a default;
the file and the environment exist to change the defaults that matter to an
installation.

## Precedence

Three layers, each overriding the one before:

1. Built-in defaults (see the [reference](/operate/configuration-reference/)).
2. The YAML file named by `--config` (default `config.yaml`). A missing file
   is not an error.
3. Environment variables prefixed `KILASFLOW_`.

`config.example.yaml` is a copy-edit starting point generated from the same
structs as the reference. Copy it to `config.yaml` and change values there;
never edit its structure — regenerating overwrites it.

## Environment variable names

The name is `KILASFLOW_<SECTION>_<KEY>`, uppercased: `server.port` is
`KILASFLOW_SERVER_PORT`, `database.max_open_conns` is
`KILASFLOW_DATABASE_MAX_OPEN_CONNS`. Only the **first** underscore after the
prefix separates the section from the key, because leaf keys contain
underscores themselves:

- `KILASFLOW_SERVER_READ_HEADER_TIMEOUT` → `server.read_header_timeout`
- `KILASFLOW_DATABASE_MAX_OPEN_CONNS` → `database.max_open_conns`

Naively splitting on every underscore would address a path that matches
nothing and would be silently ignored. This is also why every section name is
a single word (`outbound`, not `outbound_http`): a two-word section could
never be reached from the environment.

A variable, or a key in the file, that matches no setting is reported at boot
instead of being dropped in silence. The warning names the variable and where
it came from, and suggests the nearest key only when one is close enough to be
a typo. Three kinds of variable are read by the process itself and are never
reported: the one a `*_env` setting names (`KILASFLOW_ENCRYPTION_KEY` unless
`security.encryption_key_env` points somewhere else, and likewise for the
other secret-holding settings), the `KILASFLOW_WORKFLOW_ENV_` allowlist, and
the CLI's `KILASFLOW_URL` and `KILASFLOW_TOKEN`.

## Workflow expressions cannot read secrets

`$env` inside a workflow does **not** see the process environment. Only
variables under the separate `KILASFLOW_WORKFLOW_ENV_` prefix are exposed, so
a workflow expression can never read the database DSN, the credential master
key, or any other secret out of the process environment. Anything a workflow
needs from the environment must be copied under that prefix deliberately.

## What a missing key does at boot

Most missing keys fall back to defaults and the server starts. Three are
worth knowing precisely, because "still starts" is the behaviour an operator
misreads in production:

- **No credential encryption key** (`KILASFLOW_ENCRYPTION_KEY` unset). The
  server starts, runs workflows, and logs a warning — but credential storage
  is disabled: credential reads and writes report unconfigured. Set the key
  before storing anything. There is no re-encryption pass, so changing the key
  later makes every credential already stored undecryptable.
- **No embed signing key** (`KILASFLOW_EMBED_SIGNING_KEY` unset). The server
  starts with a warning, the session endpoints report themselves
  unconfigured, and every embed token is refused. Embedding is off until both
  the key and at least one `embed.allowed_origins` entry are set; an empty
  allowlist fails closed even with a key.
- **No config file at all.** Fully supported. The server boots on SQLite with
  an unauthenticated API on port 8080 and says so in the logs.
- **An empty `sql.sqlite_root`** (`KILASFLOW_SQL_SQLITE_ROOT=""`). The server
  starts and logs that SQLite credentials are disabled. A test or a node run
  using one is then refused, with a message naming the key. The default,
  `./data/sqlite`, confines each tenant's SQLite files to its own subdirectory.
  `sql.sqlite_unconfined: true` is the single-tenant escape hatch back to
  absolute paths, and it logs a warning at every boot. See
  [credentials](/concepts/credentials/#sqlite-files).

One missing key refuses to start rather than warn: `auth.enabled: true` with
no signing key. A server that answered every request with `401` would look
like a broken deployment rather than a misconfigured one, so the boot fails
with an error naming the variable.
