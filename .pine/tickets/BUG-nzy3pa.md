---
id: BUG-nzy3pa
title: "Chat/embedding model names dropped on import (Gemini modelName, n8n defaults) and lost on export; provider options dropped silently"
status: todo
priority: high
labels:
    - n8n
    - importer
    - ai
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
---

# Description

n8n's own "Build your first AI agent" template (6270) imports and then fails with `model is required`, because the importer reads `model` where n8n writes `modelName`. Ollama, Groq and Gemini options are dropped without an import issue.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-5). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The Google Gemini Chat Model keeps its model in `modelName` (e.g. `models/gemini-1.5-pro-latest`). It appears in 72/998 templates (7.2%), including n8n's own "Build your first AI agent" template (6270). In all 104 sampled instances the key is `modelName` (91) or absent, which means n8n's default (13). None uses `model`.

# Steps to Reproduce

1. Import Manual → AI Agent ← Gemini Chat Model, using template 2421 (`modelName: "models/gemini-1.5-pro-latest"`, `options.safetySettings`).
2. `kilasflow run <id> --wait`.
3. Import template 6270 whole, where Gemini's `options: {temperature: 0}` carries no modelName.
4. Import an Ollama Chat Model (templates 18825 and 19358) and a Groq Chat Model (19222).

# Expected

Gemini reads `modelName` (falling back to n8n's default), stripping the `models/` prefix if the OpenAI-compat endpoint needs it. Every option that cannot be carried produces a `dropped` issue, instead of the import reporting only the credential note.

# Actual

- Gemini arrives as `kilasflow.chatModel {"baseUrl":"https://generativelanguage.googleapis.com/v1beta/openai","model":{"__rl":true,"mode":"list","value":""}}`. The only issue raised is the generic lossy "this model's endpoint came from an n8n credential…". The run then fails with `node "geminichatmodel-id" configuration is invalid: model is required`. `safetySettings` is dropped with no issue.
- Ollama: `think`, `numCtx`, `keepAlive` and `numBatch` are dropped with no issue. Only `numPredict` becomes `maxTokens`.
- Groq: `maxTokensToSample: 4096` is dropped with no issue.

# Also found by the audit

## TPL-3: Chat-model and embedding model names are silently dropped (Gemini `modelName`, n8n defaults not materialised), so `validate` fails with "model is required"

*bug · high · importer*

**n8n:** - Google Gemini Chat Model stores the model in `modelName`, with default `models/gemini-2.5-flash` (n8n `LmChatGoogleGemini.node.ts` l.50/100).
- Ollama Chat Model defaults to `llama3.2` (`LMOllama/description.ts` l.20).
- Embeddings OpenAI defaults to `text-embedding-3-small`.
- n8n omits any parameter left at its default, so templates often carry no model at all.
- In the top 198: 18 templates use Gemini (14 with `modelName`, 4 with the default) and 17 use embeddingsOpenAi with the default model.

**Steps to reproduce:**

1. Run `minimal_repros.py`, cases `gemini_modelName` (`modelName:"models/gemini-2.0-flash"`), `ollama_default_model` (`parameters:{options:{}}`) and `embeddings_default_model`.
2. Import templates 2753, 2466, 6270, 2384, 1960 and 2165.

**Actual:**

- Every case imports with `model: {"__rl":true,"mode":"list","value":""}` (chat) or no model (embeddings).
- The only diagnostic is the generic lossy credential note. Nothing says the model was lost.
- `validate` then fails with `node "g" configuration is invalid: model is required`. That is 12 nodes across 7 of the 45 templates: 1960, 2165, 2384, 2466, 2753, 6270, and 2950, a paid template whose parameters are stripped.
- For comparison, `lmChatOpenAi` with no model does get n8n's default (`gpt-5-mini`), so the behaviour is inconsistent.
- After I set the model by hand, 2384 ran end to end on Ollama and answered "PONG", and 6270 ran with its agent (`runs/2384.json`, `runs/6270.json`).

**Expected:**

- Gemini reads `modelName`.
- Every OpenAI-compatible provider and embeddingsOpenAi materialise n8n's per-version default when the key is absent.
- Anything that cannot be carried is reported, instead of surfacing only at validate.

**Suggested fix:**

Give `openAICompatibleModelToKilas` a per-provider model key and default (Gemini: `modelName` → `models/gemini-2.5-flash`; Ollama: `llama3.2`), and give embeddings `text-embedding-3-small` (`text-embedding-ada-002` at v1). Emit a lossy issue whenever a default was materialised.

**Evidence:**

- `internal/interop/n8n/n8n.go:554-558` passes `"model"` for Gemini.
- `parameters.go:4750-4764` (`openAICompatibleModelToKilas`, no default).
- `parameters.go:3682-3699` (`embeddingsOpenAiToKilas`, no default).
- `minimal_repros.json`, `import/results.json` (validate diagnostics).
- n8n sources are in `agents/n8n-templates/n8nsrc/`.

**Related:**

none in Pine. NG-5 in `findings/node-gap.md` covers the Gemini `modelName` half; the Ollama and embeddings defaults, and the inconsistency with lmChatOpenAi, are new here.

## TPL-11: Exporting an imported Gemini, Ollama, Groq, DeepSeek, Mistral or xAI model writes `kilasflow.chatModel`, which n8n cannot load

*bug · medium · importer*

**n8n:** A workflow exported back must re-open in n8n. 26 of the top 198 templates use one of these providers (by template: Gemini 18, Ollama 5 plus 1 legacy lmOllama, Mistral 2, DeepSeek 2, Groq 1, xAI 1; some use two).

**Steps to reproduce:**

1. Import template 2753, or `minimal_repros.py` cases `gemini_modelName` / `ollama_default_model`.
2. `kilasflow workflow export <id>`.

**Actual:**

- The export contains `{"type":"kilasflow.chatModel","typeVersion":1,"parameters":{"baseUrl":"https://generativelanguage.googleapis.com/v1beta/openai","model":{"__rl":true,"mode":"list","value":""}}}`.
- Lossy note: "n8n has no equivalent of the KilasFlow node \"kilasflow.chatModel\"; it was exported under its own type…".
- The original `@n8n/n8n-nodes-langchain.lmChatGoogleGemini`, its `modelName` and its credential name are gone, so the round-tripped workflow no longer opens cleanly in n8n.

**Expected:**

Export restores the provider node. Keep the source type and version (as placeholders already do via their capsule), or map the chatModel back by `baseUrl` host. Either way, the model is restored.

**Suggested fix:**

When importing an OpenAI-compatible provider, record `originalType`/`originalTypeVersion` on the chatModel and restore them on export, writing `model` back under the provider's key (`modelName` for Gemini).

**Evidence:**

- `roundtrip/2753.diff.json`, `roundtrip/repro_gemini_modelName.export.json`, `roundtrip/repro_ollama_default_model.export.json`
- `internal/interop/n8n/n8n.go:546-590` (these mappings are `importOnly: true`)
- `n8n.go:1405-1422` (fallback export under the KilasFlow type)

**Related:**

none


# Also found by the audit

## AI-11: Imported Ollama model: editor says "Stream output: Enabled" and "temperature … does not apply", but the run is non-streaming and does send temperature

*bug · medium · importer*

**n8n:** The imported Ollama node keeps its options, and they show as editable fields that take effect.

**Steps to reproduce:**

1. Import `n8n-ollama-agent.json` (lmChatOllama with `options.temperature 0.2, numPredict 512, keepAlive "10m", numCtx 8192`).
2. Open the "Ollama Chat Model" node.
3. Run an equivalent chatModel with `options:{temperature:0.2,maxTokens:512}` and no `stream` key, through the logging proxy.

**Actual:**

- The NDV shows empty Temperature and Maximum tokens fields, with two notices under Options: "maxTokens is set but does not apply to this operation." and "temperature is set but does not apply to this operation."
- The request actually sent is `{'temperature': 0.2, 'max_tokens': 512}`, with no `stream`.
- The Stream output checkbox shows Enabled while the descriptor says `"stream": false`, because `boolValue(nil)` is false and the declared default is not applied at run time.
- `keepAlive` and `numCtx` are dropped with no import issue.

**Expected:**

Imported options land in the fields that the NDV shows and that the runtime reads. Declared defaults (stream: true) mean the same thing at run time as in the editor. Dropped options are reported.

**Suggested fix:**

Map temperature and maxTokens to the chatModel's top-level fields on import (or declare them in its Options collection). Use `boolOr(parameters, "stream", true)`. Report keepAlive, numCtx and other Ollama-only options as lossy.

**Evidence:**

`import-ollama-ndv.png`, `case-1b.execution.json`, `proxy-log.jsonl` (last completions request of case 1b), `case-import.response.json`. Code `nodes/ai.go:872` (`boolValue(ir.Parameters["stream"])`), `nodes/ai.go:384-400` (the chatModel Options collection declares only timeout and maxRetries), `internal/interop/n8n/parameters.go:4768-4785`.

**Related:**

TPL-3 and NG-5 (model names dropped; different fields)


# Acceptance Criteria
- [ ] Gemini's `modelName` (or n8n's default when it is absent) becomes the chatModel model, with the `models/` prefix handled
- [ ] Every chat-model option that is not carried produces a `dropped` import issue; `maxTokensToSample` maps to `maxTokens`
- [ ] A test imports template 6270 and asserts the model is non-empty and the run gets past validation
- [ ] Ollama (`llama3.2`), embeddingsOpenAi (`text-embedding-3-small`, or `ada-002` at v1) and every OpenAI-compatible provider fill in n8n's per-version default model when the key is absent, with a lossy note (consistent with lmChatOpenAi)
- [ ] Exporting an imported Gemini/Ollama/Groq/DeepSeek/Mistral/xAI model restores the original provider node type, version and model key (`modelName` for Gemini), so the round-tripped workflow opens in n8n
- [ ] Imported chat-model options land in the fields the NDV shows and the runtime reads; `stream` means the same thing in the editor and at run time

# Implementation Plan

Pass `"modelName"` for Gemini, with a default. Map the provider option names (`maxTokensToSample`→`maxTokens`), and emit a `dropped` issue for every leftover key in `options`. Add a test that imports template 6270 and asserts the model is non-empty.

# Notes

**2026-09-23: progress.** The `stream` half of AI-11 is fixed. `modelDescriptorFor` now defaults `stream` to the definition's `true` when the key is absent (`TestAChatModelStreamsByDefaultAsItsDefinitionSays`). The other imported-option fields and the model-name defaults are still open.

Related (from the audit): none

# Related Files

- `probes/Gemini_Chat_Model.json`, `probes/Ollama_Chat_Model.json`, `probes/Groq_Chat_Model.json`, workflow wf_01a0cbd0-0097-7c5f-8bac-e1faa4064c1c (template 6270).
- `internal/interop/n8n/n8n.go:554-559` passes `"model"` as the model field.
- `parameters.go:4750-4783`.
- `chatModelToKilas` (`parameters.go:4935`) keeps only `n8nChatModelOptionKeys` and reports only `responseFormat`.

# Attachments
