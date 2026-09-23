---
id: FEAT-re138f
title: 'File & format utility nodes: Convert to File, HTML, Markdown, XML, Crypto, Compression, Edit Image'
status: todo
priority: high
labels:
    - n8n
    - parity
    - node-catalog
    - binary
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
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
- [ ] Convert to File (csv/xlsx/json/text/base64) and HTML (extract by CSS selector, generate a table) import and run
- [ ] Markdown↔HTML, XML↔JSON, Crypto (hash/hmac/sign/uuid) and Compression (zip/gzip) import and run
- [ ] Edit Image resize/crop/text runs through a pure-Go imaging library
- [ ] Legacy `spreadsheetFile` / `moveBinaryData` map onto the new nodes

# Implementation Plan

Build them as one "file & format" family over the existing binary store. Suggested order: Convert to File and HTML, then Markdown and XML, then Crypto and Compression, then Edit Image (resize/crop/text through a pure-Go imaging library).

# Notes

Related (from the audit): none

# Related Files

`probes/Convert_to_File.json`, `probes/HTML.json`, `probes/Edit_Image.json`, `results-extra.json`

# Attachments
