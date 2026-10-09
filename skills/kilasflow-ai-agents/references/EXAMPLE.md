# A validated chat agent

A chat trigger feeding an agent, with a model, a memory and one tool wired into
the agent's own inputs. It is the document `kilasflow workflow validate` accepted
when this file was tested, so copy it and change the model, the prompt and the
tool rather than wiring one from memory. Replace `<credentialId>` with the id of
an `openAiApi` credential from `kilasflow credential list` (create one with the
kilasflow-credentials skill; the key is never written in the document).

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Support chat agent",
  "nodes": [
    {
      "id": "chat",
      "name": "When chat message received",
      "type": "kilasflow.chatTrigger",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 }
    },
    {
      "id": "agent",
      "name": "AI Agent",
      "type": "kilasflow.agent",
      "typeVersion": 1,
      "position": { "x": 320, "y": 0 },
      "parameters": {
        "prompt": { "mode": "expression", "value": "{{ $json.chatInput }}" },
        "systemMessage": "You answer questions from customers of Acme. Use the calculator for any arithmetic, and say so when you do not know.",
        "maxIterations": 6
      }
    },
    {
      "id": "model",
      "name": "OpenAI Chat Model",
      "type": "kilasflow.lmChatOpenAi",
      "typeVersion": 1,
      "position": { "x": 200, "y": 240 },
      "parameters": {
        "model": { "__rl": true, "mode": "list", "value": "gpt-5-mini" },
        "options": { "temperature": 0.2 }
      },
      "credentials": { "openAiApi": "<credentialId>" }
    },
    {
      "id": "memory",
      "name": "Window memory",
      "type": "kilasflow.memoryBuffer",
      "typeVersion": 1,
      "position": { "x": 360, "y": 240 },
      "parameters": {
        "sessionIdType": "fromInput",
        "sessionKey": { "mode": "expression", "value": "{{ $json.sessionId }}" },
        "maxMessages": 20
      }
    },
    {
      "id": "calc",
      "name": "Calculator",
      "type": "kilasflow.calculatorTool",
      "typeVersion": 1,
      "position": { "x": 520, "y": 240 },
      "parameters": {
        "toolName": "calculator",
        "toolDescription": "Evaluates an arithmetic expression such as 12 * (3 + 4) and returns the number."
      }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "chat", "port": "main" },
      "target": { "nodeId": "agent", "port": "main" }
    },
    {
      "id": "c2",
      "kind": "ai_languageModel",
      "source": { "nodeId": "model", "port": "model" },
      "target": { "nodeId": "agent", "port": "model" }
    },
    {
      "id": "c3",
      "kind": "ai_memory",
      "source": { "nodeId": "memory", "port": "memory" },
      "target": { "nodeId": "agent", "port": "memory" }
    },
    {
      "id": "c4",
      "kind": "ai_tool",
      "source": { "nodeId": "calc", "port": "tool" },
      "target": { "nodeId": "agent", "port": "tools" }
    }
  ],
  "settings": {}
}
```

## How the wiring reads

| Connection | Kind | From (source) | To (target) |
| --- | --- | --- | --- |
| `c1` | `main` | the chat trigger's `main` | the agent's `main` |
| `c2` | `ai_languageModel` | the model's `model` | the agent's `model` (required, one) |
| `c3` | `ai_memory` | the memory's `memory` | the agent's `memory` (at most one) |
| `c4` | `ai_tool` | the tool's `tool` | the agent's `tools` (any number) |

The sub-node is always the source and the agent the target, and the `kind` names
the channel the two ports share. The chat trigger and the agent are the only
`main` connection; the three sub-nodes have no `main` port at all, so wiring
them with `main` is refused.

## What each part is for

- **The prompt is an expression marker.** `{ "mode": "expression", "value":
  "{{ $json.chatInput }}" }` reads the message the chat trigger carries; the same
  text as a plain string would be sent to the model verbatim.
- **The model is a locator.** `lmChatOpenAi` stores its model as `{ "__rl": true,
  "mode": "list", "value": "…" }`, and `options` holds only what you add: a field
  that is absent is not sent. Its credential is the `openAiApi` entry in
  `credentials`, by id.
- **The memory needs a key.** `sessionKey` is stored as the expression
  `{{ $json.sessionId }}`; with `sessionIdType: "fromInput"` the key is scoped to
  this memory node, so two memory nodes in one workflow do not share a
  conversation by accident.
- **The tool is described for the model.** `toolName` and `toolDescription` are
  the only thing the model reads about the calculator.
- **The agent is bounded.** `maxIterations` caps the rounds of model and tool
  calls, and the output item carries `output`, `iterations`, `toolCalls` and
  `usage`.

## Check it

```bash
kilasflow workflow validate --file agent.json
kilasflow workflow create --file agent.json --quiet
kilasflow credential test <credentialId>
kilasflow run <workflowId> --wait --input '{"chatInput":"What is 12 * 7?","sessionId":"s1"}'
kilasflow exec trace <executionId>
```

`validate` proves the graph compiles. `credential test` calls the provider's
models endpoint with the stored key, which is the only proof the credential
works, and the manual run takes the same item a chat message would carry. Do not
activate the workflow to try it: activation is the user's decision.
