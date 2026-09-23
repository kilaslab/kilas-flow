---
id: FEAT-9we7kw
title: Code-node JavaScript formats dates in en-CA and en-GB, the locales imported workflows use for YYYY-MM-DD and day-first dates
status: todo
priority: medium
parent: EPIC-tjnr1z
created: "2026-09-23T07:09:43Z"
updated: "2026-09-23T07:09:43Z"
---

# Description

Date formatting in Code-node JavaScript (Intl.DateTimeFormat, Date#toLocale*,
Luxon's toLocaleString) supports en and en-US only; any other locale throws
"RangeError: date formatting in locale X is not supported" rather than
answering in the wrong layout. The corpus uses en-CA, most likely for the
`toLocaleDateString('en-CA')` YYYY-MM-DD idiom, and en-GB is the usual
day-first choice, so imported workflows that use either fail at run time.

The en-US data is generated from recorded Node 24 output
(scripts/js-parity/record.mjs) and pinned by internal/jsrun/testdata/parity
goldens; 664 of the 1,794 swept option combinations differ between en-CA and
en-US.

# Acceptance Criteria
- [ ] en-CA and en-GB date formatting match Node 24 across the same option
      sweep as en-US, recorded by the same generator and pinned by goldens.
- [ ] `new Date(...).toLocaleDateString('en-CA')` gives YYYY-MM-DD.
- [ ] Every other date locale still refuses by name.
- [ ] The corpus scoreboard counts the templates this unblocks.

# Implementation Plan

# Notes

# Related Files

# Attachments
