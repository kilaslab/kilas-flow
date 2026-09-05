---
title: Your first workflow
description: Not yet written. What it will cover, and what to read instead for now.
---

:::caution[This page has not been written yet]
It is a placeholder so that the navigation around it can settle before the
tutorial arrives. A walkthrough written now would be a walkthrough of an editor
that is still changing shape, and correcting it repeatedly is more work than
writing it once against a stable surface.
:::

## What will be here

A single, complete first run: open the editor, add a trigger, add one node that
does something visible, run it, and read the result. It will be written against
the editor as it actually behaves, with the screenshots taken from a real build.

## What to do in the meantime

Start the server as described in [Install](/start/install/) and open the editor
at `/app/workflows`. The pieces you will meet are these, and the summary is
accurate even though the tutorial is not written:

- **A trigger starts a workflow.** `Manual` runs when you press the button in
  the editor and is the one to start with. `Webhook` gives the workflow a URL,
  `Schedule` runs it on a cron expression, and `Execute Workflow Trigger` lets
  another workflow call it.
- **Running is asynchronous.** Starting a workflow records a queued execution
  and returns straight away. The editor follows the run over a server-sent event
  stream, so nodes light up as they complete rather than all at once at the end.
- **Every run is recorded.** Each node's input and output is stored against the
  execution, which is what makes a failed run debuggable after the fact.
- **Parameters can be expressions.** A field containing `{{ … }}` is evaluated
  against the items flowing into the node, which is how a node reads a value
  produced upstream.

If you would rather drive it from the API than the editor, the running server
publishes its own reference at `/docs`, generated from the same Go types that
serve the requests. That page makes no external requests, so it works offline.
