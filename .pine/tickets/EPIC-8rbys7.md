---
id: EPIC-8rbys7
title: 'n8n parity & UX audit (2026-09-23): findings backlog'
status: todo
priority: high
labels:
    - audit
    - n8n
    - parity
    - ux
created: "2026-09-23T01:16:36Z"
updated: "2026-09-23T01:16:36Z"
---

# Description

A parity and UX audit of KilasFlow against n8n, run on 2026-09-23 against a fresh build of `main` (018af94). Parallel agents each covered one area:
- which n8n nodes the community uses most, and which of them are missing
- editor and canvas UX, including how large workflows load
- debugging UX: executions, errors, pinned data, the logs
- the AI Agent node against a local Ollama model (`gemma4:12b-mlx`), with each tool and memory type
- how faithfully popular n8n templates import
- the dashboard, credentials and onboarding

Each finding became a child ticket of this epic. Related findings with one fix were grouped into a single ticket.

## Summary

**Headline numbers:**
- **Template coverage.** Only **9.4%** of the 499 most-viewed n8n templates, and **2.4%** of the 499 newest, use nothing but node types KilasFlow can run.
- **Clean imports.** Of the 45 most-viewed templates, all 45 import (HTTP 201), but only **2 (4.4%)** are runnable as imported. 25.6% of their functional nodes become placeholders.
- **Biggest unlocks.** Adding **Code (JavaScript), Google Sheets, Slack and the OpenAI app node** would raise fully runnable templates from 5.9% to 30.0%. The Code node alone appears in 56.9% of templates (83.6% of the newest), and is planned in EPIC-tjnr1z.
- **Editor performance** is not the problem. At 300 nodes, load takes 0.24–0.42 s and pan/zoom stays at 60 fps. Only typing degrades: 98 ms per keystroke at 300 nodes (BUG-pzkpfr).
- **AI agent on local Ollama** (`gemma4:12b-mlx`): of 14 cases, 4 pass, 8 partly pass and 2 fail.

**What works well**, reported by every agent:
- Imports never fail outright, and positions, stickies and AI sub-node edges survive.
- The import report is honest.
- The execution replay canvas streams live over SSE.
- The CLI debugging surface is strong: `run --wait`, `exec get` and `debug eval`.
- Output is XSS-safe, icon buttons are accessible, and the Indonesian translation is complete.
- MCP over streamable HTTP, multi-tool selection and memory isolation all work.

**Regressions.** 22 findings reproduce items that tickets already marked done claim to have fixed, or leave unchecked. The notable ones are BUG-rrkjrd, BUG-ysvmaa, BUG-6as5y7, BUG-6jvcs5, BUG-esb9sh, BUG-y57cz4, FEAT-56nep4 and FEAT-jvembs. Each child ticket names the ticket it contradicts.

**Changes made during the audit:**
- The README was rewritten, and a hero screenshot added at `.github/assets/editor.png`.
- The README's deep-dive sections moved into the docs (`concepts/architecture.md`, `concepts/webhooks.md`, CONTRIBUTING).
- Stale node counts in the docs were re-measured.

## Children (104 tickets; every finding covered, several merged into one ticket)

### Node catalogue gaps (ranked by n8n template usage) (21)

- FEAT-vntngh [critical] Google Sheets node, trigger and tool (the most-used n8n app node)
- FEAT-wzfz3d [high] 'Chat-ops nodes: Slack, Discord, WhatsApp Business Cloud (+ Slack/WhatsApp triggers)'
- FEAT-t672pv [high] OpenAI and Google Gemini app nodes (message, analyze image, transcribe)
- BUG-nzy3pa [high] Chat/embedding model names dropped on import (Gemini modelName, n8n defaults) and lost on export; provider options dropped silently
- BUG-9pmv8y [high] Real RAG templates lose the main edge into the insert-mode vector store
- BUG-3qxx0j [high] 'Extract From File: fromJson rejected, absent operation becomes pdf, xlsx/csv import silently then fail'
- FEAT-zwpvbf [high] 'LangChain root chains: Information Extractor, Text Classifier, Summarization, Q&A, Sentiment'
- FEAT-re138f [high] 'File & format utility nodes: Convert to File, HTML, Markdown, XML, Crypto, Compression, Edit Image'
- FEAT-sz4ddp [high] 'Email: Send Email (SMTP) and IMAP Email Trigger'
- FEAT-ppnetz [medium] 'Model providers beyond OpenAI-compatible: Anthropic, Azure OpenAI, Ollama/Gemini embeddings'
- FEAT-mj2nek [medium] 'Persistent chat memory: Postgres Chat Memory, Redis Chat Memory, Chat Memory Manager'
- FEAT-zm3wh2 [medium] Vector store retrieve mode, Vector Store Tool, Qdrant and In-Memory stores
- BUG-b8bwhw [medium] Importer does not map *Tool variants of native nodes (httpRequestTool, gmailTool, postgresTool)
- FEAT-nq1vsx [medium] 'AI tools: Think, Wikipedia, SerpAPI, Code Tool, and an MCP Server Trigger'
- FEAT-2m24nh [medium] 'Top SaaS app nodes via packs: Airtable, Google Calendar/Docs, Notion, Supabase, GitHub'
- FEAT-4e376e [medium] Multi-page n8n Form node and Wait-for-form with custom fields
- FEAT-xzdn35 [medium] 'Map legacy n8n types with native equivalents: Cron, Interval, Function, Item Lists, Read Binary File'
- FEAT-c81kp3 [medium] 'HTTP Request: generic OAuth2 and predefined-credential authentication'
- FEAT-mq412g [medium] Importer resolves community node types to installed sidecar/pack nodes
- FEAT-kfmq1z [low] RSS Read and RSS Feed Trigger
- FEAT-t38djq [low] Data-utility nodes (Rename Keys, Compare Datasets, Redis) and a 'deliberately unsupported' reason table

### n8n import fidelity (7)

- BUG-5bgx5c [high] 'Switch v1/v2 rules misread on import: every rule dropped and branches 2+ cut (BUG-6as5y7 regression)'
- FEAT-274c4p [medium] 'Import-fidelity scoreboard over the top-N n8n templates (baseline: 2/45 runnable as imported)'
- BUG-ppvyzr [medium] Workflow Tool calling its own workflow ({{ $workflow.id }}) imports into a shape validate rejects
- BUG-phv0r9 [medium] Conversational / OpenAI-Functions agents import as placeholders instead of Tools Agents
- FEAT-s99vdp [medium] 'Import report and placeholder UX: readable/grouped/clickable report; placeholder & Code panels explain and suggest'
- BUG-6gkd12 [low] 'Round-trip fidelity: node notes, settings, tags dropped; versions shifted (If downgraded); get→validate not symmetric'
- BUG-p3j233 [low] Set field of n8n type 'null' outputs the string "{}" while the report says it writes null

### Debugging and executions (20)

- BUG-bw2zc1 [high] Wait inside Loop Over Items fails on the 2nd iteration ("suspended node already completed")
- BUG-zf4pnj [high] $('Node').item fails after count-changing nodes and error-output splits (BUG-rrkjrd still reproduces)
- FEAT-ktasef [high] 'Editor canvas: live per-node run status, item counts, error badges, and actionable run errors'
- FEAT-eqzpzq [high] 'NDV and inspector data panes: Input/Output with Table/JSON/Schema, search, paging, binary preview'
- FEAT-70j6dn [high] Execute step, partial execution, and pinned data (imported pinData is dropped today)
- BUG-0xv7bg [high] Execution inspector shows only a node's last run; loop bodies read "Not reached" with 0-item edges
- BUG-sgrxhh [high] 'continueErrorOutput error branch is not drawn on the canvas; Settings shows ''Continue on Fail: Disabled'''
- BUG-nbymq4 [high] HTTP 4xx/5xx errors drop status code, URL and response body (error and error-branch items)
- BUG-56qqgx [high] Webhook node panel shows /webhook/<path>, which 404s; the real address is the minted hash route
- BUG-q6b75c [high] '`exec trace` of a finished run stops after 64 events and invents a terminal frame with a backwards id'
- BUG-dstsg9 [medium] Every node run records 0 ms (startedAt == finishedAt); BUG-6bqh51 still reproduces
- FEAT-02cj1g [medium] 'Expression editor: live result preview, node/field completions, validation of unknown nodes and syntax'
- BUG-nn74ph [medium] 'Go Code node: panics report only ''exited with status 2''; compile errors use wrapper line numbers'
- FEAT-bfrkyk [medium] '''Listen for test event'' for webhook-triggered workflows (Execute currently runs with an empty item)'
- BUG-p334yw [medium] Error workflow only runs if itself active; payload lacks execution.url and uses node id for lastNodeExecuted
- FEAT-zn5rqy [medium] 'Executions UI: retry (original/current), delete, debug-in-editor, date filter, per-workflow link, sticky actions'
- BUG-z0s4zg [medium] ai.* and webhook.response SSE events are unnamed, and each prints a goroutine dump to the server log
- BUG-j7qrp2 [medium] '`debug eval` evaluates against the node''s output, can''t pick an item/run, and mixes runs'
- FEAT-53pa9a [medium] Structured server log line for every terminal execution (failed runs leave no trace today)
- BUG-x6gyc1 [medium] Timer Wait shown as 'Waiting for approval' with Approve/Reject; cancelled node reads 'Not reached'

### Editor and canvas (14)

- BUG-n9a6bz [high] Node picker search goes stale after the first letter, and Enter inserts a different node than highlighted
- BUG-ngt25j [high] 'Code node and sticky content use a single-line input: Enter does nothing, pasted multi-line code collapses'
- BUG-txafja [high] Pasting n8n JSON onto the canvas bypasses the importer (placeholders, JS silently lost, '=' expressions literal)
- FEAT-ez6xtm [high] 'Node states & actions: disabled nodes look active and can''t be re-enabled; no context menu, notes or pin'
- BUG-15st2k [high] 'Canvas node rendering: AI port labels overlap, placeholders draw 24 ports, branch labels crossed, fallback icons, empty subtitles'
- BUG-rbask0 [high] 'Sticky notes: node names unreadable on stickies (dark theme), selected sticky covers nodes, raw colour number, markdown links raw'
- FEAT-4bjfny [medium] 'Node picker discoverability: n8n aliases/display names, synonyms, HTTP fallback row, one Triggers group'
- BUG-3k12ky [medium] 'Canvas input parity: Cmd+A/edge selection broken, wheel zooms instead of pans, n8n shortcuts missing, Cmd+S opens browser dialog'
- FEAT-w7n7x6 [medium] 'Node placement: new nodes stack at the viewport centre, Tab ignores selection, ''+'' leaves source selected, delete doesn''t reconnect'
- BUG-bcahaj [medium] 'Editor chrome collisions: minimap covers zoom/fit/tidy (>12 nodes), Chat covers minimap/controls, inspector hides canvas at 1024-1280 px'
- FEAT-mxmjt7 [medium] 'Parameter editing: live required-field checks with node badges, type-aware IF/Filter operators, Set/HTTP field polish'
- BUG-pzkpfr [medium] 'Typing in the inspector re-projects the whole canvas: keystroke latency 29→98 ms from 50→300 nodes'
- FEAT-fpqg78 [low] Rename the workflow and edit workflow settings (timeout, error workflow, timezone) from inside the editor
- BUG-hmp85t [low] Revision preview can show a blank canvas; save/navigation messages expose revision ids, 'Try again' on 404, native confirm

### AI agents (tested on local Ollama) (11)

- BUG-2mes2k [high] HTTP Request Tool hands the model only the first element of a JSON-array response
- BUG-t12ffz [high] Data table Tool set to Insert (or any write) silently performs a read, and the agent reports success
- BUG-ecbq28 [high] 'Reasoning models: `reasoning` dropped between tool turns (wrong answers), channel tokens leak, no think/effort option'
- BUG-xam6t8 [high] 'AI timeouts: streamed answers die at 30 s, errors blame the wrong bound, and a 1 m default kills agent runs'
- FEAT-edzr73 [high] 'Canvas chat panel: markdown, preserved newlines, token streaming, visible tool steps, human-worded errors'
- FEAT-ky75b5 [high] 'AI logs view: each model call (messages, response, tokens, latency) and tool call (input, output, duration)'
- BUG-n6p7qy [medium] 'Workflow Tool: sub-workflow''s typed inputs not offered to the model; every failure reported as ''not active'''
- BUG-rh7mpa [medium] Structured Output Parser ignores minimum/maximum/additionalProperties and refuses nullable types
- BUG-6d6wbg [medium] 'Agent tool calls: malformed arguments silently become {} and run; unknown tool names get no list of valid tools'
- FEAT-qf0hsa [low] 'MCP: SSE transport (or a clear error) for the MCP Client Tool; an HTTP transport for `kilasflow mcp serve`'
- BUG-9dw5me [low] Basic LLM Chain ignores image input (vision only works on the Agent); vision toggle is mis-described

### Dashboard, credentials, onboarding, API (21)

- BUG-rytwy7 [critical] Datastore row search starts an endless request loop (~2,600 req/s) and freezes the page
- BUG-g7ffj1 [high] Impossible cron (e.g. '0 0 31 2 *') is accepted and fires every 15 s; zero times shown as 'Jan 1, 07:07:12'
- BUG-e7dwpk [high] Datastore names can collide (incl. case-only); 'By Name' silently acts on the wrong table
- FEAT-p01rcw [high] 'First-run experience: stale ''Scaffolding only'' root page, /app 404, no samples/templates/help'
- BUG-66fhea [medium] Boot warns KILASFLOW_ENCRYPTION_KEY (and other secret/env vars) 'matches nothing and was ignored' although used
- BUG-epy2se [medium] 'Credential tests: untestable types shown as ''Test failed''; OpenAI has no Base URL; Telegram baseUrl required, no probe'
- FEAT-fqmh01 [medium] 'Google OAuth credentials: show the redirect URI, connected state; don''t mark absent secrets ''Stored''; dialog overflow'
- FEAT-kcdrcy [medium] 'NDV credentials: create inline, one Authentication selector for HTTP Request, truncate long names'
- FEAT-8752vx [medium] 'Credential usage: warn on deleting an in-use credential; flag and refuse dangling references'
- FEAT-tjcr13 [medium] 'Human validation errors across dashboard: field labels not keys, no ''422 —'', inline + cleared on edit, 404 back action'
- FEAT-wcr6en [medium] 'Typed credential inputs: options selects, number fields, multi-line JSON editor'
- BUG-v8ksv8 [medium] Unknown /api/* paths return 200 with the SPA HTML instead of a JSON 404
- FEAT-a3dwj2 [medium] 'Dashboard IA: Overview with stats, Help menu, theme toggle (light palette unreachable), packs/embed settings'
- FEAT-m7aw75 [medium] '$vars is always empty: add a Variables store (API + Settings page) or flag $vars on import'
- BUG-rs0xq1 [medium] Published workflow JSON Schema allows 4 connection kinds; the engine accepts 13 (RAG/parser docs fail validation)
- BUG-b4cb1c [medium] 'Board/doc drift: FEAT-3taswf ''done'' but @kilasflow/sdk not on npm; stale node counts; CLI help nouns'
- BUG-605n21 [low] 'Data table polish: duplicate column headers, raw ISO datetimes, id-based export filename, ''Datastores'' vs ''Data table'' copy'
- BUG-namghh [low] 'Low-contrast text: root links 1.48:1, datastore type labels and ''Failed'' badge under WCAG AA'
- BUG-719gaz [low] 'Auth-off consistency: API-key copy contradicts behaviour, /login claims auth required, 401 on /auth/me every page'
- BUG-2n4rfz [low] /docs shows Scalar 'Ask AI', 'Generate MCP' and registry search blocked by the page CSP (4 console errors)
- FEAT-3t112f [low] 'New schedule dialog: searchable active-workflow picker, cron description, next-run preview, timezone'

### Embedding (1)

- BUG-b3p8va [critical] 'Embedded editor cannot save or run from a cross-origin host page: every write returns 403 ''not allowed from that origin'''

### Agent surface: CLI, skills, MCP (9)

- BUG-y38bss [high] Every guarded CLI verb and MCP confirm:true call fails with 401 when auth is off (the default)
- BUG-2eryxn [high] MCP adapter breaks every boolean tool argument (`run.wait`, `api.list`, `skills_install.dry_run`)
- BUG-r1m83f [high] Guarded operations bypass confirmation through `kilasflow api` and the MCP `api` tool
- BUG-x28fsx [high] 'Skills bundle content: 10 of 13 skills describe verbs as unshipped, no copy-paste JSON shapes, no AI/loop/binary coverage'
- BUG-2z8geh [high] 422 validation errors echo the whole request body, including credential secrets, into the response/envelope
- BUG-t3p92b [medium] '`skills check` flags every non-KilasFlow skill in the harness dir as drift; `install --scope project` resolves to cwd'
- FEAT-fs3pjr [medium] 'CLI ergonomics: bare/n8n node names in `node describe`, human output with data and failure reasons, real `--help`, actionable errors, richer `context`'
- FEAT-sc3qrq [medium] 'n8n-style CLI workflows: import/export files, `--all` and directory forms, bulk operations, list filters'
- FEAT-0hdfzd [medium] 'MCP tool quality: inline documents, annotations, enums, real descriptions; record `--skills-used` beyond revisions'

## Attachments

- `n8n-node-gap-matrix.md`: 130+ n8n node types ranked by template usage against their KilasFlow status, with the greedy unlock order
- `greedy-unlock.json`: the unlock order as data
- `template-import-report.csv` and `template-unsupported-frequency.md`: per-template import results for the top 45, and the frequency of unsupported types over the top 198
- `ai-ollama-matrix.md`: the AI Agent test matrix against local Ollama
- `editor-perf.md`: load, pan/zoom and keystroke measurements at 50, 150 and 300 nodes

# Goals

- Every audit finding is tracked as a child ticket with repro steps, evidence and acceptance criteria.
- The node gaps are ranked by real n8n template usage, so the catalogue grows in the order that unlocks the most workflows.
