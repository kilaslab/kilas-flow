---
id: BUG-9dw5me
title: Basic LLM Chain ignores image input (vision only works on the Agent); vision toggle is mis-described
status: done
priority: low
labels:
    - ai
    - vision
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-10-08T16:26:00Z"
---

# Description

BUG-tcqkad fixed agent vision but left chain image rows.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ai-ollama; finding ids: AI-17). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The Basic LLM Chain accepts "Image (Binary)" and "Image (URL)" prompt rows. The Agent's "Automatically Passthrough Binary Images" sends input images to the model.

# Steps to Reproduce

1. HTTP Request (responseFormat file, a 128×128 red-circle PNG) → Basic LLM Chain "Describe the shape and colour in this image".
2. Repeat with an AI Agent.

# Expected

The chain sends image rows or binary images like the agent does. The toggle is described as "send input images to the model".

# Actual

The chain sends a text-only message, and gemma4 replies "Please provide the image you are referring to…". The Agent sends `[text, image_url(data:image/png;base64…)]` and answers "The image shows a pink circle." (PASS). The Agent's `passthroughBinaryImages` description reads "Carry the incoming item's binary attachments through to the output item.", which does not say it is the switch that sends images to the model. The import drops chain image rows as reported.

# Acceptance Criteria
- [x] The chain sends image rows and binary images the same way the agent does
- [x] The toggle reads "send input images to the model"

# Implementation Plan

Reuse `inputImages` in `ChainExecutor`, add image row types to `messages`, and reword the description.

# Notes

Related tickets: BUG-tcqkad

Related (from the audit): BUG-tcqkad (done; fixed agent vision, left chain image rows)

# Related Files

`case-10c.execution.json`, `case-10b.execution.json`, `proxy-log.jsonl`. Code `nodes/ai.go:553`, `internal/interop/n8n/parameters.go:4648-4655`.

# Attachments

## Fix

`nodes/ai.go`:

- The Basic LLM Chain has the agent's `passthroughBinaryImages` toggle (default on). When on, the item's image attachments go with the final user turn through the agent's `inputImages` path: the same data URIs, the same 4-image and 8 MiB bounds, and non-images skipped. When the rows end on a system or AI turn, the images go in a user turn of their own.
- Human prompt rows have n8n's `messageType` (`text`, `imageBinary`, `imageUrl`), with `binaryImageDataKey` (default `data`) and `imageUrl`. Each image row is a user turn holding one picture. An Image (Binary) row whose binary is missing, is not an image, or is over the bound fails the item and names the binary. The passthrough does not send that binary again. An Image (URL) row must be http(s) or `data:image/`. As in n8n, only a human row reads `messageType`.
- Reading one stored image into a data URI is now `imageDataURI`, which the agent and the chain share.
- The agent's toggle now reads "Send input images to the model: …". The chain's toggle reads the same.

Tests (`nodes/ai_test.go`): `TestChainSendsTheItemsImagesToTheModel` (on and off), `TestChainSendsImageRows`, `TestChainImageRowNamesTheMissingBinary`, `TestVisionToggleSaysItSendsImagesToTheModel`. `go test ./nodes/ ./internal/ai/...`, `./internal/interop/...` and `./internal/node/...` pass, and `go vet` is clean.

Not done here (interop, out of this ticket's scope): `chainMessagesToKilas` in `internal/interop/n8n/parameters.go` still drops n8n image rows on import with a "does not implement" note. It could now carry `messageType`, `binaryImageDataKey` and `imageUrl` verbatim. `chainMessagesToN8N` writes only `type` and `message` back, so an exported image row becomes an empty text row. The chain's `passthroughBinaryImages` has no n8n equivalent and is dropped on export.
