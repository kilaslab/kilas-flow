---
id: FEAT-gcq50s
title: Drive/Gmail full parity and remaining RAG cluster modes
status: todo
priority: low
labels:
    - parity
parent: EPIC-hkypt5
created: "2026-09-21T12:03:36Z"
updated: "2026-09-21T13:30:00Z"
---

# Description

Work deliberately left out of the first RAG/Google slice. The shipped subset is enough for the four template *shapes* (Search/Download/Move, Gmail Trigger/Get, cluster insert + retrieve-as-tool). This ticket tracks the rest of n8n's Drive/Gmail/RAG surface.

# Acceptance Criteria
- [ ] Google Shared Drives (corpora, driveId) on Drive nodes and trigger
- [ ] Remaining Drive trigger events beyond file created/updated in a folder
- [ ] Gmail drafts, labels, reply, threads, attachment download/upload
- [ ] Additional text splitters (character, token)
- [ ] Vector Store rerank / retrieve-for-chain; in-memory and Supabase stores
- [ ] n8n embeddings types that are not OpenAI-compatible `/v1/embeddings` (Cohere, Gemini, Azure, HuggingFace, Bedrock, Vertex) except behind an OpenAI-compatible gateway

# Implementation Plan
Separate tickets per area when someone needs them. Do not block 16706/3647/4799/5908 shapes on this list.

# Notes
OCR, Pinecone, Q&A Chain retriever, Google Sheets, and tenant `CREATE EXTENSION` stay out of scope.

# Related Files

# Attachments
