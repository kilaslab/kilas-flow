<!--
One change per pull request. Keep this description short but make it specific —
a reviewer holding the diff has no use for a restatement of it.
-->

## What this changes

## Why

<!--
The reasoning the diff cannot carry: what the alternative was, why this one won,
and what would break if someone changed it back.
-->

## How it was verified

<!-- The exact commands and what you observed. "Tests pass" is not a verification. -->

## Checklist

- [ ] `make lint` and `make test` pass locally — and `make docs-build` if a documentation page changed.
- [ ] If a request or response type changed: the matching `make generate-*` ran and its output is committed, and the `make generate-*-check` targets pass.
- [ ] Documentation updated for anything a reader would notice, and it describes the system that exists rather than one that is planned.
- [ ] No new dependency without a reason above, and nothing vendored or copied that `.pine/memory/licensing.md` forbids.
- [ ] If this touches a trust boundary — credentials, authentication, the embed session, webhook verification, `safehttp`, the Code node sandbox — the description says what the threat model assumes.
