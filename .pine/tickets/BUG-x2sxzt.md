---
id: BUG-x2sxzt
title: Respond to Webhook serves tenant-written HTML on the instance's own origin with no CSP sandbox
status: todo
priority: high
labels:
    - security
    - webhooks
parent: EPIC-7c3ry9
created: "2026-09-23T07:35:21Z"
updated: "2026-09-23T07:35:37Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review.

A "Respond to Webhook" node lets a workflow author return any body and headers as the webhook's HTTP response, defaulting to `text/html; charset=utf-8` when the body is not valid JSON (`writeResponse`, internal/webhook/webhook.go ~1115-1131). Webhook responses are served on the same router as the rest of the API (`router.Handle(WebhookPrefix, webhookHandler)`, internal/api/routes.go ~84-85), i.e. on the instance's own origin.

That makes a tenant-controlled HTML page same-origin with every editor iframe and the dashboard. A page returned this way can run script that makes cookie-authenticated same-origin calls against the host SaaS application's own API, with no isolation between "content a webhook returned" and "the application's own pages".

# Steps to Reproduce

1. Build a workflow whose trigger is a webhook, ending in a Respond to Webhook node that returns an HTML body containing a `<script>`.
2. Call the webhook and inspect the response headers.

# Expected

An HTML response from Respond to Webhook cannot execute script with access to the instance's own cookies/origin — either it carries a CSP that sandboxes it without `allow-same-origin`, or it is served from a separate origin entirely.

# Actual

The response is served with no CSP header, `text/html` (or whatever content-type the workflow set), from the same origin as the dashboard and every editor iframe.

# Acceptance Criteria
- [ ] Webhook HTML responses carry a CSP that sandboxes them (`sandbox` without `allow-same-origin`), or are served from a separate origin
- [ ] A test asserts a webhook HTML response carries the sandboxing header

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid, refs accurate (webhook.go:1115-1131, routes.go:84-85; the only CSP is internal/web/embed.go:238-246).
- A second site: webhook.go:923-929 serves the trigger's own `responseData` acknowledgement as `text/html` with no CSP — same fix needed.
- `writeResponse` copies workflow-set headers first (:1116-1118), so a workflow can set its own `Content-Security-Policy`: force the sandbox header after the copy (or strip tenant CSP).

# Related Files

internal/webhook/webhook.go `writeResponse`, ~1115-1131 (defaults an unrecognized body to `text/html`, sets no CSP)
internal/api/routes.go ~84-85 (webhook prefix routed on the same router as the API)
