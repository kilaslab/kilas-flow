---
id: FEAT-c81kp3
title: 'HTTP Request: generic OAuth2 and predefined-credential authentication'
status: todo
priority: medium
labels:
    - n8n
    - parity
    - credentials
    - http
parent: EPIC-8rbys7
created: "2026-09-23T01:17:55Z"
updated: "2026-09-23T01:17:55Z"
---

# Description

19.7% of the sampled HTTP Request instances authenticate with `predefinedCredentialType`. It is n8n's escape hatch for every service without a node, and KilasFlow's import advice ("attach a KilasFlow credential") cannot be followed for any OAuth2 service.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-19). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** - HTTP Request can authenticate with "Predefined Credential Type" (any node's credential, e.g. `googleSheetsOAuth2Api`, `openAiApi`, `anthropicApi`) or with generic OAuth2.
- In the cache, 299 of 1,520 HTTP Request instances (19.7%, across 117 templates) use `predefinedCredentialType`, and 30 use generic `oAuth2Api`.
- This is n8n's escape hatch for every service without a node.

# Steps to Reproduce

Import one workflow with four real HTTP Request instances: predefined `openAiApi` (18862), `anthropicApi` (18911), `googleSheetsOAuth2Api` (19067), and generic `oAuth2Api` (19099).

# Expected

HTTP Request accepts a generic OAuth2 credential (auth-code/client-credentials with refresh) and any stored credential type whose auth can be applied to a request. The import diagnostic names the credential type to create.

# Actual

- Each gets the lossy note "this HTTP Request used an n8n credential. Credentials are not imported; attach a KilasFlow credential before running the workflow."
- The imported node keeps `authentication: predefinedCredentialType`, but `kilasflow.httpRequest` has no `authentication` parameter and accepts only httpBasicAuth/httpHeaderAuth/httpBearerAuth/httpQueryAuth/httpCustomAuth.
- The only OAuth2 credential types (`gmailOAuth2`, `googleDriveOAuth2Api`) cannot be attached to HTTP Request, so the advice cannot be followed for any OAuth2 service.
- Separately, the openAiApi instance is blocked: "this node sends a multipart form body, which this server does not build".

# Acceptance Criteria
- [ ] A generic `oAuth2Api` credential (authorization code and client credentials, with refresh)
- [ ] HTTP Request can attach any OAuth2 credential, and any stored credential whose auth can be applied to a request
- [ ] The import diagnostic names the credential type to create
- [ ] Multipart form bodies are supported

# Implementation Plan

Add a generic `oAuth2Api` credential type (the refresh flow exists for Gmail/Drive) and let HTTP Request attach any OAuth2 credential. Translate `predefinedCredentialType` into a named "attach credential of type X" issue.

# Notes

Related (from the audit): none

# Related Files

`probes/http-predefined-auth.json` (wf_01a0cbcd-db87-7cc9-b278-35a92eaf8aeb). The catalog `kilasflow.httpRequest.credentials` has 5 types. `GET /api/v1/credential-types` lists 15.

# Attachments
