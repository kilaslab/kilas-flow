---
title: Credential types
description: Every credential type the server knows — its fields, which are secret, where an empty allowed-domains list may send it — generated from the live catalogue.
sidebar:
  order: 8
---

<!-- Generated from GET /api/v1/credential-types of a real binary by scripts/generate-node-catalogue.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the credential-type catalogue a real binary serves at `GET /api/v1/credential-types`, not hand-written.

**20 credential types ship**, and a node pack adds none — it names one of these. The concepts page covers [how a credential is stored, scoped and tested](/concepts/credentials/); this page is the per-type field reference an integrator or editor renders a form from.

| ID | Name | Fields | Testable |
| --- | --- | --- | --- |
| [`gmailOAuth2`](#gmailoauth2) | Gmail OAuth2 | `clientId`, `clientSecret`, `access_token`, `refresh_token`, `expiry`, `scope` | yes |
| [`googleDriveOAuth2Api`](#googledriveoauth2api) | Google Drive OAuth2 | `clientId`, `clientSecret`, `access_token`, `refresh_token`, `expiry`, `scope` | yes |
| [`googleGeminiApi`](#googlegeminiapi) | Google Gemini | `apiKey` | yes |
| [`googleSheetsOAuth2Api`](#googlesheetsoauth2api) | Google Sheets OAuth2 | `clientId`, `clientSecret`, `access_token`, `refresh_token`, `expiry`, `scope` | yes |
| [`googleSheetsTriggerOAuth2Api`](#googlesheetstriggeroauth2api) | Google Sheets Trigger OAuth2 | `clientId`, `clientSecret`, `access_token`, `refresh_token`, `expiry`, `scope` | yes |
| [`httpBasicAuth`](#httpbasicauth) | HTTP Basic Auth | `user`, `password` | no |
| [`httpBearerAuth`](#httpbearerauth) | HTTP Bearer Auth | `token` | no |
| [`httpCustomAuth`](#httpcustomauth) | HTTP Custom Auth | `json` | no |
| [`httpHeaderAuth`](#httpheaderauth) | HTTP Header Auth | `name`, `value` | no |
| [`httpQueryAuth`](#httpqueryauth) | HTTP Query Auth | `name`, `value` | no |
| [`imap`](#imap) | IMAP | `user`, `password`, `host`, `port`, `secure`, `allowUnauthorizedCerts` | yes |
| [`jwtAuth`](#jwtauth) | JWT Auth | `keyType`, `secret`, `publicKey`, `privateKey`, `algorithm` | no |
| [`mysql`](#mysql) | MySQL | `host`, `port`, `database`, `user`, `password`, `tls` | yes |
| [`openAiApi`](#openaiapi) | OpenAI | `apiKey`, `url` | yes |
| [`openRouterApi`](#openrouterapi) | OpenRouter | `apiKey` | yes |
| [`postgres`](#postgres) | PostgreSQL | `host`, `port`, `database`, `user`, `password`, `sslMode` | yes |
| [`smtp`](#smtp) | SMTP | `user`, `password`, `host`, `port`, `secure`, `disableStartTls` | yes |
| [`sqlite`](#sqlite) | SQLite file | `path` | yes |
| [`telegramApi`](#telegramapi) | Telegram | `accessToken`, `baseUrl` | yes |
| [`wahaApi`](#wahaapi) | WAHA | `baseUrl`, `apiKey` | no |

## gmailOAuth2

Connects to Gmail. Client ID and secret can be from your Google Cloud project or the platform app.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `clientId` | no | no | — | From your Google Cloud OAuth client. Leave empty to use the platform client (KILASFLOW_GOOGLE_CLIENT_ID). |
| `clientSecret` | no | yes | — | From your Google Cloud OAuth client. Leave empty to use the platform client. |
| `access_token` | no | yes | — | Filled by Connect. Do not paste tokens. |
| `refresh_token` | no | yes | — |  |
| `expiry` | no | no | — | RFC3339 timestamp written by Connect. |
| `scope` | no | no | `https://www.googleapis.com/auth/gmail.modify` |  |

An empty allowed-domains list means **`googleapis.com`, `*.googleapis.com`, `accounts.google.com`, `www.google.com`, `googleusercontent.com`, `*.googleusercontent.com`** for this type.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## googleDriveOAuth2Api

Connects to Google Drive. Client ID and secret can be from your Google Cloud project or the platform app.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `clientId` | no | no | — | From your Google Cloud OAuth client. Leave empty to use the platform client (KILASFLOW_GOOGLE_CLIENT_ID). |
| `clientSecret` | no | yes | — | From your Google Cloud OAuth client. Leave empty to use the platform client. |
| `access_token` | no | yes | — | Filled by Connect. Do not paste tokens. |
| `refresh_token` | no | yes | — |  |
| `expiry` | no | no | — | RFC3339 timestamp written by Connect. |
| `scope` | no | no | `https://www.googleapis.com/auth/drive` |  |

An empty allowed-domains list means **`googleapis.com`, `*.googleapis.com`, `accounts.google.com`, `www.google.com`, `googleusercontent.com`, `*.googleusercontent.com`** for this type.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## googleGeminiApi

A Google AI Studio API key for the Gemini models.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `apiKey` | yes | yes | — | A key from Google AI Studio (aistudio.google.com), in the form AIza…. |

An empty allowed-domains list means **`generativelanguage.googleapis.com`** for this type.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## googleSheetsOAuth2Api

Connects to Google Sheets. Client ID and secret can be from your Google Cloud project or the platform app.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `clientId` | no | no | — | From your Google Cloud OAuth client. Leave empty to use the platform client (KILASFLOW_GOOGLE_CLIENT_ID). |
| `clientSecret` | no | yes | — | From your Google Cloud OAuth client. Leave empty to use the platform client. |
| `access_token` | no | yes | — | Filled by Connect. Do not paste tokens. |
| `refresh_token` | no | yes | — |  |
| `expiry` | no | no | — | RFC3339 timestamp written by Connect. |
| `scope` | no | no | `https://www.googleapis.com/auth/spreadsheets https://www.googleapis.com/auth/drive.metadata.readonly` |  |

An empty allowed-domains list means **`googleapis.com`, `*.googleapis.com`, `accounts.google.com`, `www.google.com`, `googleusercontent.com`, `*.googleusercontent.com`** for this type.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## googleSheetsTriggerOAuth2Api

Connects the Google Sheets Trigger. Client ID and secret can be from your Google Cloud project or the platform app.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `clientId` | no | no | — | From your Google Cloud OAuth client. Leave empty to use the platform client (KILASFLOW_GOOGLE_CLIENT_ID). |
| `clientSecret` | no | yes | — | From your Google Cloud OAuth client. Leave empty to use the platform client. |
| `access_token` | no | yes | — | Filled by Connect. Do not paste tokens. |
| `refresh_token` | no | yes | — |  |
| `expiry` | no | no | — | RFC3339 timestamp written by Connect. |
| `scope` | no | no | `https://www.googleapis.com/auth/spreadsheets.readonly https://www.googleapis.com/auth/drive.metadata.readonly` |  |

An empty allowed-domains list means **`googleapis.com`, `*.googleapis.com`, `accounts.google.com`, `www.google.com`, `googleusercontent.com`, `*.googleusercontent.com`** for this type.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## httpBasicAuth

Sends an RFC 7617 Authorization header.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `user` | yes | no | — |  |
| `password` | yes | yes | — |  |

An empty allowed-domains list means **any host** for this type: scope it before giving it to a caller who can choose where a request goes.

This type answers `untestable` on the test endpoints — there is no probe that proves it.

## httpBearerAuth

Sends an Authorization: Bearer header.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `token` | yes | yes | — |  |

An empty allowed-domains list means **any host** for this type: scope it before giving it to a caller who can choose where a request goes.

This type answers `untestable` on the test endpoints — there is no probe that proves it.

## httpCustomAuth

Sends several headers and query parameters described by a JSON template, for example {"headers":{"X-Api-Key":"abc"},"qs":{"tenant":"acme"}}.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `json` | yes | yes | — | An object with optional headers and qs objects. Every value is sent as written; the body is not modifiable from here. |

An empty allowed-domains list means **any host** for this type: scope it before giving it to a caller who can choose where a request goes.

This type answers `untestable` on the test endpoints — there is no probe that proves it.

## httpHeaderAuth

Sends a fixed header, for example an API key.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | yes | no | — |  |
| `value` | yes | yes | — |  |

An empty allowed-domains list means **any host** for this type: scope it before giving it to a caller who can choose where a request goes.

This type answers `untestable` on the test endpoints — there is no probe that proves it.

## httpQueryAuth

Sends a fixed query parameter, for example ?api_key=….

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | yes | no | — |  |
| `value` | yes | yes | — |  |

An empty allowed-domains list means **any host** for this type: scope it before giving it to a caller who can choose where a request goes.

This type answers `untestable` on the test endpoints — there is no probe that proves it.

## imap

Reads a mailbox over IMAP. The field keys are n8n's own, so an imported workflow's credential shape survives the mapping.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `user` | yes | no | — |  |
| `password` | yes | yes | — |  |
| `host` | yes | no | — |  |
| `port` | no | no | `993` |  |
| `secure` | no | no | `true` | On (the default) connects with TLS from the start. Off connects in clear and upgrades with STARTTLS when the server offers it. |
| `allowUnauthorizedCerts` | no | no | `false` | Connect even when the server's certificate cannot be verified. |

An empty allowed-domains list means **any host** for this type: scope it before giving it to a caller who can choose where a request goes.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## jwtAuth

Holds the key that verifies incoming JSON Web Tokens.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `keyType` | yes | no | `passphrase` |  |
| `secret` | no | yes | — | The shared secret an HMAC-signed token was signed with. |
| `publicKey` | no | no | — | PEM-encoded public key (RSA or ECDSA) the token signature is checked against. |
| `privateKey` | no | yes | — | Kept so a payload written by n8n's JWT credential is stored unchanged; inbound verification reads the public key. |
| `algorithm` | yes | no | `HS256` |  |

An empty allowed-domains list means **any host** for this type: scope it before giving it to a caller who can choose where a request goes.

This type answers `untestable` on the test endpoints — there is no probe that proves it.

## mysql

Connects to a MySQL or MariaDB database you own.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `host` | yes | no | — |  |
| `port` | no | no | — | Defaults to 3306. |
| `database` | yes | no | — |  |
| `user` | yes | no | — |  |
| `password` | yes | yes | — |  |
| `tls` | no | no | `true` | true (the default) checks the server's certificate and host name; skip-verify encrypts without checking; preferred encrypts only when the server offers it, without checking; false sends everything in the clear. |

An empty allowed-domains list means **any host** for this type: scope it before giving it to a caller who can choose where a request goes.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## openAiApi

An OpenAI API key, or the key and address of an OpenAI-compatible endpoint.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `apiKey` | yes | yes | — | A key from platform.openai.com, in the form sk-…, or the key an OpenAI-compatible endpoint expects. |
| `url` | no | no | `https://api.openai.com/v1` | Leave it empty for OpenAI. Set it to reach an OpenAI-compatible endpoint, such as a gateway or a local Ollama at http://localhost:11434/v1. |

An empty allowed-domains list means **the host of `url`** for this type — the address its owner set.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## openRouterApi

An OpenRouter API key.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `apiKey` | yes | yes | — | A key from openrouter.ai, in the form sk-or-… |

An empty allowed-domains list means **`openrouter.ai`** for this type.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## postgres

Connects to a PostgreSQL database you own.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `host` | yes | no | — |  |
| `port` | no | no | — | Defaults to 5432. |
| `database` | yes | no | — |  |
| `user` | yes | no | — |  |
| `password` | yes | yes | — |  |
| `sslMode` | no | no | `verify-full` | verify-full (the default) checks the server's certificate and host name; verify-ca checks the certificate only; require encrypts without checking either; disable sends everything in the clear. |

An empty allowed-domains list means **any host** for this type: scope it before giving it to a caller who can choose where a request goes.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## smtp

Sends email through an SMTP server. The field keys are n8n's own, so an imported workflow's credential shape survives the mapping.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `user` | no | no | — |  |
| `password` | no | yes | — | Leave user and password empty for an internal relay that does not authenticate. |
| `host` | yes | no | — |  |
| `port` | no | no | `465` | Defaults to 465, the implicit-TLS port. |
| `secure` | no | no | `true` | On (the default) connects with TLS from the start, usually port 465. Off connects in clear and upgrades with STARTTLS when the server offers it, usually port 587. |
| `disableStartTls` | no | no | `false` | Leave the connection unencrypted when the credential's SSL/TLS is off. Only for a relay you know is reachable in the clear. |

An empty allowed-domains list means **any host** for this type: scope it before giving it to a caller who can choose where a request goes.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## sqlite

Opens a SQLite file in this tenant's own directory on the server. The file is created on first use if it does not exist.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `path` | yes | no | — | Path relative to this tenant's SQLite directory, for example orders.db or reports/q1.db. Absolute paths and .. are refused. |

An empty allowed-domains list means **any host** for this type: scope it before giving it to a caller who can choose where a request goes.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## telegramApi

A Telegram bot token from BotFather.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `accessToken` | yes | yes | — | The token BotFather gave you, in the form 123456:ABC-DEF… |
| `baseUrl` | no | no | `https://api.telegram.org` | Where the Bot API lives. Leave it empty unless you run Telegram's own local Bot API server. |

An empty allowed-domains list means **the host of `baseUrl`** for this type — the address its owner set.

The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.

## wahaApi

A self-hosted WAHA instance: its base URL and its API key.

| Field | Required | Secret | Default | Description |
| --- | --- | --- | --- | --- |
| `baseUrl` | yes | no | — | Where the WAHA instance is, for example https://waha.example.com. |
| `apiKey` | yes | yes | — |  |

An empty allowed-domains list means **the host of `baseUrl`** for this type — the address its owner set.

This type answers `untestable` on the test endpoints — there is no probe that proves it.

