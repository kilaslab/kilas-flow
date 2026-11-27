---
id: FEAT-re138f
title: 'File & format utility nodes: Convert to File, HTML, Markdown, XML, Crypto, Compression, Edit Image'
status: doing
priority: high
labels:
    - n8n
    - parity
    - node-catalog
    - binary
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-11-27T08:51:29Z"
---

# Description

These appear together in 12.1% of templates. They are pure Go with no outbound traffic, which makes them the cheapest gaps in the catalogue.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-9). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** HTML appears in 34 templates, Convert to File in 32, Markdown in 22, XML in 11, Edit Image in 11, Spreadsheet File (legacy) in 9, Crypto in 5, Compression in 3 and Move Binary Data in 6. The cluster appears in 12.1% of templates. Convert to File is n8n Pulse #31.

# Steps to Reproduce

Import Manual → X for each node, using real instances: convertToFile 3954, html 3790, markdown 2281, xml 154, editImage 2418, crypto 18987, plus a docs-derived compression node.

# Expected

Convert to File (csv/xlsx/json/text/base64→binary), HTML extract (CSS selectors) and generate, Markdown↔HTML, XML↔JSON, Crypto (hash/hmac/sign), and Compression (zip/gzip) import and run. These are pure Go with no outbound traffic, so they are the cheapest gaps in the catalogue.

# Actual

Every one is a blocking placeholder. Convert to File is the #6 unlock and HTML the #17. XML is the sole blocker in 2 of the newest templates, and Edit Image in 4 of the most-viewed.

# Acceptance Criteria
- [x] Convert to File (csv/xlsx/json/text/base64) and HTML (extract by CSS selector, generate a table) import and run
- [x] Markdown↔HTML, XML↔JSON, Crypto (hash/hmac/sign/uuid) and Compression (zip/gzip) import and run
- [ ] Edit Image resize/crop/text runs through a pure-Go imaging library
- [ ] Legacy `spreadsheetFile` / `moveBinaryData` map onto the new nodes

# Implementation Plan

Build them as one "file & format" family over the existing binary store. Suggested order: Convert to File and HTML, then Markdown and XML, then Crypto and Compression, then Edit Image (resize/crop/text through a pure-Go imaging library).

# Notes

Related (from the audit): none

# Related Files

`probes/Convert_to_File.json`, `probes/HTML.json`, `probes/Edit_Image.json`, `results-extra.json`

# Attachments

## Progress

2026-11-27, branch `worktree-agent-ac4ce01b48e7309ca` (from `feat/epic-sweep-2026-11-27`). Landed, each as a native node plus an importer mapping with export back, unit tests for the node and for the import, an e2e run in the coverage and compare specs, and a glyph:

- **Crypto** (`n8n-nodes-base.crypto` / `kilasflow.crypto`): hash (MD5, SHA1, SHA256, SHA384, SHA512, SHA3-224/256/384/512; hex or base64), HMAC over the same digests, sign (RSA PKCS#1 v1.5 and ECDSA over a PEM key, RSA-SHA1/256/384/512, standard library only), generate (UUID v4, random hex, base64 or ASCII string). A signing algorithm outside those four imports as a placeholder.
- **XML** (`n8n-nodes-base.xml` / `kilasflow.xml`): xmlToJson and jsonToxml on `encoding/xml` raw tokens. Options: attrkey, charkey, explicitArray, explicitRoot, ignoreAttrs, mergeAttrs, normalize, normalizeTags, trim, includeWhiteChars, cdata, headless, rootName, renderOpts (pretty, indent, newline); `async` is accepted and dropped. Any other option imports as a placeholder naming it. Elements are written in alphabetical order because items do not keep key order.
- **Markdown** (`n8n-nodes-base.markdown` / `kilasflow.markdown`): markdownToHtml on goldmark and htmlToMarkdown on html-to-markdown v2, behind n8n's option names. The importer writes n8n's own converter defaults out (they differ from the native ones), refuses options the node lacks (emoji, underline and others) and reports that code blocks are written fenced where n8n indents them.
- **Compression** (`n8n-nodes-base.compression` / `kilasflow.compression`): zip and gzip compress, zip and gzip decompress told apart by magic bytes, with entry-count and size bounds.
- **HTML** (`n8n-nodes-base.html` / `kilasflow.html`): extractHtmlContent (CSS selectors via cascadia; text, html, attribute, value; skip selectors; return array; trim and clean-up; from a JSON field or a binary file), convertToHtmlTable (capitalize, caption and the attribute options, validated as attributes), generateHtmlTemplate.
- **Convert to File** (`n8n-nodes-base.convertToFile` / `kilasflow.convertToFile`): csv, xlsx (a stdlib-only workbook that Extract From File reads back), toJson (all items or one file each), toText, toBinary (base64 to file). HTML, iCal, ODS, RTF, XLS and non-UTF-8 encodings import as placeholders with the reason.
- **Legacy Spreadsheet File** (`n8n-nodes-base.spreadsheetFile`): reading maps onto Extract From File and writing onto Convert to File, for csv and xlsx only; autodetect, other formats and unsupported options import as placeholders. Move Binary Data keeps its placeholder, now with a hint naming Extract From File and Convert to File.

Dependencies added (all pure Go, recorded in `.pine/memory/licensing.md`): github.com/yuin/goldmark v1.8.7 (MIT), github.com/JohannesKaufmann/html-to-markdown/v2 v2.5.2 (MIT) with github.com/JohannesKaufmann/dom v0.3.1 (MIT), github.com/andybalholm/cascadia v1.3.4 (BSD-2-Clause); golang.org/x/net is now a direct dependency (it was already indirect).

Remaining: **Edit Image** (not started), and **Move Binary Data** mapping (its two halves have different semantics for defaults and keep-source that were not settled; it still imports as a placeholder). The corpus baseline (`internal/interop/n8n/corpus/baseline.json`) still lists Convert to File as unmapped because its fixtures are not available here; regenerate it with `KILASFLOW_N8N_REFERENCE` set. The acceptance boxes for Edit Image and for `moveBinaryData` stay unticked.
