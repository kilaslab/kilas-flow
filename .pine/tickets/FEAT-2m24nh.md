---
id: FEAT-2m24nh
title: 'Top SaaS app nodes via packs: Airtable, Google Calendar/Docs, Notion, Supabase, GitHub'
status: todo
priority: medium
labels:
    - n8n
    - parity
    - node-catalog
parent: EPIC-8rbys7
created: "2026-09-23T01:17:55Z"
updated: "2026-09-23T01:17:55Z"
---

# Description

This 29-node SaaS cluster appears in 21.1% of templates. The declarative converter and packs exist for exactly this long tail.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-16). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Airtable appears in 31 templates, Google Calendar in 25, Notion in 24, Google Docs in 19, GitHub in 18, Supabase in 18, LinkedIn in 16, WordPress in 16, X in 15, YouTube in 13, Facebook Graph API in 12, Twilio in 11 and HubSpot in 8. This 29-node SaaS cluster appears in 21.1% of templates. On n8n.io/integrations, Airtable is #10, Notion #14 and Supabase #15.

# Steps to Reproduce

Import Manual → X for each node, using real instances: airtable, notion, googleCalendar, googleDocs, supabase, github, microsoftOutlook, hubspot, linkedIn, twitter, wordpress, youTube, facebookGraphApi, twilio and the n8n API node.

# Expected

The top SaaS nodes import and run.

# Actual

Every one is a blocking placeholder. Airtable, Notion and Google Calendar are unlocks #13–15.

# Acceptance Criteria
- [ ] Airtable, Google Calendar, Google Docs, Notion, Supabase and GitHub import and run, in that order of priority
- [ ] Each generated pack has an importer mapping entry and a credential type with a test probe

# Implementation Plan

This long tail is what the declarative converter (FEAT-ed6wdy) and packs exist for. Prioritise by this table: Airtable, then Google Calendar and Docs (sharing the Google OAuth2 from Drive/Gmail), then Notion, then Supabase, then GitHub. Add an importer mapping entry for each generated pack.

# Notes

Related tickets: FEAT-ed6wdy

Related (from the audit): none

# Related Files

`results-extra.json` (15 app nodes), `cluster-usage.json` (saas)

# Attachments
