# Retrieval-augmented generation: the ingest graph

A retrieval workflow has two halves that never share a run. The ingest half reads
documents, splits them into chunks, embeds each chunk and stores the vectors in a
collection. The query half embeds a question, finds the nearest chunks and gives
them to a model, usually as a tool an agent may call. This file validates the
ingest half; for the query half, ask the node itself, because that side of the
vector store is the one that changes:

```bash
kilasflow node describe kilasflow.vectorStore
```

Read its `mode` options and, for each mode, which input and output ports the node
shows. A mode that hands the store to an agent as a tool exists; its ports are
the ones to wire, and nothing here should be copied over them.

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

## The validated graph

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

## How the ports line up

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

## Check it

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
