---
id: BUG-hejyb9
title: 'Code node: httpRequest''s error for a non-2xx status does not read as n8n''s'
status: todo
priority: low
labels:
    - code-node
    - javascript
created: "2026-09-25T09:56:46Z"
updated: "2026-09-25T09:56:46Z"
---

# Description

Found by the 2026-09-25 Code-node self-test. When `this.helpers.httpRequest` gets a 404, its error message is "The request failed with status 404 Not Found". n8n's `httpRequest` (axios underneath) says "Request failed with status code 404", and scripts that match on the message behave differently.

# Acceptance Criteria
- [ ] The error thrown for a non-2xx status reads as n8n's does (verify n8n's actual message and error properties black-box or from its source as a behaviour reference, never ported).
- [ ] The error's other properties scripts commonly read (status/statusCode, response body) match what n8n exposes, or the difference is documented.
