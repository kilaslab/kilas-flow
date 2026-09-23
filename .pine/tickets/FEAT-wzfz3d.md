---
id: FEAT-wzfz3d
title: 'Chat-ops nodes: Slack, Discord, WhatsApp Business Cloud (+ Slack/WhatsApp triggers)'
status: todo
priority: high
labels:
    - n8n
    - parity
    - node-catalog
    - chatops
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
---

# Description

Slack appears in 16.8% of sampled templates (29.9% of the newest), and the chat-ops cluster in 34.4% of the newest. None of these nodes exist today.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-3). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** - Slack appears in 168/998 templates (16.8%) and in **29.9% of the newest**.
- WhatsApp Business Cloud: 27. Discord: 21. WhatsApp Trigger: 11. Slack Trigger: 9. Teams: 2.
- The cluster together appears in 20.6% of templates (34.4% of the newest). Slack is #6 on n8n.io/integrations.

# Steps to Reproduce

Import Manual → Slack v2.5, using template 19147 (`resource: message`, `operation: post`, `select: channel`, `channelId` locator, `text`, `otherOptions`). Do the same for Discord, WhatsApp, `slackTrigger` and `whatsAppTrigger`.

# Expected

Slack post/update/get-channel-history, Discord (webhook and bot) and WhatsApp Cloud send, plus the Slack and WhatsApp triggers, import and run.

# Actual

- Every one of them becomes a blocking `kilasflow.unsupported` placeholder.
- The run fails with 422 "…imported from n8n-nodes-base.slack, which KilasFlow does not support".
- Slack is the #3 unlock (+40 templates) after Code and Sheets.
- The WAHA and GOWA packs are different APIs from Meta's WhatsApp Cloud node, and neither is a mapping target for `n8n-nodes-base.whatsApp`.

# Acceptance Criteria
- [ ] Slack post/update/get-history, the Discord webhook and bot send, and WhatsApp Cloud send import and run as declarative packs or native nodes
- [ ] The Slack trigger verifies Slack's request signature; the WhatsApp trigger handles Meta's verify handshake
- [ ] `slackApi`, `discordBotApi` and `whatsAppApi` credential types exist, with a test probe
- [ ] Template 19147 imports with no blocking issue

# Implementation Plan

Generate declarative packs where the n8n node is declarative. Slack Web API, Discord and WhatsApp Cloud are plain REST with bearer tokens, the same route as `pack.telegram`. Add `slackApi`/`discordBotApi`/`whatsAppApi` credential types and importer mappings. The Slack trigger needs signed-event webhook verification.

# Notes

Related (from the audit): none

# Related Files

`probes/Slack.json`, `results-extra.json` (Discord, WhatsApp Business Cloud, Slack Trigger, WhatsApp Trigger), `cluster-usage.json` (chatops).

# Attachments
