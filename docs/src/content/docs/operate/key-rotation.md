---
title: Key rotation
description: How to replace the credential encryption key, and the one-time reseal after upgrading to versioned seals.
sidebar:
  order: 5
---

The credential encryption key (`KILASFLOW_ENCRYPTION_KEY` unless
`security.encryption_key_env` names another variable) seals every stored
credential and every value a trigger's registration captured, such as the
secret a service signs its webhook deliveries with. Replacing it is a
procedure, not a single edit: a server that only has the new key cannot open
anything the old key sealed.

## What the key protects, and how

- Each payload is sealed with AES-256-GCM under a key **derived** from the
  master key with HKDF-SHA256. The master key itself never seals anything.
- Each payload starts with an 8-byte header: a format marker (`kfs` and a
  version byte) and a 4-byte key id derived from the master key. The key id
  lets a server holding two keys pick the right one. It says nothing about the
  key.
- Each payload is bound to the row it belongs to. A credential is bound to its
  tenant, id and type, and a captured trigger value to its tenant and webhook
  route. A payload copied into another row does not open there.
- The Google Connect state and its PKCE verifier are signed with another key
  derived from the master key, never with the master key itself.

Payloads written before this format existed have no header and no binding.
They still open: the server reads both formats. Because they carry no binding,
someone who can write to the database could copy one of them into another row
and it would open there. The reseal after upgrading (see below) upgrades every
payload the database holds. It does not reach a copy of an old payload taken
before it ran — a dump, a backup, a replica that lagged — and such a copy still
opens in any row for as long as its key opens legacy payloads. Two things close
that for good, and the second is the stronger:

- Set `security.legacy_seals: refuse` once a reseal reports nothing left to
  reseal. No legacy payload opens again, under any key.
- Rotate to a new key after the upgrade reseal. Old copies were sealed under
  the old key, so once it leaves the keyring they open nowhere. Do this where
  dumps or backups of the database have been handed to anyone who should not
  be able to move a credential between rows.

## The two settings

| Setting | Default variable | What it does |
| --- | --- | --- |
| `security.encryption_key_env` | `KILASFLOW_ENCRYPTION_KEY` | The **primary** key. Every new seal uses it, and it opens what it sealed. |
| `security.secondary_encryption_key_env` | `KILASFLOW_ENCRYPTION_KEY_SECONDARY` | The **secondary** key, set only during a rotation. It opens what it sealed and never seals anything. |
| `security.legacy_seals` | — (`open` or `refuse`; default `open`) | Whether a payload from before this format opens at all. `refuse` turns them away under every key; v1 payloads are not affected. |

The secondary key is always read from the environment, even when the primary
comes from a secrets manager (`secrets.manager_addr`). A secondary variable
that holds something other than a key refuses the boot, so a typo cannot
silently leave the old rows unreadable. Both keys are accepted as base64, hex
or a raw 32-character string. A raw string is logged as a warning at boot:
generate keys with `openssl rand -base64 32`.

`kilasflow -reseal-credentials` connects to the database, applies any pending
migrations, and seals again every payload that is not in the current format
under the primary key, across every tenant. Then it exits. It logs two lines,
one for credentials and one for captured trigger values, each counting the
rows it `resealed`, the rows already `current`, the rows `skipped` because a
request changed them during the pass, and the rows that were `unopenable`.

- Each write only lands if the row still holds what the pass read, so running
  it while servers are serving is safe. A row a request updated in the
  meantime is already sealed under the primary key.
- It does not change `updated_at`. A reseal changes how a credential is
  stored, not what it is.
- It exits non-zero, naming each row, when a payload could not be opened with
  the configured keys. That means a key that sealed it is missing. Do not
  remove the secondary key until a run reports no unopenable row.

## After upgrading to this release

Every credential stored by an earlier release is in the old format. Once every
replica runs the new release, run the reseal once with the same key:

```sh
kilasflow -config config.yaml -reseal-credentials
```

Run it with the same configuration and environment as the server. In a
container, that is the server image with the flag appended. A second run
reports `resealed=0`.

Then refuse legacy payloads, so a pre-upgrade copy of one can never be moved
into a row and opened:

1. Run the reseal again and check both lines report `resealed=0` and
   `unopenable=0`.
2. Set `security.legacy_seals: refuse` (`KILASFLOW_SECURITY_LEGACY_SEALS=refuse`)
   on every replica and restart them.

With `refuse` set, a later reseal reports any legacy payload it meets — one
restored from an old backup, say — as `unopenable`, and says to set it back to
`open` for that reseal. Where old dumps or backups have left your control,
also [rotate the key](#rotating-the-key-one-process) after this, so that
nothing sealed under the pre-upgrade key opens anywhere.

A binary from before this release cannot open the new format. Upgrade every
replica together, and treat the backup as the rollback plan
([Backups and upgrades](/operate/upgrades/#upgrading)).

## Rotating the key: one process

1. Back up the database.
2. Generate the new key: `openssl rand -base64 32`.
3. Restart the server with the new key as the primary and the old key as the
   secondary:

   ```sh
   KILASFLOW_ENCRYPTION_KEY="<new key>"
   KILASFLOW_ENCRYPTION_KEY_SECONDARY="<old key>"
   ```

4. Run `kilasflow -reseal-credentials` with that same environment. It must
   report `unopenable=0` on both lines.
5. Run it again. It must report `resealed=0`.
6. Unset `KILASFLOW_ENCRYPTION_KEY_SECONDARY` and restart.

## Rotating the key: several replicas

Replicas restart one at a time, so for a while some run the old configuration
and some the new. Every replica must be able to open what any other replica
seals at every step:

1. Roll out **primary = old key, secondary = new key**. Nothing is sealed with
   the new key yet, but every replica can now open it.
2. Roll out **primary = new key, secondary = old key**. New writes use the new
   key, and replicas still on step 1 can open them.
3. Run `kilasflow -reseal-credentials` once, from any one process with the
   step 2 configuration, until it reports `unopenable=0` and then
   `resealed=0`.
4. Roll out with the secondary removed.

Skipping step 1 means a replica still on the old key cannot read a credential
that an upgraded replica just saved.

## What a rotation interrupts

- **Google Connect popups in flight.** The Connect state is signed with a key
  derived from the primary key. A popup that was opened before the primary
  changed fails when it returns, and the user presses Connect again. Stored
  Google tokens are not affected. The same happens once at the upgrade that
  introduced the derived signing key.
- **Nothing else.** Credentials and captured trigger values keep opening
  throughout, as long as the secondary key stays configured until the reseal
  has finished.

## Losing a key

There is no recovery without the key. A payload sealed under a key that no
longer exists cannot be opened by anyone, including KilasFlow. Keep the old key
until the reseal reports no unopenable row, and keep backups encrypted under a
separate key from the one they contain.
