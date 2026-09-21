# What a session may call

The authority half of an embed session: the middleware's per-request rule, the
confinement minted into the token, the stream ticket, and the failures an
integrator actually sees. Sources: internal/api/middleware/embed.go,
internal/api/handlers/embed.go and `embedscope.go`, internal/auth/session.go,
internal/api/handlers/auth.go.

## The per-request rule

The middleware answers one question — is this request inside this session's
authority — about what the request targets rather than about which handler will
run, and everything not permitted is refused by a real default arm rather than an
exclusion list (`permits`).

| Request | Result |
| --- | --- |
| the node catalogue and the credential list | needs `workflow:read` (the editor has to offer a picker) |
| that one workflow, read | needs `workflow:read` |
| that one workflow, saved | needs `workflow:write` |
| that one workflow, run | needs `workflow:run` |
| the executions of that workflow | needs `workflow:read`, with `workflowId` equal to the session's |
| one execution and its event stream | the scope is checked here, ownership in the handler |
| a different workflow, or listing every workflow | refused: scoped to a different workflow |
| activate, deactivate, delete, import | refused outright, at any scope |
| managing datastores, or another datastore's id | refused; another datastore reads as unknown |
| that datastore's definition and rows | `datastore:read` / `datastore:write` |
| anything else, including routes added tomorrow | refused |

## Confinement

A save, a publish, a restore and a run are checked against the **confinement**
minted into the token from the revision the workflow's owner published, so a
`workflow:write` session cannot introduce a node that reaches outside what that
revision referenced and thereby widen its own authority (`embedConfinementOf`,
`embedDocumentProblem`). The published revision is the source, and the latest
draft is the fallback for a workflow never activated, because a document cannot
be its own authority. The refusal is 403 and names each offending node.

## The stream ticket

`create-stream-ticket` takes an `executionId` and confirms the execution exists
**inside the caller's tenant** first, so a ticket can never be minted for a run
the caller could not already read.

- It is single-use: redemption is recorded when the stream opens and a second use
  is refused. That record is process-local, so the lifetime — 30 seconds at most,
  `MaxTicketTTL` — is what bounds a replay against another instance.
- It is spent as the `ticket` query parameter on `stream-execution-events`
  (`GET /executions/{id}/events`), the one operation the auth gate accepts a
  ticket for, and it is spent in the gate rather than the handler, so a replay
  never reaches a handler.
- It travels in a query string and therefore lands in every proxy access log on
  the way, which is why it lives seconds: mint one per connect, on demand, and
  cache nothing.

## Failure modes

| Symptom | Cause |
| --- | --- |
| the iframe never loads | no embed signing key (every token answers 503), or the page's origin is not in `embed.allowed_origins` |
| the frame loads, then answers 403 | the session's origin is not the page's origin, or the request targets outside the session's subject |
| every call answers 401 | the token expired, or none was sent |
| the session reads the wrong customer's data | it was minted with another tenant's key: the tenant comes from the credential, not from the page |
| a save is refused with a node named | the document reaches outside the confinement derived from the published revision |
| the first events call works and the second fails | the ticket is single-use; mint one per connect |
| rows appear under a tenant you deleted | an embed session issued before the deletion keeps its scopes until it expires; repeat the deletion afterwards |
