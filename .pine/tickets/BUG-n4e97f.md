---
id: BUG-n4e97f
title: Boot warns that KILASFLOW_ENCRYPTION_KEY and KILASFLOW_EMBED_SIGNING_KEY were ignored although both are used
status: todo
priority: low
labels:
    - config
    - logging
    - credentials
parent: EPIC-brpz48
created: "2026-09-25T13:53:44Z"
updated: "2026-09-25T13:53:44Z"
---

# Description

`KILASFLOW_ENCRYPTION_KEY` and `KILASFLOW_EMBED_SIGNING_KEY` are the default *secret-holding* environment variables, named by `credentials.encryption_key_env` and `embed.signing_key_env`. The key reader uses them directly. The strict environment-to-config mapper also sees them and warns at every boot:

    WARN configuration key matches nothing and was ignored key=encryption.key source=environment did_you_mean=execution
    WARN configuration key matches nothing and was ignored key=embed.signing_key source=environment did_you_mean=embed.signing_key_env

Both keys are in fact used; credentials encrypt and embed sessions sign. An operator reading the log is told the one secret that matters was ignored, and `did_you_mean=execution` points the wrong way. Seen on a lab boot, 2026-09-25.

# Acceptance Criteria
- [ ] The mapper does not warn about an environment variable that a `*_env` setting names; the current value of that setting is used, so a renamed variable is covered too.
- [ ] A test covers both defaults and a renamed variable.
