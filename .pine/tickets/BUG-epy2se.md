---
id: BUG-epy2se
title: 'Credential tests: untestable types shown as ''Test failed''; OpenAI has no Base URL; Telegram baseUrl required, no probe'
status: todo
priority: medium
labels:
    - credentials
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

Every credential type without a probe shows a red failure. An OpenAI-compatible (Ollama) credential can never test green. The Telegram credential API refuses to save without the base URL, which the docs say to leave empty.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: lead, ux-ops; finding ids: OPS-7, OPS-8, LEAD-6). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## OPS-7: Credential "Test" shows a red "Test failed" for every type that has no probe

*bug · medium · credentials*

**n8n:** Credential types without a test show no Test button, and a saved credential shows "Connection tested successfully" or a specific error.

**Steps to reproduce:**

1. /credentials → New credential → HTTP Header Auth (or Telegram, Basic, Bearer, Query, Custom, JWT). 2. Fill valid values and click Test.

**Actual:**

The red status reads "Test failed — this credential type has no test defined". The API answers `{"ok":false,"detail":"this credential type has no test defined","untestable":true}`, but the page ignores `untestable`. Telegram is untestable, although a getMe probe is trivial.

**Expected:**

Hide Test (or show a neutral "This type can't be tested") when untestable is true. Add a probe for Telegram (GET {baseUrl}/bot{token}/getMe).

**Suggested fix:**

Add `untestable` to testResult with a neutral rendering, expose `testable` on GET /credential-types, and give telegramApi a TestRequest.

**Evidence:**

agents/ux-ops/12-cred-header-test.png; web/src/routes/(dashboard)/credentials/+page.svelte:282-296 (`testResult = { ok: response.data.ok, … }`); internal/api/handlers/credentials.go:583-587.

**Related:**

none


## OPS-8: The OpenAI credential has no Base URL, so an OpenAI-compatible endpoint (Ollama) always fails its test, and the node carries the URL instead

*gap · medium · credentials / ai*

**n8n:** The OpenAI credential has "API Key", "Organization ID" and "Base URL" (default https://api.openai.com/v1), and its test calls {baseUrl}/models, so a local Ollama credential tests green.

**Steps to reproduce:**

1. New credential → OpenAI, name "[ux-ops] Ollama via OpenAI", API key "ollama". 2. Click Test.

**Actual:**

"Test failed — the service answered 401". The probe is hard-coded to https://api.openai.com/v1/models. The type's only field is `apiKey`, described as "A key from platform.openai.com, in the form sk-…", and the base URL lives on the lmChatOpenAi node instead. An n8n export whose OpenAI credential pointed at a gateway loses that URL, and every Ollama user sees a failed test.

**Expected:**

A Base URL field (default https://api.openai.com/v1) that the probe and the nodes use unless the node overrides it.

**Suggested fix:**

Add optional `url` or `baseUrl` to openAiApi (n8n's key is `url`), template it into the TestRequest, and fall back to it when the node's baseURL is empty.

**Evidence:**

agents/ux-ops/14-cred-openai-ollama-test.png; internal/credentials/builtin.go:224 (`Test: &TestRequest{URL: "https://api.openai.com/v1/models"}`); node-types.json kilasflow.lmChatOpenAi baseURL parameter.

**Related:**

none


## LEAD-6: telegramApi credential refuses to save without baseUrl, while the docs say to leave it empty

*bug · medium · credentials*

**Steps to reproduce:**

POST /api/v1/credentials {"name":"x","type":"telegramApi","fields":{"accessToken":"123:abc"}}

**Actual:**

`422 credential field "baseUrl" is required`

**Expected:**

baseUrl is optional and defaults to https://api.telegram.org. The README said, and concepts/webhooks.md now says: "Its Base URL field is normally empty; set it only if you run Telegram's own local Bot API server."

**Suggested fix:**

Default baseUrl in the credential type (or in the pack's baseURL expression), and mark it not required.

**Evidence:**

packs/telegram/pack.json:13 uses `{{ $credentials.baseUrl }}` with no default


# Acceptance Criteria
- [ ] When the API reports `untestable: true`, the UI hides Test or shows a neutral message, and GET /credential-types exposes `testable`
- [ ] `openAiApi` has an optional Base URL (n8n key `url`, default https://api.openai.com/v1) used by the probe and as the nodes' fallback
- [ ] `telegramApi` baseUrl is optional and defaults to https://api.telegram.org, and Telegram has a getMe probe

# Implementation Plan

See each finding's suggested fix above.

# Notes

# Related Files

# Attachments
