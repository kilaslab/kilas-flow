---
id: FEAT-fqmh01
title: 'Google OAuth credentials: show the redirect URI, connected state; don''t mark absent secrets ''Stored''; dialog overflow'
status: todo
priority: medium
labels:
    - credentials
    - oauth
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

A user cannot finish Google OAuth setup without reading the source to find the redirect URI, and cannot tell whether an account is connected.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-9, OPS-10). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## OPS-9: Google OAuth credentials never show the redirect URI to register, and never-connected accounts look "Stored"

*ux · medium · credentials / oauth*

**n8n:** The OAuth2 credential modal shows "OAuth Redirect URL" with a copy button (docs: "From your n8n credential, copy the OAuth Redirect URL. Paste it into the Authorized redirect URIs in Google Console") and an "Account connected" / "Reconnect" state.

**Steps to reproduce:**

1. New credential → Gmail OAuth2 (the default type in the list), name only, click "Connect with Google". 2. Set a client id and secret and call POST /credentials/{id}/oauth/start. 3. Reopen the credential with Edit. 4. Create a Drive credential with only a client id via the API.

**Actual:**

(a) Step 1 saves the credential, then shows "Google authorization failed: 422 — set a Google Cloud client id and secret on this credential, or configure the platform client (KILASFLOW_GOOGLE_CLIENT_ID)". (b) The redirect URI Google must be given is `http://127.0.0.1:18080/oauth/callback` (from the authorizeUrl), and nothing in the UI shows it. With no public URL configured it is derived from the Host header, which is also never explained. (c) GET returns `access_token`, `refresh_token` and `clientSecret` as "••••••••" even though none was ever set, so Edit shows "Stored — type to replace" for a secret that doesn't exist. Neither the list nor the dialog can say whether the Google account is connected.

**Expected:**

A read-only "OAuth redirect URL" row with a copy button, a hint when it is a loopback or private address, and a "Connected as … / Not connected" badge in the list and the dialog. Redact only secrets that are present.

**Suggested fix:**

Expose `redirectUrl` from GET /credential-types (or a new /oauth/redirect-url) and render it for OAuth types. In Redacted, return "" for absent optional secrets. Add a `connected` flag (refresh_token present) to CredentialResource.

**Evidence:**

agents/ux-ops/09-cred-google-connect-noclient.png, agents/ux-ops/10-cred-list-gmail.png; internal/credentials/credentials.go:146-170 (`Redacted` marks absent optional secrets as stored); internal/api/handlers/oauth.go:110-113 (redirect = public URL or request host).

**Related:**

none


## OPS-10: The Google credential dialog's contents overflow its panel

*bug · low · credentials*

**Steps to reproduce:**

1. /credentials → New credential. The type defaults to Gmail OAuth2. 2. Look at the right edge of the dialog.

**Actual:**

The dialog is 384 px wide (sm:max-w-sm) but its content is 455 px (scrollWidth), because the footer carries 4 buttons ("Connect with Google", "Test", "Cancel", "Save credential"). Inputs and buttons stick out past the card onto the blurred backdrop. Only the two Google types trigger it; header, Telegram and JWT stay at 384/384.

**Expected:**

The content fits the dialog, or the dialog is wider.

**Suggested fix:**

Give this Dialog.Content `sm:max-w-lg` and let Dialog.Footer wrap (`flex-wrap`), or move "Connect with Google" into the form body.

**Evidence:**

agents/ux-ops/08-cred-new-dialog.png; measured via eval (dialog 528–912 px, inputs 544–967 px).

**Related:**

none


# Acceptance Criteria
- [ ] The OAuth types show a read-only "OAuth redirect URL" with a copy button, plus a hint when it is a loopback or private address
- [ ] The list and the dialog show "Connected as … / Not connected" (a `connected` flag on CredentialResource)
- [ ] `Redacted` returns empty for optional secrets that are absent
- [ ] The Google credential dialog content fits its panel (wider dialog, or a wrapping footer)

# Implementation Plan

See each finding's suggested fix above.

# Notes

# Related Files

# Attachments
