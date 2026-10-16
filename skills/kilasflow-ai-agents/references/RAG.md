# Retrieval-augmented generation: the ingest and query graphs

A retrieval workflow has two halves that never share a run. The ingest half reads
documents, splits them into chunks, embeds each chunk and stores the vectors in a
collection. The query half hands that collection to an agent as a tool: the agent
decides when to ask, the tool embeds the question, finds the nearest chunks and
has its own chat model answer from them. This file validates both halves, the
ingest graph first and the query graph after it ("The validated query graph"
below). The vector store changes more than any other AI node, so when you change
a mode or an option, read the node again and wire the ports it now shows:

```bash
kilasflow node describe kilasflow.vectorStore
kilasflow node describe kilasflow.vectorStoreTool
```

## What it needs

- **PostgreSQL with the pgvector extension, for the vector store.**
  `kilasflow.vectorStore` is registered on every install but is unavailable on
  SQLite, and `workflow validate` reports that as a diagnostic on the vector
  store node ("needs PostgreSQL with the pgvector extension"). The embeddings
  node is not affected: it calls an OpenAI-compatible HTTP API and runs on either
  database, and everything else in the graph below validates on SQLite. Do not
  edit the graph to make the diagnostic go away, change the database. On PostgreSQL the extension has to
  exist when the server boots (`CREATE EXTENSION vector`, or the pinned pgvector
  image), or the same diagnostic names the missing extension.
- **An embedding model of a supported width.** The store keeps one table per
  width, 384, 768, 1024 and 1536 dimensions, and refuses a vector of any other
  width at insert with a message naming those four. `text-embedding-3-small` is
  1536 wide; a local model is usually 384 or 768.
- **An embeddings credential.** `kilasflow.embeddings` calls an OpenAI-compatible
  embeddings endpoint and accepts an `openAiApi`, `openRouterApi` or
  `httpBearerAuth` credential, referenced by id. Its `baseUrl` points at a
  compatible server, under the same egress rules as a chat model (see MODELS.md).
- **A collection name.** The vector store's `collection` groups the chunks that
  belong together; a query reads one collection.

## The validated ingest graph

Five nodes feed the vector store. The text source is a Set node so the example
needs no file; in a real workflow it is an HTTP Request, an Extract From File or a
trigger carrying the text, and nothing downstream changes.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Ingest a handbook into a vector store",
  "nodes": [
    {
      "id": "start",
      "name": "Manual Trigger",
      "type": "kilasflow.manual",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 }
    },
    {
      "id": "source",
      "name": "Handbook text",
      "type": "kilasflow.set",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "assignments": {
          "assignments": [
            {
              "id": "a1",
              "name": "text",
              "type": "string",
              "value": { "mode": "expression", "value": "{{ $json.text }}" }
            }
          ]
        }
      }
    },
    {
      "id": "loader",
      "name": "Document loader",
      "type": "kilasflow.documentLoader",
      "typeVersion": 1,
      "position": { "x": 480, "y": 0 },
      "parameters": { "textField": "text" }
    },
    {
      "id": "splitter",
      "name": "Text splitter",
      "type": "kilasflow.textSplitter",
      "typeVersion": 1,
      "position": { "x": 480, "y": 200 },
      "parameters": { "chunkSize": 800, "chunkOverlap": 100 }
    },
    {
      "id": "embed",
      "name": "Embeddings",
      "type": "kilasflow.embeddings",
      "typeVersion": 1,
      "position": { "x": 720, "y": 200 },
      "parameters": { "mode": "cluster", "model": "text-embedding-3-small" },
      "credentials": { "openAiApi": "<credentialId>" }
    },
    {
      "id": "store",
      "name": "Vector store",
      "type": "kilasflow.vectorStore",
      "typeVersion": 1,
      "position": { "x": 720, "y": 0 },
      "parameters": { "mode": "insert", "collection": "handbook" }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "start", "port": "main" },
      "target": { "nodeId": "source", "port": "main" }
    },
    {
      "id": "c2",
      "kind": "main",
      "source": { "nodeId": "source", "port": "main" },
      "target": { "nodeId": "loader", "port": "main" }
    },
    {
      "id": "c3",
      "kind": "ai_textSplitter",
      "source": { "nodeId": "splitter", "port": "splitter" },
      "target": { "nodeId": "loader", "port": "splitter" }
    },
    {
      "id": "c4",
      "kind": "ai_document",
      "source": { "nodeId": "loader", "port": "document" },
      "target": { "nodeId": "store", "port": "document" }
    },
    {
      "id": "c5",
      "kind": "ai_embedding",
      "source": { "nodeId": "embed", "port": "embedding" },
      "target": { "nodeId": "store", "port": "embedding" }
    }
  ],
  "settings": {}
}
```

## How the ingest ports line up

| Connection | Kind | From (source) | To (target) |
| --- | --- | --- | --- |
| `c2` | `main` | the Set node's `main` | the loader's `main` |
| `c3` | `ai_textSplitter` | the splitter's `splitter` | the loader's `splitter` |
| `c4` | `ai_document` | the loader's `document` | the store's `document` |
| `c5` | `ai_embedding` | the embeddings node's `embedding` | the store's `embedding` |

- **The loader is the only `main` input.** It turns each incoming item's
  `textField` into a document, optionally cutting it with the splitter, and hands
  the documents to the store on the `ai_document` channel. The store has no `main`
  input in `insert` mode: it runs when its documents arrive.
- **`mode: "cluster"` on the embeddings node** is what removes its `main` input
  and leaves only the `embedding` output, which is how it sits under a store. It
  is not listed by `node describe`, but the node reads it to decide its ports.
  Without it the node expects items to embed and shows a `main` input too.
- **Ports follow the mode.** The vector store's ports are computed from its
  `mode`, so after you change the mode, read `node describe` again and wire the
  ports it now shows.
- **Chunks are sized in characters.** `chunkSize` and `chunkOverlap` set the
  window and the repeat between windows; a chunk that cuts a sentence in half
  retrieves worse than one that does not.

## Check the ingest

```bash
kilasflow credential create --file embeddings.json --yes
kilasflow workflow validate --file ingest.json
kilasflow workflow create --file ingest.json --quiet
kilasflow run <workflowId> --wait --input '{"text":"Our refund window is 30 days."}'
kilasflow exec get <executionId> --json
```

A successful run ends with the vector store's node run holding one output item
per stored chunk, each `{ "collection": "handbook", "id": "vecdoc_…" }`; a text
longer than `chunkSize` therefore produces several items, and an empty list
means nothing was stored. The document text leaves the installation for the
embeddings provider, so the credential's `allowedDomains` should name only that
provider. Run the query side against the same `collection`, with an embeddings
node of the same model and width, or the nearest-neighbour search compares
vectors that were never meant to be compared.

## The validated query graph

The query half is an agent that owns a Vector Store Tool. The tool has two
sub-nodes of its own: a vector store in `retrieve` mode, which says where the
documents live, and a chat model, which writes the answer from the chunks that
come back. The store in turn has the embeddings node that embeds the question.
Nothing in this graph reads or writes the ingest run; it reads the collection the
ingest run filled.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Answer questions from the handbook",
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
        "systemMessage": "You answer employee questions about the company handbook. Ask the handbook tool before you answer, and say so when it does not know.",
        "maxIterations": 6
      }
    },
    {
      "id": "agentModel",
      "name": "Agent chat model",
      "type": "kilasflow.lmChatOpenAi",
      "typeVersion": 1,
      "position": { "x": 160, "y": 240 },
      "parameters": {
        "model": { "__rl": true, "mode": "list", "value": "gpt-5-mini" }
      },
      "credentials": { "openAiApi": "<chatCredentialId>" }
    },
    {
      "id": "handbookTool",
      "name": "Handbook tool",
      "type": "kilasflow.vectorStoreTool",
      "typeVersion": 1,
      "position": { "x": 480, "y": 240 },
      "parameters": {
        "toolName": "handbook",
        "toolDescription": "Answers a question about company policy, such as refunds, leave and expenses, from the employee handbook. Pass one complete question.",
        "topK": 4
      }
    },
    {
      "id": "toolModel",
      "name": "Tool chat model",
      "type": "kilasflow.lmChatOpenAi",
      "typeVersion": 1,
      "position": { "x": 360, "y": 480 },
      "parameters": {
        "model": { "__rl": true, "mode": "list", "value": "gpt-5-mini" }
      },
      "credentials": { "openAiApi": "<chatCredentialId>" }
    },
    {
      "id": "store",
      "name": "Handbook store",
      "type": "kilasflow.vectorStore",
      "typeVersion": 1,
      "position": { "x": 600, "y": 480 },
      "parameters": { "mode": "retrieve", "collection": "handbook" }
    },
    {
      "id": "embed",
      "name": "Embeddings",
      "type": "kilasflow.embeddings",
      "typeVersion": 1,
      "position": { "x": 600, "y": 700 },
      "parameters": { "mode": "cluster", "model": "text-embedding-3-small" },
      "credentials": { "openAiApi": "<embeddingsCredentialId>" }
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
      "source": { "nodeId": "agentModel", "port": "model" },
      "target": { "nodeId": "agent", "port": "model" }
    },
    {
      "id": "c3",
      "kind": "ai_tool",
      "source": { "nodeId": "handbookTool", "port": "tool" },
      "target": { "nodeId": "agent", "port": "tools" }
    },
    {
      "id": "c4",
      "kind": "ai_languageModel",
      "source": { "nodeId": "toolModel", "port": "model" },
      "target": { "nodeId": "handbookTool", "port": "model" }
    },
    {
      "id": "c5",
      "kind": "ai_vectorStore",
      "source": { "nodeId": "store", "port": "vectorStore" },
      "target": { "nodeId": "handbookTool", "port": "vectorStore" }
    },
    {
      "id": "c6",
      "kind": "ai_embedding",
      "source": { "nodeId": "embed", "port": "embedding" },
      "target": { "nodeId": "store", "port": "embedding" }
    }
  ],
  "settings": {}
}
```

Replace `<chatCredentialId>` with the id of an `openAiApi` credential from
`kilasflow credential list` and `<embeddingsCredentialId>` with the embeddings
credential the ingest run used; both are referenced by id and never written as a
key.

## How the query ports line up

| Connection | Kind | From (source) | To (target) |
| --- | --- | --- | --- |
| `c1` | `main` | the chat trigger's `main` | the agent's `main` |
| `c2` | `ai_languageModel` | the agent model's `model` | the agent's `model` |
| `c3` | `ai_tool` | the Vector Store Tool's `tool` | the agent's `tools` |
| `c4` | `ai_languageModel` | the tool model's `model` | the tool's `model` |
| `c5` | `ai_vectorStore` | the store's `vectorStore` | the tool's `vectorStore` |
| `c6` | `ai_embedding` | the embeddings node's `embedding` | the store's `embedding` |

- **The tool has its own model.** `c4` is separate from `c2`: the agent's model
  decides when to call the tool and what to ask, and the tool's model reads the
  retrieved chunks and writes the answer the agent receives. They may be the same
  model or different ones, but each port takes one connection, so each needs its
  own node. The tool's calls are billed inside the agent's run, in the agent's
  `usage`.
- **`mode: "retrieve"` is what turns the store into a source.** In that mode the
  store has no `main` input and no `document` input; its only input is
  `embedding`, and its only output is `vectorStore`. Nothing is searched at the
  store node: the tool embeds the question and searches when the agent calls it.
  The store's `mode` options also include a retrieve-as-tool mode, which hands the
  agent raw matches on a `tool` port instead and has no model of its own; read it
  with `node describe` if the agent should do the answering itself.
- **The collection and the embeddings must match the ingest.** The store's
  `collection` is the one the ingest run wrote, and the embeddings node is the same
  model, width and provider; the tool then compares like with like. Set `distance`
  on the store only to the value the collection was created with.
- **`topK` bounds the context.** It is a whole number the node's description
  gives the range of; a larger value sends more chunks to the tool's model on
  every question.
- **The tool is described for the agent.** `toolName` and `toolDescription` are
  all the agent reads about it. Say which questions it answers, so the agent calls
  it for those and not for arithmetic. If the agent should be able to use more
  than the handbook, wire more tools to `tools`; discover them with `node list`
  as the skill describes, and do not copy a tool list from this file.

## Check the query

```bash
kilasflow workflow validate --file query.json
kilasflow workflow create --file query.json --quiet
kilasflow credential test <chatCredentialId>
kilasflow run <workflowId> --wait --input '{"chatInput":"What is the refund window?","sessionId":"s1"}'
kilasflow exec trace <executionId>
```

`validate` proves the graph compiles, and on SQLite it also reports the vector
store's missing pgvector as a diagnostic (see "What it needs"). The trace shows
the agent's tool call and the answer the tool returned; a tool result of "No
stored document matches this question." means the collection is empty or the
embeddings do not match the ones the ingest used. The question and the retrieved
chunks leave the installation for both providers, so each credential's
`allowedDomains` should name only its own provider.
