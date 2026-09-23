---
id: FEAT-sz4ddp
title: 'Email: Send Email (SMTP) and IMAP Email Trigger'
status: todo
priority: high
labels:
    - n8n
    - parity
    - node-catalog
    - email
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
---

# Description

`emailSend` appears in 5.6% of templates and is n8n's default failure-alerting channel. There is no email credential type at all.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-10). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** `emailSend` appears in 56 templates (5.6%) and is #18 on n8n.io/integrations. `emailReadImap` appears in 14 and Microsoft Outlook in 7. The cluster appears in 7.3% of templates. Email is also n8n's default failure-alerting channel.

# Steps to Reproduce

1. Import Manual → Send Email v2.1, using template 3277 (`fromEmail`, `toEmail`, `subject`, `html`).
2. Import IMAP Email Trigger → No Op, using template 1344 (`mailbox`, `format`).
3. Import Outlook.

# Expected

SMTP send (text/html, attachments from binary) and an IMAP trigger on the existing leased poll framework import and run.

# Actual

All three are blocking placeholders. There is no `smtp` or `imap` credential type in `GET /api/v1/credential-types` (15 types, none for email). Send Email is the #5 unlock (+19 templates).

# Acceptance Criteria
- [ ] `smtp` and `imap` credential types, with a connection test
- [ ] `kilasflow.emailSend` (text/html, attachments from binary, STARTTLS/TLS), mapped from `n8n-nodes-base.emailSend` v1/v2
- [ ] `kilasflow.emailReadImap` trigger on the leased poll framework, with a UID watermark

# Implementation Plan

Add `smtp` and `imap` credential types, `kilasflow.emailSend` (net/smtp with STARTTLS/TLS), and `kilasflow.emailReadImap` on the poll framework with UID watermarking. Map both.

# Notes

Related tickets: FEAT-nqpvf6

Related (from the audit): none. FEAT-nqpvf6 (done) names IMAP only as a poll-framework consumer.

# Related Files

`results-extra.json` (Send Email SMTP, IMAP Email Trigger, Microsoft Outlook), the credential-types listing.

# Attachments
