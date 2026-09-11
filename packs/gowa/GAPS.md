# GOWA n8n node → KilasFlow mapping gaps

KilasFlow does **not** yet ship a full `packs/gowa` declarative pack (unlike WAHA).
Instead, `@aldinokemal2104/n8n-nodes-gowa.gowa` imports as `kilasflow.httpRequest`
against the [GOWA REST API](https://github.com/aldinokemal/go-whatsapp-web-multidevice).

Default base URL: `http://127.0.0.1:3000` (credentials never import — rewrite the URL
and attach auth after import).

## Mapped (import → HTTP)

| n8n resource | operation (empty = default) | HTTP |
|---|---|---|
| `app` | _(empty)_ / `devices` | `GET /app/devices` |
| `app` | `status` | `GET /app/status` |
| `app` | `info` | `GET /app/info` |
| `app` | `login` / `logout` / `reconnect` | `GET /app/{op}` |
| `send` / `message` | _(empty)_ / `message` / `text` | `POST /send/message` |
| `send` | `link` | `POST /send/link` |
| `send` | `presence` | `POST /send/presence` |
| `send` | `chatPresence` | `POST /send/chat-presence` |
| `device` | _(empty)_ / `list` / `get` | `GET /devices` |

The sample workflow `iNPSkvgNZE5VeltK.json` ("Get device information", `resource: app`)
imports as `GET http://127.0.0.1:3000/app/devices` — **no blocking unsupported**.

## Not mapped (lossy → placeholder HTTP)

- Media send (`image` / `audio` / `video` / `file` / `sticker`) — multipart bodies
- Message revoke / react / update / read
- Group / newsletter / chatwoot / call resources
- Device path ops (`/devices/{id}/login`, webhook config, …)
- Webhook **trigger** equivalent (GOWA pushes to your webhook; use `kilasflow.webhook`)

For unmapped resource/operation pairs the importer still emits an HTTP node aimed at
`GET /app/devices` (or `POST /send/message` under `send`) plus a **lossy** diagnostic
pointing here — not a blocking `kilasflow.unsupported`.

## Future

A generated `packs/gowa` from GOWA's OpenAPI (same path as WAHA / `cmd/nodepackgen`)
would cover the full surface; this HTTP mapping is the minimum that unblocks the AGL suite.
