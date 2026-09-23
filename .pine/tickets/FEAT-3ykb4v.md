---
id: FEAT-3ykb4v
title: "Dialogs, menus, toasts and empty states: three elevation levels, a mounted toast system, dialog sizes, no overflow"
status: todo
priority: medium
labels:
    - frontend
    - dialogs
    - feedback
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:16Z"
updated: "2026-09-23T03:05:16Z"
---

# Description

Menus and popovers have a transparent box-shadow and the same colour as cards, so they don't lift off the page. Dialogs mix 32 px inputs with 28 px lists, and the new-credential dialog's content overflows its card at 1440×900 (the functional part is FEAT-fqmh01). No toast system is mounted: the ui/sonner wrapper is unused and transient feedback is scattered across at least 12 inline role=status texts ('Copied', import results, saves). The empty states use different compositions. This ticket applies the elevation spec (audit §9), mounts a token-styled Toaster, and defines dialog sizes of 400, 560 and 720 px.

# Acceptance Criteria
- [ ] Menus, popovers, tooltips and toasts use --kf-overlay plus --kf-shadow-2, and dialogs and the command palette use --kf-shadow-3. measure.js finds no overlay element with a transparent box-shadow, and the overlay is ≥ΔL 0.08 above --kf-bg in dark.
- [ ] The Toaster is mounted once in the root layout, styled only with tokens, and announces through aria-live. Copy, save, import, delete and test-connection confirmations use it, and the ad-hoc inline 'Copied' paragraphs are removed (a grep list in the PR).
- [ ] Dialogs use size tokens (sm 400, md 560, lg 720) and 28 px controls; at 1280×800 and 1440×900, no dialog has scrollWidth > clientWidth (check over new-credential, import, export, datastore create, schedule create).
- [ ] Every empty state (schedules, credentials, datastores, executions with filters, the workflow list with search) uses one EmptyState composition, and its text passes ≥4.5:1.
- [ ] Dialog and menu entrance and exit follow the VR-10 motion tokens.

# Implementation Plan

Tokenise the shadcn Dialog, Popover, DropdownMenu and Tooltip content classes. Keep the chat panel's surface on the same elevation level 2; its content stays with FEAT-edzr73.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
