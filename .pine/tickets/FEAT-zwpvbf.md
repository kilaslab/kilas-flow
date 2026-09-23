---
id: FEAT-zwpvbf
title: 'LangChain root chains: Information Extractor, Text Classifier, Summarization, Q&A, Sentiment'
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

These chains appear together in 7.4% of templates. Each is a thin layer over the existing `chainLlm` and structured-output plumbing.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-8). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Information Extractor appears in 34 templates, Text Classifier in 19, Summarization Chain in 17, Question & Answer Chain in 9 and Sentiment Analysis in 4. The cluster appears in 7.4% of templates. The Auto-fixing (6) and Item List (2) output parsers are missing too.

# Steps to Reproduce

Import Manual → X ← OpenAI Chat Model for each chain, using real instances:
- informationExtractor, template 19265 (`text`, `attributes`, `options`)
- textClassifier, template 19529 (`inputText`, `categories`)
- chainSummarization, template 3169 (`operationMode`)
- chainRetrievalQa, template 2358
- sentimentAnalysis, template 2792
- Output parsers on a Basic LLM Chain.

# Expected

These chains import and run. Each is a thin layer over the existing `chainLlm` plus structured-output plumbing: the extractor is a JSON-schema output, and the classifier is an n-output router whose port count comes from `categories`, the same way Switch does it.

# Actual

Every one is a blocking `kilasflow.unsupported` placeholder. Information Extractor is the #8 unlock (+14 templates) and Text Classifier the #19.

# Acceptance Criteria
- [ ] `informationExtractor` and `textClassifier` (with one output port per category) import and run
- [ ] Summarization, Q&A (retrieval) and Sentiment chains import and run
- [ ] The Auto-fixing and Item List output parsers exist

# Implementation Plan

Add `kilasflow.informationExtractor` and `kilasflow.textClassifier` first. Both reuse `outputschema.go`, and the classifier's ports come from PortsFor. Summarization can map/reduce over chainLlm.

# Notes

Related tickets: FEAT-gcq50s

Related (from the audit): none. FEAT-gcq50s excludes the "Q&A Chain retriever".

# Related Files

`probes/Text_Classifier.json`, `probes/Information_Extractor.json`, `results-ai.json` (Sentiment Analysis, Summarization Chain, Question and Answer Chain, Auto-fixing/Item List Output Parser).

# Attachments
