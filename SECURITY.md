# Security policy

## Supported versions

No release tag has been cut yet: `git tag` is empty, nothing is published to
`ghcr.io/kilaslab/kilasflow`, and `@kilasflow/sdk` is not on npm. There is
therefore no supported version to patch — every security fix lands on `main`,
and `main` is the only version anyone should be running.

Once tags exist, this section will name the versions that still receive fixes.

## Reporting a vulnerability

Report privately through GitHub's advisory form:

**<https://github.com/kilaslab/kilas-flow/security/advisories/new>**

That channel is visible only to you and the maintainers. Please do not open a
public issue, and do not post it in a discussion or a pull request — an issue is
indexed within minutes and tells everyone running the product what to attack
before there is a fix.

If you cannot use GitHub, email **yusrilizzaaulia@gmail.com** with `KilasFlow
security` in the subject.

A useful report includes:

- **Version.** The output of `kilasflow -version`, or the image tag.
- **Deployment.** Docker or bare binary, SQLite or PostgreSQL, authentication on
  or off, single process or several.
- **Reproduction.** The smallest sequence that shows the problem, ideally a
  workflow JSON export plus the requests.
- **Impact.** What an attacker gains, and what access they need to start.

## What to expect

KilasFlow is maintained by one person and pays no bounty. Reports are read and
acknowledged as quickly as possible — days, not hours — and you will be told
whether the report is accepted, what the fix is, and when it ships. If you want
credit in the published advisory, say so and how you would like to be named.

Fixes are released as a commit on `main` and, once tags exist, as a patch
release. Advisories are published on the repository's security tab.

## In scope

The product's own trust boundaries. A report that defeats one of these is a
vulnerability in KilasFlow:

- **Credential storage.** Credentials are encrypted at rest with AES-256-GCM and
  the master key comes from the environment. Anything that recovers a stored
  secret without the key, or leaks one into a log, an execution event, an API
  response or an export, is in scope.
- **Authentication and authorisation.** Sessions, API keys, the principal a
  request carries, and tenant isolation — one tenant reading or writing another
  tenant's workflows, executions, credentials or datastore rows.
- **The embed boundary.** `internal/embed` issues and validates the iframe
  editor sessions: forging a session, escalating past the scopes it grants, or
  reading a workflow or datastore the session was not issued for.
- **Webhooks.** `internal/webhook` maps an inbound request to a workflow and
  trigger node. Bypassing a trigger's signature check (Telegram's
  `X-Telegram-Bot-Api-Secret-Token`, WAHA's HMAC over the raw body), replaying a
  delivery past the deduplication, or reaching a workflow that is not active.
- **Server-side request forgery.** `internal/safehttp` refuses to dial private
  and loopback addresses. A way past that guard — redirects, DNS rebinding,
  alternate address encodings, a node that bypasses the shared client — is one of
  the more valuable reports you can send.
- **Node execution.** Escaping the Code node's WebAssembly sandbox, or reaching
  the host filesystem, environment or network from a workflow's expression,
  SQL-building or routing layers.
- **The n8n importer.** A workflow document that, when imported, makes the server
  do something the document should not be able to ask for.

## Out of scope

- **Running with authentication off.** That is the documented default, the
  server says so in its log at every start, and with it off anyone who can reach
  the port owns the installation. Turning it on is four environment variables.
- **Anything that requires disabling a documented guard.** If a report needs
  `outbound.allow_private_networks: true`, or `outbound.allowed_private_endpoints`
  pointed at a target, the operator has already granted what the report claims.
  The hardening checklist is `docs/src/content/docs/operate/security.md`.
- **Vulnerabilities in the services a node talks to.** WAHA, Telegram, your
  database and every other outbound endpoint are someone else's software; report
  those upstream.
- **An attacker who already holds the encryption key, the config file or the
  database.** Those are the operator's secrets, and possession of them is
  assumed to be game over.
- **Raw volume.** Exhausting the process by sending more requests than it can
  serve is what a reverse proxy and its rate limits are for. A report that a
  single request defeats a documented limit — `webhook.max_body_bytes`,
  `webhook.response_timeout`, the server's own timeouts — is in scope; "I sent a
  million requests" is not.
- **The documentation site**, which is static and serves no application code.

## Reporters and conduct

Good-faith research is welcome and will not be met with legal threats. Please
stop at the point where you have proven the issue, do not access other people's
data, and give us a reasonable window to fix it before publishing. Behaviour in
the report itself is covered by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
