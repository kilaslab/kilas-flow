---
id: BUG-548bk9
title: 'Code-node JavaScript: an unhandled promise rejection from a callback is lost instead of failing the run as in Node'
status: todo
priority: low
parent: EPIC-tjnr1z
created: "2026-09-23T07:09:43Z"
updated: "2026-09-23T07:09:43Z"
---

# Description

In Node an exception thrown from a callback (crypto.randomBytes(n, cb),
pbkdf2, scrypt, randomInt) or a promise rejected with no handler is uncaught
and ends the process, which fails an n8n Code node. goja tracks neither, so
in Code-node JavaScript such an error is lost and the node succeeds with
whatever the code returned. A throw from a timer callback already fails the
run. Found by the crypto lane of FEAT-zjrw76.

# Steps to Reproduce

A Code (JavaScript) node with:

    require('crypto').randomBytes(4, () => { throw new Error('lost') })
    await new Promise((resolve) => setTimeout(resolve, 10))
    return items

# Expected

What n8n does with the same node: check its JS task runner first (it may
fail the node with "lost", or only log it).

# Actual

The node succeeds, and "lost" appears nowhere.

# Acceptance Criteria
- [ ] n8n's behaviour for an unhandled rejection and a throwing async callback
      in a Code node is recorded.
- [ ] Code-node JavaScript does the same, using goja's promise rejection
      tracker, without counting the runtime's own promises or the body's
      result, which the runner consumes.

# Related Files

# Attachments
