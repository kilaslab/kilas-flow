# Choosing a model node and its credential

Three node types supply a chat model to an agent or a chain. All of them have one
output, `model`, of kind `ai_languageModel`, and none has an input. They differ
in where the model lives and which credential reaches it.

| Node | Model lives at | Credential type | Model is stored as |
| --- | --- | --- | --- |
| `kilasflow.lmChatOpenAi` | OpenAI | `openAiApi` | a resource locator, `{ "__rl": true, "mode": "list", "value": "gpt-5-mini" }` |
| `kilasflow.lmChatOpenRouter` | OpenRouter's catalogue | `openRouterApi` | a resource locator, `{ "__rl": true, "mode": "list", "value": "openai/gpt-4.1-mini" }` |
| `kilasflow.chatModel` | any OpenAI-compatible endpoint, by `baseUrl` | `httpBearerAuth` | a string, `"model": "llama3.1"` |

Read the live definition before you write one: `kilasflow node describe
kilasflow.lmChatOpenAi` prints every key, default and option, and the catalogue is
the authority if this table and it disagree.

## The credential

The key is a credential, referenced by id under its type:
`"credentials": { "openAiApi": "<credentialId>" }`. Create it once, with the
owner supplying the value into a body file that is never printed or committed:

```json kilasflow-request create-credential
{
  "name": "OpenAI production key",
  "type": "openAiApi",
  "fields": {
    "apiKey": "<the key, supplied by its owner>"
  },
  "allowedDomains": ["api.openai.com"]
}
```

```bash
kilasflow credential create --file openai.json --yes
kilasflow credential test <credentialId>
```

- `openAiApi` has an optional `url` field for a gateway; `openRouterApi` has only
  `apiKey`. A credential can only be sent to its `allowedDomains`, and an empty
  list means the type's own host (`api.openai.com`, `openrouter.ai`).
- `credential test` calls the provider with the stored key and answers pass or
  fail. It tests the credential, not the model name, the prompt or your quota.
- A node that names a credential of the wrong type is refused when the graph
  compiles, naming the types it accepts.

## A local or self-hosted model

A server that speaks the OpenAI chat-completions protocol (Ollama, LM Studio,
vLLM, a gateway) is a `kilasflow.chatModel`:

- Set `baseUrl` to the server's `/v1` address, for example
  `http://127.0.0.1:11434/v1`, and `model` to the name the server lists.
- It needs an `httpBearerAuth` credential. The credential API refuses an empty
  `token`, and a local server ignores the header, so store any non-empty
  placeholder as the token.
- The default egress policy refuses loopback and private addresses, so the
  operator grants exactly that endpoint in the server's configuration with
  `outbound.allowed_private_endpoints`, a list of `host:port` entries
  (`KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS` in the environment). The port
  is part of the grant and there are no wildcards. Never ask for
  `outbound.allow_private_networks`: it opens every private address to every
  workflow, and the policy is the thing that keeps a tenant's base URL away from
  the host's internals.
- A slow local model needs `options.timeout` raised (milliseconds); a value
  above the deployment's ceiling is refused rather than quietly lowered.
- List the models the server offers with the escape hatch, because the
  `node options` verb sends no parameters and so cannot pass a base URL:
  `kilasflow api load-node-property-options --path type=kilasflow.chatModel
  --body @probe.json` with the body
  `{"property": "model", "parameters": {"baseUrl": "http://127.0.0.1:11434/v1"}, "credentialId": "<credentialId>"}`
  answers the endpoint's own model list. The two provider nodes carry a default
  address, so for them `kilasflow node options <type> --property model
  --credential <credentialId>` is enough.

## Tuning

Everything under `options` is optional, and an option you do not add is not sent,
so the provider's own default applies.

- `reasoningEffort` (`none`, `low`, `medium`, `high`) sets how long a reasoning
  model may think. `none` turns thinking off, which makes a local thinking model
  many times faster.
- `temperature` lower is less random, and `0` is sent as zero rather than
  treated as unset. Change this or `topP`, not both.
- `maxTokens` caps one completion; `-1` leaves it to the model. `maxRetries`
  resends a rate-limited or failed request with backoff.
- The agent's own `maxIterations` bounds the model-and-tool rounds, and the
  output's `usage` shows what a run spent.

## When the model is the problem

- `credential test` fails: the key, the `url` or the allowed domains are wrong;
  fix the credential before the workflow.
- The run fails at the model node with a policy message about the address: the
  endpoint is not granted. Ask the operator for the `allowed_private_endpoints`
  entry; do not work around it.
- The agent answers without calling a tool the model should have called: the
  model may not support tool calls. A model that returns OpenAI-format
  `tool_calls` works; read the trace (`kilasflow exec get <executionId> --json`)
  to see whether any call was made.
