---
id: FEAT-gcq50s
title: Drive/Gmail full parity and remaining RAG cluster modes
status: todo
priority: low
labels:
    - parity
parent: EPIC-8rbys7
created: "2026-09-21T12:03:36Z"
updated: "2026-10-09T11:01:23Z"
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
OCR, Pinecone, Q&A Chain retriever and tenant `CREATE EXTENSION` stay out of scope. Google Sheets is no longer out of scope: FEAT-vntngh owns it. The vector-store retrieve mode, Vector Store Tool and the in-memory store are owned by FEAT-zm3wh2.

Re-parented (2026-10-09) from EPIC-hkypt5 to the parity backlog EPIC-8rbys7: it is a low-priority list of remaining Drive/Gmail/RAG surface, to be split into its own tickets when someone needs an area, and it no longer holds the RAG-templates epic open.

# Related Files

# Attachments
