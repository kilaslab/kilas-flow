---
id: BUG-66fhea
title: Boot warns KILASFLOW_ENCRYPTION_KEY (and other secret/env vars) 'matches nothing and was ignored' although used
status: todo
priority: medium
labels:
    - config
    - boot
    - regression
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

The very first log line a careful operator sees says their encryption key was ignored. It wasn't. The warning came with the BUG-y57cz4 fix, and it contradicts install.md's promise of "two warnings".

Found by the 2026-09-23 n8n-parity and UX audit (source reports: lead, ux-ops; finding ids: OPS-6, LEAD-1). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## OPS-6: Boot warns that KILASFLOW_ENCRYPTION_KEY (and the other secret env vars) "matches nothing and was ignored" even though it is read and used

*bug · medium · config / boot*

**Steps to reproduce:**

1. Start the server as documented with KILASFLOW_ENCRYPTION_KEY set (I reproduced on a throwaway instance on :18999 in agents/ux-ops/boot/). 2. Also set KILASFLOW_EMBED_SIGNING_KEY, KILASFLOW_WORKFLOW_ENV_GREETING or KILASFLOW_URL. 3. Read the first log lines.

**Actual:**

`2026/09/23 07:50:20 WARN configuration key matches nothing and was ignored key=encryption.key source="config.yaml (not found)" did_you_mean=execution`, although credentials encrypt fine. The same false warning appears for `key=embed.signing_key … did_you_mean=embed.signing_key_env` (the embed key is actually applied: the "embed signing key is not set" warning disappears), for `key=workflow.env_greeting did_you_mean=idempotency.retention` (the documented $env allowlist) and for `key=url did_you_mean=sql` (the CLI's KILASFLOW_URL). There are three more defects in the same line. The source says "config.yaml (not found)" when the value came from the environment. The line uses Go's default log format (`2026/09/23 … WARN`) while every other line is `time=… level=…`. And the "starting kilasflow" line says `config=config.yaml` for a file that doesn't exist. docs/src/content/docs/start/install.md:78-96 promises "Two warnings on a correctly configured first run", so a careful operator reads this as "my encryption key was ignored", or renames the embed key to the suggested but wrong variable.

**Expected:**

No warning for the KILASFLOW_* variables the binary reads directly. The warning names the variable and "environment" as the source, uses the configured handler, and gives a did_you_mean only when it is close.

**Suggested fix:**

Build an exemption set from every `*KeyEnv` value (security.encryption_key_env, auth.signing_key_env, embed.signing_key_env, the operator key), the KILASFLOW_WORKFLOW_ENV_ prefix and the CLI's URL/TOKEN. Tag each key with its provider, and only suggest a key within an edit distance of about 3. Add a boot_strictness test that sets KILASFLOW_ENCRYPTION_KEY and expects no warning.

**Evidence:**

kf-server.log line 1; agents/ux-ops/boot/boot.log, agents/ux-ops/boot/boot2.log. Root cause: internal/config/config.go:1006 loads every KILASFLOW_* variable through `env.ProviderWithValue`, and :1424 `envKeyToPath` maps KILASFLOW_ENCRYPTION_KEY to `encryption.key`. No field has that path, because the key is read out-of-band by credentials.KeyFromEnvironment(cfg.Security.EncryptionKeyEnv) (internal/credentials/credentials.go:273, cmd/kilasflow/main.go:1338). warnUnknownKeys (:1074) never exempts variables named by *_key_env fields or the KILASFLOW_WORKFLOW_ENV_ prefix. configSource (:1109-1116) reports the file even for env keys. nearestKey (:1122) always returns something, however far away.

**Related:**

BUG-y57cz4 (done). The unknown-key warning its fix added now produces these false positives.


## LEAD-1: Documented KILASFLOW_ENCRYPTION_KEY triggers "configuration key matches nothing and was ignored"

*bug · medium · onboarding*

**Steps to reproduce:**

1. Start the binary with KILASFLOW_ENCRYPTION_KEY set, exactly as README/.env.example instruct. 2. Read the first log line.

**Actual:**

`WARN configuration key matches nothing and was ignored key=encryption.key source="config.yaml (not found)" did_you_mean=execution`. Credentials nevertheless work, because security.encryption_key_env reads the variable directly.

**Expected:**

No warning for a variable the product itself documents. The env-to-config mapper should skip the variable names that *_env keys point at: KILASFLOW_ENCRYPTION_KEY, KILASFLOW_AUTH_SIGNING_KEY, KILASFLOW_EMBED_SIGNING_KEY, KILASFLOW_BOOTSTRAP_PASSWORD, KILASFLOW_AUTH_OPERATOR_KEY.

**Suggested fix:**

Before warning, have the env loader exclude every variable named by a `*_env` config value. Add a test that boots with each documented secret variable and asserts no warning.

**Evidence:**

scratchpad kf-server.log, line 1

**Related:**

none (the ux-ops agent was asked to confirm the root cause)


# Acceptance Criteria
- [ ] No warning for any KILASFLOW_* variable named by a `*_key_env` field, the KILASFLOW_WORKFLOW_ENV_ prefix, or the CLI's KILASFLOW_URL/TOKEN
- [ ] The warning names the variable, says the source is the environment, and uses the configured slog handler
- [ ] `did_you_mean` appears only within a small edit distance
- [ ] A boot test sets each documented secret variable and asserts no unknown-key warning

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-y57cz4

# Related Files

# Attachments
