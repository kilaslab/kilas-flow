---
name: kilasflow-ai-agents
description: Use when building an AI agent, chat, LLM chain or RAG workflow — wiring a chat model, memory, tools, an output parser or a vector store into a workflow. Triggers on "agent", "AI", "LLM", "chat model", "tool", "memory", "RAG", "embeddings", "vector store".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow node list
  - kilasflow node describe
  - kilasflow node options
  - kilasflow workflow validate
  - kilasflow workflow create
  - kilasflow credential create
  - kilasflow run
  - kilasflow exec trace
kilasflow_operations:
  - list-node-types
  - load-node-property-options
  - validate-workflow-document
  - create-workflow
  - create-credential
  - run-workflow
  - stream-execution-events
kilasflow_nodes:
  - kilasflow.agent
  - kilasflow.chainLlm
  - kilasflow.chatTrigger
  - kilasflow.chatModel
  - kilasflow.lmChatOpenAi
  - kilasflow.lmChatOpenRouter
  - kilasflow.memoryBuffer
  - kilasflow.outputParser
  - kilasflow.calculatorTool
  - kilasflow.documentLoader
  - kilasflow.textSplitter
  - kilasflow.embeddings
  - kilasflow.vectorStore
kilasflow_expression_roots:
  - $json
kilasflow_not_shipped: []
---

## Non-negotiables

1. Sub-nodes connect by AI kind, with the sub-node as the source and the agent as the target. A chat model's `model` port goes to the agent's `model` input as `ai_languageModel`, a memory to `memory` as `ai_memory`, a tool to `tools` as `ai_tool` and an output parser to `outputParser` as `ai_outputParser`; the connection's `kind` is spelled lowerCamel after the prefix. A model wired through a `main` connection is refused with "connection kind must match both endpoint ports" (code `port.incompatible`).
2. An agent takes exactly one model, at most one memory, at most one output parser and any number of tools. The `model` input is required and holds a maximum of one connection, so an agent with no model, or with two, does not compile.
3. A model's API key is a credential referenced by id, never a parameter. The node names its credential type and the credential's id in `credentials`, as `{"openAiApi": "<credentialId>"}`, and the key itself lives only in the credential (`kilasflow credential create`, `create-credential`; the kilasflow-credentials skill). A key in `parameters` is stored in every revision and shown in every trace.
4. Run `kilasflow workflow validate --file wf.json` (`validate-workflow-document`) before you save, and only then `kilasflow workflow create --file wf.json` (`create-workflow`). Validation compiles the graph the way activation does, so a wrong port, a missing credential or a second model is a diagnostic with a path instead of a run that fails after it has called the provider.
5. Never list the tools from memory. The set of tools is what the node catalogue says it is today, and it grows: ask for it with `kilasflow node list` (`list-node-types`), as in `kilasflow node list --json | jq -r '.data.items[] | select(.category != "Imported" and any(.outputs[]?; .kind == "ai_tool")) | .type'`, then read one with `kilasflow node describe <type>`. A node can also become a tool by mode, so a node whose `mode` or `operation` options mention a tool is read with `node describe` as well.

## Strong defaults

- Start from the validated example: `kilasflow skills show kilasflow-ai-agents --reference EXAMPLE.md` is a chat agent with a model, a memory and a calculator tool. For retrieval-augmented work start from `--reference RAG.md`, and for the choice of model and its credential from `--reference MODELS.md`.
- The usual shape is `kilasflow.chatTrigger` into `kilasflow.agent`, with the model, the memory and the tools wired into the agent's own inputs. A manual run of such a workflow takes the item the chat trigger would carry: `kilasflow run <workflowId> --wait --input '{"chatInput":"hello","sessionId":"s1"}'` (`run-workflow`), and `kilasflow exec trace <executionId>` (`stream-execution-events`) shows what each node did.
- Store the prompt as an expression marker, `{"mode": "expression", "value": "{{ $json.chatInput }}"}`. A plain string that merely contains braces is sent to the model as text, so the model would be asked a question about `{{ $json.chatInput }}`.
- Write a memory node's `sessionKey` yourself, as the marker `{"mode": "expression", "value": "{{ $json.sessionId }}"}`. The editor copies that default onto a freshly placed node, but a document written by hand without it is refused with "sessionKey is required", and a literal key would put every conversation in one bucket.
- Pick the model node by where the model lives. `kilasflow.lmChatOpenAi` is OpenAI with an `openAiApi` credential, `kilasflow.lmChatOpenRouter` is OpenRouter with an `openRouterApi` credential, and `kilasflow.chatModel` is any OpenAI-compatible endpoint, reached through its `baseUrl` with an `httpBearerAuth` credential. The model's reasoning effort, temperature and token limit are the `options` collection; a field you do not add is not sent. The models a provider offers are loaded, not remembered, through `kilasflow node options <type> --property model --param baseUrl=<address>` (`load-node-property-options`), which passes the address the loader needs. See references/MODELS.md.
- A local model such as Ollama is a `kilasflow.chatModel` whose `baseUrl` is the local `/v1` address. It needs an `httpBearerAuth` credential holding any non-empty placeholder token, because the credential API refuses an empty field and the local server ignores the header, and the operator must list that one endpoint in `outbound.allowed_private_endpoints`. Never ask for `outbound.allow_private_networks`: it opens every private address to every workflow.
- Use `kilasflow.chainLlm` for a single-shot call: one prompt per item through a model, with an optional output parser and no tools or memory. Use the agent when the model must choose tools or remember the conversation.
- Use `kilasflow.outputParser` when the answer must be JSON of a known shape: give it a `jsonSchema` or an `exampleJson` and wire it to the agent's or chain's `outputParser` input. It retries a malformed answer up to `maxRetries` times.
- Give every tool a name and a description the model can act on, as the example's `kilasflow.calculatorTool` does. `toolDescription` is the only thing the model reads about a tool, and a tool whose description is vague is a tool the model uses wrongly.
- Bound the loop. The agent's `maxIterations` caps the model-and-tool rounds, and its output item carries `output`, `iterations`, `toolCalls` and `usage`, so a cost question is answered from the run rather than guessed. `returnIntermediateSteps` adds the whole message list, tool turns included, for debugging.
- The vector store needs PostgreSQL with pgvector. `kilasflow.vectorStore` is unavailable on SQLite, and `workflow validate` says so as a diagnostic on that node; `kilasflow.embeddings` calls an OpenAI-compatible HTTP API and runs on either database; read references/RAG.md for the ingest graph (a `kilasflow.documentLoader` fed by a `kilasflow.textSplitter`, and `kilasflow.embeddings`, into the store) and ask the node itself for the query side with `kilasflow node describe kilasflow.vectorStore`.
- A workflow offered to an agent as a tool, and every other tool variant, is found through the discovery in the last non-negotiable and read with `node describe`, not written from a remembered parameter list.

## Decision tree

```
what should the model do?
|
+-- hold a conversation, remember it, and call tools
|     -> kilasflow.chatTrigger -> kilasflow.agent
|        + a model (ai_languageModel), kilasflow.memoryBuffer (ai_memory), tools (ai_tool)
|        start from references/EXAMPLE.md
|
+-- answer one prompt per item, nothing more
|     -> kilasflow.chainLlm with a model
|
+-- return structured JSON, not prose
|     -> add kilasflow.outputParser (ai_outputParser) to the agent or the chain
|
+-- act on systems (read a table, call an API, run another workflow)
|     -> a tool node wired to the agent's tools input
|        list them: kilasflow node list --json | jq (non-negotiable 5)
|
+-- answer from the customer's own documents (RAG)
|     -> ingest: documentLoader + textSplitter + embeddings + vectorStore (references/RAG.md)
|        query: kilasflow node describe kilasflow.vectorStore
|
+-- which model, which credential, a local server?
|     -> references/MODELS.md, then kilasflow credential create --file cred.json --yes
|
+-- the graph is refused
      -> kilasflow workflow validate --file wf.json and read each diagnostic's path:
         a wrong kind, a missing credential, a second model, a missing sessionKey
```

## Anti-patterns

- "The model is just another node, so I will connect it with `main`" → the model's port has the kind `ai_languageModel`, and the agent's input of that name accepts nothing else → connect the model's `model` port to the agent's `model` input with a connection of kind `ai_languageModel`.
- "I will put the API key in the node so it works" → the key is then in the document, every revision, every export and every trace → create a credential and put its id in `credentials`.
- "I know the tools, I will write them in" → the catalogue changes between versions and the agent's tool set is whatever you wired, so a remembered list is a list of nodes that may not exist or may have a different name → discover them with `node list` and read each with `node describe`.
- "The prompt is `{{ $json.chatInput }}`" → a plain string with braces is data, so the model is asked the literal text → store the prompt as the expression marker.
- "The local model refuses to connect, so I will turn on allow_private_networks" → that opens every private address to every workflow on the instance → list the one endpoint in `outbound.allowed_private_endpoints` and leave the policy flag off.
- "Two models will give a better answer" → the agent's `model` input holds one connection and refuses a second → run two agents and merge, or choose one.
- "The agent will remember without a session key" → memory is keyed by `sessionKey`, and a missing one is refused while a constant one shares a conversation across users → store `{{ $json.sessionId }}` as an expression.
- "I will activate the workflow to try the chat" → activation publishes an endpoint and is the user's decision → `kilasflow run <workflowId> --wait --input '{"chatInput":"…","sessionId":"s1"}'` and read `kilasflow exec trace <executionId>`.
- "It validated, so the model will answer" → validation proves the graph compiles, not that the credential works or the provider answers → `kilasflow credential test <credentialId>`, then one real run.

## Reference files

| File | Read when |
| --- | --- |
| EXAMPLE.md | you are building a chat agent and want a validated document with a model, a memory and a tool wired by the right connection kinds |
| RAG.md | the agent must answer from documents: the ingest graph, the ports each node shows, and where to read the query side |
| MODELS.md | you are choosing a model node, creating its credential, pointing at a local or OpenAI-compatible server, or tuning reasoning effort, tokens and timeouts |
