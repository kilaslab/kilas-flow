# Files and binary payloads

A file in a workflow is never inside the item. An item holds its fields in `json`
and, beside them, a `binary` map of references: a name, a file name, a media type
and a size, and an `id` that points at bytes the server keeps outside the run
record (docs: concepts/items-and-lineage). That is why a trace of a run that moved
a multi-megabyte PDF is a few hundred bytes, and why a node reads a file by the
property name it was stored under rather than by value.

## The reference an item carries

```json
{
  "json": {},
  "binary": {
    "data": { "id": "bin_…", "fileName": "handbook.pdf", "mediaType": "application/pdf", "size": 48213 }
  }
}
```

- The key (`data` here) is the **binary property name**. Nodes that read or write
  a file ask for it by name, and the default is `data`.
- Only this metadata appears in the execution record, an API response and a log.
  Nothing in a document, a trace or `exec get` ever shows the bytes.
- Writes are bounded by the instance's `binary.max_bytes` (16 MiB by default) and
  an oversized payload is refused, not truncated.
- The reference travels with the item through the nodes that pass it on: the
  Extract node below leaves `binary.data` in place beside the text it adds.

## Download a file, then read its text

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Download a PDF and read its text",
  "nodes": [
    {
      "id": "start",
      "name": "Manual Trigger",
      "type": "kilasflow.manual",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 }
    },
    {
      "id": "fetch",
      "name": "Download PDF",
      "type": "kilasflow.httpRequest",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "method": "GET",
        "url": "https://files.example.com/handbook.pdf",
        "responseFormat": "file",
        "outputPropertyName": "data"
      }
    },
    {
      "id": "read",
      "name": "Extract text",
      "type": "kilasflow.extractFromFile",
      "typeVersion": 1,
      "position": { "x": 480, "y": 0 },
      "parameters": {
        "operation": "pdf",
        "binaryPropertyName": "data",
        "destinationKey": "text"
      }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "start", "port": "main" },
      "target": { "nodeId": "fetch", "port": "main" }
    },
    {
      "id": "c2",
      "kind": "main",
      "source": { "nodeId": "fetch", "port": "main" },
      "target": { "nodeId": "read", "port": "main" }
    }
  ],
  "settings": {}
}
```

- **`responseFormat: "file"`** makes the HTTP Request node store the body as a
  binary property instead of parsing it into `json`; `outputPropertyName` names
  that property. The item's `json` stays empty.
- **`extractFromFile`** reads the property named by `binaryPropertyName` and
  writes the result to `destinationKey` in `json`. The operations are `pdf`
  (the text layer only), `text` (UTF-8), `json`, `csv` and `xlsx` (one item per
  row, the columns as fields) and `binaryToProperty` (the file converted into a
  `json` property). An image-only scan yields empty text: the node does not run
  OCR.
- **The result is ordinary data.** After the Extract node, `{{ $json.text }}` is
  a string like any other, and the binary reference is still on the item for a
  later node that wants the file itself.
- **The host must be reachable.** The default egress policy refuses loopback and
  private addresses, and a download is a request like any other; a credential on
  the HTTP Request node authenticates it.

## Check it

```bash
kilasflow workflow validate --file read-pdf.json
kilasflow workflow create --file read-pdf.json --quiet
kilasflow run <workflowId> --wait
kilasflow exec get <executionId> --json
```

In the trace the `Download PDF` node run has `binary.data` with an `id`, a
`mediaType` and a `size`, and the `Extract text` node run has the same reference
plus `json.text`. If the text is empty, the first suspect is a scanned PDF; if the
download node fails, read its error before blaming the extraction.
