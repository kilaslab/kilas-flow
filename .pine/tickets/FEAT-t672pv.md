---
id: FEAT-t672pv
title: OpenAI and Google Gemini app nodes (message, analyze image, transcribe)
status: todo
priority: high
labels:
    - n8n
    - parity
    - ai
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
---

# Description

`@n8n/n8n-nodes-langchain.openAi` appears in 11.7% of templates and is the #4 unlock. The model plumbing it needs already exists (`internal/ai/openai.go`).

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-4). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** - `@n8n/n8n-nodes-langchain.openAi` (Message a model, Analyze image, Generate image, Transcribe audio…) appears in 117/998 templates (11.7%). n8n Pulse ranks it #17 (~668K deployments).
- `@n8n/n8n-nodes-langchain.googleGemini` appears in 22 templates (all from the newest sample), and the legacy `n8n-nodes-base.openAi` in 17.
- The cluster together appears in 16.1% of templates.

# Steps to Reproduce

1. Import Manual → OpenAI v1.5 (template 2525: `modelId` locator, `messages.values`, `options`).
2. Import Manual → Google Gemini v1.2 (template 19088: `modelId`, `messages`, `jsonOutput`, `builtInTools`).

# Expected

At least `resource: text, operation: message` (chat completion with optional JSON output), `image: analyze` (vision) and `audio: transcribe` map onto the existing OpenAI-compatible adapter (`kilasflow.chainLlm` / `internal/ai/openai.go`). Image generation can come later.

# Actual

Both import as blocking placeholders ("KilasFlow has no equivalent of the n8n node \"@n8n/n8n-nodes-langchain.openAi\"…"). The OpenAI app node is the #4 unlock (+33 templates), and it is the sole blocker in 13 of the most-viewed templates.

# Acceptance Criteria
- [ ] `openAi` resource text/message (with JSON output), image/analyze and audio/transcribe import and run
- [ ] The Gemini app node's text operation runs through Gemini's OpenAI-compatible endpoint
- [ ] Template 2525 imports with no blocking issue

# Implementation Plan

Add a `kilasflow.openAi` node (or map the text/message operation onto chainLlm plus an lmChatOpenAi sub-node). The Gemini app node's text operation can reuse the Gemini OpenAI-compat base URL, like the chat model does.

# Notes

Related tickets: FEAT-j5s2n4

Related (from the audit): FEAT-j5s2n4 (done) listed "the OpenAI app node's message/image operations over the existing OpenAI…" as fix step 3, but it was never delivered.

# Related Files

`probes/OpenAI_app_node.json`, `probes/Google_Gemini_app_node.json`, `coverage.json` (single-blocker list).

# Attachments
