# Telegram node pack

Hand-written, not generated. Telegram publishes its API as HTML documentation
rather than an OpenAPI document, and the community-maintained specs that do
exist are unvetted third-party artifacts — for twenty-three operations with
stable parameter names, metadata reviewed once is cheaper and safer than
importing a conversion of somebody else's transcription.

Hand-written but still **declarative**: `pack.json` runs on the same routing
interpreter the generated packs do, so it inherits the egress policy, the
credential host scope and the multipart upload path rather than growing its own
executor. There is no Go in this package beyond registration.

## The values are reconstructed

`resource` and `operation` values are what an imported workflow matches against
by literal string, and this pack's were reconstructed from n8n-nodes-base's
naming convention plus the labels visible in `design-refs/n8n-v2/`, because the
reference checkout does not contain the Telegram node. They are pinned by
`TestEveryResourceAndOperationInTheTicketIsRegistered`, so correcting one is a
one-line diff in two places rather than an archaeology exercise.

Verify them against `packages/nodes-base/nodes/Telegram` when the sparse
checkout is widened.

## The token is in the path

The Bot API authenticates with the token in the URL —
`/bot<token>/sendMessage` — which no header or query placement can express. The
pack writes `{credential.accessToken}` and `internal/credentials` substitutes it
inside the package that already holds the secret. It is deliberately *not*
`{{ $credentials.accessToken }}`: that root carries non-secret fields only and
is meant to keep carrying only those.

## The trigger is not here

`kilasflow.telegramTrigger` is a built-in node in `nodes/`, because its
registration, its file downloads and its polling mode are behaviour rather than
data. Both use the same `telegramApi` credential, so one bot needs one
credential.
