---
id: BUG-8dmp5y
title: 'Auth/session hardening: redirect secret leak, proxy SSRF bypass, login throttle, revalidation'
status: doing
priority: high
labels:
    - security
    - auth
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T14:25:53Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 4 finding(s) from dims: find:api-security-tenancy.

---
### Credential allowedDomains bypassed by HTTP redirect: custom-header/query secrets forwarded to any host [find:api-security-tenancy] (high/security) · area: credentials / safehttp / HTTP Request node · confidence: high

engine.Request.Authenticate checks the credential's AllowsHost only against the initial URL; safehttp's CheckRedirect re-checks scheme/AllowedHosts/IP but never the credential's AllowedDomains, and Go strips only Authorization/Cookie (not custom headers/query) on cross-host redirects, so a scoped httpHeaderAuth secret follows a 30x to an out-of-scope host.

Evidence: Private :8107. Credential httpHeaderAuth {name:X-Api-Key,value:SCOPED-SECRET-5521} allowedDomains ['127.0.0.1']. Control: HTTP node GET http://localhost:8097/direct -> failed 'credential ... is not allowed for host localhost'. Attack: GET http://127.0.0.1:8097/redirect?to=http://localhost:8097/leaked -> succeeded; stub log shows the follow-up 'GET /leaked Host: localhost:8097 X-Api-Key: SCOPED-SECRET-5521'. Same shape in the load-options HTTP loader and the credential-test probe (AllowsHost checked on the initial target only, then safehttp.NewClient(policy).Do).

n8n behavior: n8n's per-credential 'Allowed HTTP Request Domains' is meant to bound where a credential is sent; parity plus defence-in-depth argue the scope must survive redirects.

Impact: A tenant workflow (or a malicious redirect from a legitimate but compromised endpoint) exfiltrates a scoped API key/header secret to an attacker host, defeating the credential's host-scoping control.

Suggested fix: Make CheckRedirect credential-aware: on a redirect whose host fails the credential AllowsHost, stop following (http.ErrUseLastResponse) or strip every header/query the credential added; add tests for header and query credentials across a redirect.

Files: internal/engine/authenticate.go, internal/safehttp/safehttp.go, internal/loadoptions/loadoptions.go, internal/credentials/registry.go

---
### SSRF guard fully bypassed when HTTP(S)_PROXY is set (ProxyFromEnvironment) [find:api-security-tenancy] (high/security) · area: safehttp · confidence: high

safehttp's transport uses Proxy: http.ProxyFromEnvironment. When a proxy env var is present the dialer only validates the proxy's address; the real target host (metadata IP, RFC1918, loopback) is never resolved or checked, so the private-address guard is void for every outbound workflow request.

Evidence: Restarted private instance with HTTP_PROXY=http://127.0.0.1:8097 (proxy in allowed_private_endpoints). HTTP node GET http://169.254.169.254/latest/meta-data/iam/ -> succeeded; the stub received the absolute-form proxy request 'GET http://169.254.169.254/latest/meta-data/iam/'. Without the proxy the identical URL is refused as a link-local address.

n8n behavior: N/A (SSRF hardening is KilasFlow's own control).

Impact: On any deployment that sets HTTP(S)_PROXY (common in enterprise/k8s egress setups), a tenant-authored URL can reach cloud metadata and the internal network — the exact class the guard exists to stop.

Suggested fix: Set Proxy: nil for tenant-authored egress, or when a proxy is configured pre-resolve and policy-check the target host before dialing and document that the proxy must enforce egress; make outbound proxying an explicit config knob rather than implicit env.

Files: internal/safehttp/safehttp.go

---
### Login has no throttling/lockout and is an unauthenticated PBKDF2 CPU-exhaustion vector [find:api-security-tenancy] (high/security) · area: auth / login · confidence: high

/auth/login applies no per-IP or per-account rate limit, no lockout, and no cap on concurrent PBKDF2 work (600k iterations per attempt, plus a full decoy hash for unknown emails), so it enables both password brute force and an unauthenticated DoS.

Evidence: Private :8107. One failed login ~58 ms. 40 parallel guesses for a@sec.test -> 40x401 in 0.57 s (~70/s, no 429/delay/lockout). Under 400 parallel unauthenticated logins, an authenticated GET /api/v1/workflows took 9.24 s instead of ~2 ms. Unknown emails also burn a decoy PBKDF2, so no valid email is even needed for the DoS.

n8n behavior: n8n rate-limits its login route (express-rate-limit, ~5/min -> 429) and offers MFA.

Impact: Online password guessing is unimpeded, and a burst of login requests starves CPU for the whole process (API + workers), degrading every tenant on a shared host.

Suggested fix: Per-IP and per-account token-bucket rate limiting with 429+Retry-After on /auth/login, progressive delay/temporary lockout per account, and a semaphore capping concurrent password-hash work so login cannot starve the API; log failed logins.

Files: internal/api/handlers/auth.go, internal/api/middleware/auth.go, internal/auth/keys.go

Existing tickets: FEAT-ddzk2k

---
### Stateless sessions are never revalidated: disabled/password-changed users keep full access and can mint non-expiring API keys [find:api-security-tenancy] (medium/security) · area: auth sessions / API keys · confidence: high

The auth middleware validates a session cookie only by HMAC+expiry and never re-checks the user row, so a disabled account or one whose password was changed keeps working for the session TTL — and can convert that window into permanent access by minting an API key (no expiry).

Evidence: Logged in as b@sec.test (cookie), then set users.disabled_at=now and replaced password_hash in the DB. With the old cookie: GET /auth/me 200, GET /workflows 200, POST /api-keys 201 returning a full new token (prefix da5fad7b7cd1). DisabledAt is only checked in Login, not in middleware resolve().

n8n behavior: n8n invalidates sessions on password change/user disable and supports API-key expiry.

Impact: Offboarding a user or forcing a password reset does not cut existing sessions, and a stolen/retained cookie becomes a permanent API key; up to 12h session TTL with no server-side revocation.

Suggested fix: Bind sessions to a per-user version (hash of password_hash + disabled flag) verified against a cached user lookup; reject disabled users per request; require fresh re-auth to mint API keys and offer key expiry; revoke a user's keys on disable.

Files: internal/api/middleware/auth.go, internal/auth/session.go, internal/api/handlers/auth.go

Existing tickets: FEAT-ddzk2k

## Acceptance criteria

- [ ] Credential allowedDomains bypassed by HTTP redirect: custom-header/query secrets forwarded to any host
- [ ] SSRF guard fully bypassed when HTTP(S)_PROXY is set (ProxyFromEnvironment)
- [ ] Login has no throttling/lockout and is an unauthenticated PBKDF2 CPU-exhaustion vector
- [ ] Stateless sessions are never revalidated: disabled/password-changed users keep full access and can mint non-ex
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress 2026-09-19 (SecurityDx)
- Status: doing. Research + partial implementation done this session.

## Progress 2026-09-19 (SecurityDx, partial)
- safehttp (committed in ad23336): Proxy:nil for tenant egress + CredentialScope ctx carrier (WithCredentialScope/CredentialScopeFrom) consulted in CheckRedirect (host:port passed, ErrUseLastResponse stops chain) + tests TestCredentialScopeStopsARedirectOutsideItsDomains, TestClientIgnoresProxyEnvironment. Scoped: go test ./internal/safehttp/ PASS.
- Remaining: engine Authenticate() 1-line scope attach (spec sent to EngineExpression, agreed); login throttle (429+Retry-After, per-IP/account buckets, PBKDF2 semaphore); session revalidation (user-version bind, disabled check per request, API-key mint re-auth/expiry); credential-test probe scope attach.

## Work (Main review 2026-09-19)
- safehttp partial (SecurityDx, in ad23336): Proxy:nil (tenant egress never via ProxyFromEnvironment), CredentialScope ctx + CheckRedirect hop check with ErrUseLastResponse, 2 regression tests green (`go test ./internal/safehttp/` ok).
- Remaining per agent: engine Authenticate() attach (EngineExpression agreed), login throttle + PBKDF2 guard, session revalidation, probe/loader scope attach. Redirect secret leak + proxy SSRF bypass done; login throttle + revalidation open.
