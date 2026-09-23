---
id: FEAT-mxmjt7
title: 'Parameter editing: live required-field checks with node badges, type-aware IF/Filter operators, Set/HTTP field polish'
status: todo
priority: medium
labels:
    - editor
    - ndv
    - validation
parent: EPIC-8rbys7
created: "2026-09-23T01:48:15Z"
updated: "2026-09-23T01:48:15Z"
---

# Description

Missing required parameters only surface as raw run errors, although every definition already carries `required`.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-14, UXE-19, UXE-24). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## UXE-14: Missing required parameters are not flagged while editing; they surface only as raw run errors

*ux · medium · ndv / validation*

**n8n:** A node with issues shows a warning triangle on the canvas at once. The NDV outlines the field ("Parameter 'URL' is required"). Execute lists the issues.

**Steps to reproduce:**

1. Add an HTTP Request node and leave URL empty. 2. Save (Cmd+S). 3. Execute.

**Actual:**

Save succeeds ("All changes saved") with no badge on the node and no field error. Only Execute shows problems, as three stacked banners: `node "HTTP Request" configuration is invalid: url is required(node)`, `node "HTTP Request" requires parameter "url"(node)` (the same issue twice, with a raw "(node)" suffix) and `Run failed: 422 — workflow validation failed`. An IF with zero conditions also saves without comment.

**Expected:**

Live client-side required-field checks (the definitions already carry `required`), a node badge, and one readable issue per field.

**Suggested fix:**

Run a required-parameter check from the definitions on every draft change to drive the existing `data-invalid` badge and field outlines. De-duplicate server issues by node and field.

**Evidence:**

SD/68-validation-save.png, SD/69-validation-run.png, SD/validate.js.

**Related:**

OPS-14 (raw error strings)


## UXE-19: The IF/Filter operator list ignores the chosen type

*ux · low · ndv / IF*

**n8n:** The operator menu is grouped by type (String, Number, Date & Time, Boolean, Array, Object), and each type shows only its own operators.

**Steps to reproduce:**

1. Add IF, then Add condition. 2. Set the type to Number, then Boolean, and open the operator menu each time.

**Actual:**

All 22 operators are always listed: String offers "larger than", "is true", "after" and "before", and Boolean offers "starts with" and "matches regex".

**Expected:**

Operators filtered by type (typeForOperation already maps them).

**Suggested fix:**

Filter operatorLabels by `typeForOperation(op) === row.type` (plus the untyped exists/empty operators).

**Evidence:**

SD/20-if-condition.png. property-field.svelte:466-495 builds one flat list, and conditions.ts:112 `typeForOperation` exists but is not used to filter.

**Related:**

none


## UXE-24: Set and HTTP field polish: the type select is truncated to "stri", "{{" switches modes inconsistently, and "=" never does

*ux · low · ndv / Set, HTTP Request*

**n8n:** Assignment rows show the name, the type icon and the value at full width. Typing "{{" or "=" in a fixed parameter switches it to an expression.

**Steps to reproduce:**

1. In Set, Add field. 2. Type `Hello {{ $json.name }}` into the value. 3. In HTTP Request, type `…?g={{ $json.greeting }}` into URL.

**Actual:**

The type select renders as "stri". The expr/fixed toggle for the value sits next to the **name** input. Typing "{{" in the Set value flips the row to expr, but typing it into the URL leaves URL in fixed mode, so the braces are sent literally. Boolean parameters read "[ ] Disabled", which is easy to confuse with node disabling.

**Expected:**

Consistent auto-switch to expression on "{{" or "=", readable controls, and toggles labelled On/Off.

**Suggested fix:**

Apply the assignment-row "{{" detection to every string field (asExpression). Widen the type select, or use an icon menu.

**Evidence:**

SD/16-set-add-field.png, SD/17-set-expr-typed.png, SD/25-http-url-expr.png.

**Related:**

none


# Acceptance Criteria
- [ ] Required fields are checked live on the client, with a node badge and one readable issue per field
- [ ] IF/Filter operator lists are filtered by the chosen type (`typeForOperation`)
- [ ] Typing "{{" or "=" switches consistently to expression mode, the type select is readable (no "stri"), and toggles are labelled On/Off

# Implementation Plan

See each finding's suggested fix above.

# Notes

# Related Files

# Attachments
