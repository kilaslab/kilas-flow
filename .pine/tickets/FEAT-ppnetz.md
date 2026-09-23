---
id: FEAT-ppnetz
title: 'Model providers beyond OpenAI-compatible: Anthropic, Azure OpenAI, Ollama/Gemini embeddings'
status: todo
priority: medium
labels:
    - n8n
    - parity
    - ai
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
---

# Description

Anthropic appears in 3% of templates. Local Ollama works only through the generic OpenAI-compatible node, and only with an invented bearer token.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: lead, node-gap; finding ids: NG-11, LEAD-5). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## NG-11: Model providers other than the OpenAI-compatible ones are missing: Anthropic, Azure OpenAI, and non-OpenAI embeddings

*gap · medium · ai-agent*

**n8n:** Anthropic Chat Model appears in 30 templates (3.0%; 3.8% of the newest) and is #9 on n8n.io/integrations. Gemini Embeddings: 11. Ollama Embeddings: 4. Azure OpenAI: 1. The cluster appears in 4.4% of templates.

**Steps to reproduce:**

1. Import Manual → AI Agent ← Anthropic Chat Model v1.6 (template 19662: `model` locator, `options`).
2. Do the same for Azure OpenAI.
3. Attach Ollama or Gemini embeddings to a PGVector store.

**Actual:**

- All are blocking placeholders. There is no `anthropicApi` credential type and no Anthropic adapter; `grep -ri anthropic internal/ai nodes` finds nothing.
- Meanwhile Gemini, Groq, Mistral, DeepSeek, xAI and Ollama chat models *do* map onto `kilasflow.chatModel`, and Ollama chat maps while Ollama *embeddings* does not.

**Expected:**

Anthropic (native Messages API, including tool use) and Azure OpenAI (deployment + api-version) chat models, plus Ollama and Gemini embeddings, import and run.

**Suggested fix:**

Add a native Anthropic adapter behind `ai.AgentRuntime`, with an `anthropicApi` credential. As a stop-gap, Anthropic's OpenAI-SDK compatibility endpoint (`https://api.anthropic.com/v1/`) could back a `chatModel` mapping, but Anthropic documents it as not production-grade. Map `embeddingsOllama` onto `kilasflow.embeddings` with the Ollama `/v1` base URL, the same way `lmChatOllama` is mapped already.

**Evidence:**

`probes/Anthropic_Chat_Model.json`, `results-ai.json` (Azure OpenAI Chat Model, Ollama Embeddings, Gemini Embeddings).

**Related:**

FEAT-gcq50s (non-OpenAI embeddings)


## LEAD-5: Local Ollama needs a fake bearer token: an empty token is refused, and there is no Ollama credential/node

*ux · medium · ai-agent / credentials*

**n8n:** A dedicated "Ollama Chat Model" node and an "Ollama API" credential that holds only a base URL (default http://localhost:11434).

**Steps to reproduce:**

POST /api/v1/credentials {"type":"httpBearerAuth","fields":{"token":""}}

**Actual:**

`422 credential field "token" is required`. kilasflow.chatModel requires an httpBearerAuth credential, so a user must invent a token such as "ollama". Project memory even says to "use a credential holding an EMPTY token", which the API refuses.

**Expected:**

The chat model's credential is optional when the base URL is loopback/private, or there's an Ollama preset/credential type; n8n Ollama nodes are imported to it.

**Suggested fix:**

Make the credential optional for kilasflow.chatModel and add an Ollama preset (base URL + model list from /api/tags). Map @n8n/n8n-nodes-langchain.lmChatOllama and embeddingsOllama on import.

**Related:**

FEAT-kwxxd0 (done) introduced the Ollama path


# Also found by the audit

## AI-10: Ollama is hard to find and half-supported: the picker finds nothing, the model list needs a credential the node does not need, and imports flag a credential as blocking

*ux · medium · onboarding*

**n8n:** A dedicated "Ollama Chat Model" node, an "Ollama API" credential (base URL default `http://localhost:11434`), and an "Embeddings Ollama" node. Typing "ollama" in the node panel finds them.

**Steps to reproduce:**

1. On a workflow, click Add step and type `ollama`.
2. Add "OpenAI-Compatible Chat Model", set Base URL `http://127.0.0.1:11434/v1`, leave the credential as None.
3. Import an n8n workflow containing `lmChatOllama` (`n8n-ollama-agent.json`).
4. Run an Embeddings node pointed at Ollama with no credential.

**Actual:**

- Step 1: "No registered node matches "ollama"." The only route is the "OpenAI-Compatible Chat Model", which defaults to `https://api.openai.com/v1` and `gpt-4o-mini`.
- Step 2: the run works with no credential (case 1). The Model dropdown, however, is a plain select that shows "attach a credential to load this field's options" (`load-options` returns `[]`), and there is no "By name" mode.
- Step 3: the import report marks a *blocking* `credentials` issue ("attach a local credential before activating") that Ollama does not need.
- Step 4: the Embeddings node fails at run time with `an openAiApi, openRouterApi or httpBearerAuth credential holding the API key is required`, although validation passed.

**Expected:**

An Ollama preset or node (keyword "ollama"), with a model list loaded from `/models` (or `/api/tags`) without a credential for a credential-less endpoint. A non-blocking import note. Embeddings follow the chat model's no-credential rule.

**Suggested fix:**

Add node-search keywords/aliases ("ollama", "local", "lm studio", "vllm") and an Ollama preset. Let the loader run without a credential when none is attached. Downgrade the import issue to info when the base URL is loopback.

**Evidence:**

`case11-07-picker-ollama.png`, `case1-ndv-chatmodel-nocred.png`, `case-import.response.json`, `case-12b.execution.json`, `case-1.execution.json` (credentialId ""). Code `nodes/ai.go:337-372` (the loader requires `httpBearerAuth`).

**Related:**

LEAD-5 (this audit). Note that its "requires an httpBearerAuth credential" is not true at run time: the node runs without one, and only the model picker and the embeddings node demand it. TPL-11.


# Acceptance Criteria
- [ ] A native Anthropic chat model (Messages API with tool use) and an `anthropicApi` credential
- [ ] Azure OpenAI chat model (deployment + api-version)
- [ ] `embeddingsOllama` and Gemini embeddings map onto `kilasflow.embeddings`
- [ ] A local Ollama needs no fake token: the credential is optional for a loopback/private base URL, or there is an Ollama preset with the model list from `/api/tags`
- [ ] Searching "ollama" in the node picker finds the OpenAI-compatible model (alias) or a dedicated Ollama preset
- [ ] The model list loads from `/models` (or `/api/tags`) without a credential for a credential-less endpoint, and imported n8n Ollama nodes get a non-blocking note instead of a blocking credential issue

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-gcq50s, FEAT-kwxxd0

# Related Files

# Attachments
