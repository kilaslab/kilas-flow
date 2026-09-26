---
id: BUG-n4e97f
title: Boot warns that KILASFLOW_ENCRYPTION_KEY and KILASFLOW_EMBED_SIGNING_KEY were ignored although both are used
status: done
priority: low
labels:
    - config
    - logging
    - credentials
parent: EPIC-brpz48
created: "2026-09-25T13:53:44Z"
updated: "2026-09-26T16:47:43Z"
---

# Description

`KILASFLOW_ENCRYPTION_KEY` and `KILASFLOW_EMBED_SIGNING_KEY` are the default *secret-holding* environment variables, named by `credentials.encryption_key_env` and `embed.signing_key_env`. The key reader uses them directly. The strict environment-to-config mapper also sees them and warns at every boot:

    WARN configuration key matches nothing and was ignored key=encryption.key source=environment did_you_mean=execution
    WARN configuration key matches nothing and was ignored key=embed.signing_key source=environment did_you_mean=embed.signing_key_env

Both keys are in fact used; credentials encrypt and embed sessions sign. An operator reading the log is told the one secret that matters was ignored, and `did_you_mean=execution` points the wrong way. Seen on a lab boot, 2026-09-25.

# Acceptance Criteria
- [x] The mapper does not warn about an environment variable that a `*_env` setting names; the current value of that setting is used, so a renamed variable is covered too.
- [x] A test covers both defaults and a renamed variable.

## Fix (2026-09-26)

The unknown-key warning now leaves alone every variable the process reads itself: the current value of each `*_env` setting, the `KILASFLOW_WORKFLOW_ENV_` prefix, and the CLI's `KILASFLOW_URL` and `KILASFLOW_TOKEN`. The `*_env` settings are found by walking the config struct's koanf tags (`variablesNamedByConfig`), so a renamed variable is covered, the default name is covered while the setting is untouched, and a `*_env` field added later needs no list edit. A variable a setting used to name and no longer does is still reported, because it is no longer read. The setting is `security.encryption_key_env`, not `credentials.encryption_key_env` as the description says.

Covered by the same change, at no extra cost: `KILASFLOW_AUTH_SIGNING_KEY`, `KILASFLOW_BOOTSTRAP_PASSWORD`, `KILASFLOW_AUTH_OPERATOR_KEY` and `KILASFLOW_GOOGLE_CLIENT_SECRET` had the same false warning. The warning also names the variable, says `source=environment` for a variable (the file's name only for a key from the file), and offers `did_you_mean` only within an edit distance of 3.

Tests in `internal/config/boot_strictness_test.go`: the documented defaults set together, a renamed variable (and the old default then warning), a walk over every `*_env` field in the struct, and a misspelled variable that still warns. Removing the exemption makes five of them fail. A boot of the built binary with both keys set logged no `matches nothing` line for either, and still warned for a misspelled variable.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-26. The generated section diffed from the ticket's creation commit and listed sixty unrelated files, so it is replaced by the fixing commit alone.

- Commits (1):
  - `48caf19b` — BUG-n4e97f: a variable a *_env setting names is no longer reported at boot as an ignored key
- Files changed (`git show --stat 48caf19b`):

```
 .pine/tickets/BUG-66fhea.md                    |  12 +-
 .pine/tickets/BUG-n4e97f.md                    |  12 +-
 docs/src/content/docs/operate/configuration.md |   9 ++
 internal/config/boot_strictness_test.go        | 188 +++++++++++++++++++++++++
 internal/config/config.go                      | 147 ++++++++++++++++---
 5 files changed, 341 insertions(+), 27 deletions(-)
```
