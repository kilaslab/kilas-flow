# Node pack generation report

- Source: `testdata/spec.json`
- SHA-256: `aeb79dce3e568d60daf5f56c67e9e1f1c687057a535edc266c632ec9b3748ca3`
- API: Fixture API 1.2.3
- Operations generated: 4

Everything listed below is **absent from the pack**.

## Operations left out (3)

- POST /api/either: request body is a oneOf/anyOf union, which has no single parameter set
- POST /api/files: request body is multipart/form-data, and only application/json can be generated
- GET /api/untagged: no tag, so it belongs to no resource

## Degraded or merged (3)

- GET /api/chats/{chatId}/messages parameter "X-Trace" is in "header", which the interpreter cannot place; the operation is generated without it
- POST /api/sendText body property "attachment": a oneOf/anyOf union has no single control; generated as free-form JSON
- Parameter "chatId" is declared as string and number by different operations; generated as a string.
