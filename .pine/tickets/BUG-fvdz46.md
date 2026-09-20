---
id: BUG-fvdz46
title: 'Config keys nothing reads: branding.* and embed.session_ttl'
status: doing
priority: medium
labels:
    - platform
    - docs
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:31Z"
updated: "2026-09-20T07:42:30Z"
---

## Problem

Two documented configuration surfaces have no consumer in the code. A config key that does
nothing is worse than a missing one: it reads as a supported feature to an operator and to an
integrator.

## Evidence

- `Config.Branding` — defined at `internal/config/config.go:435-449`, defaulted at `:662`,
  documented in `config.example.yaml`. No reader outside `internal/config`: the only branding
  that reaches the editor is the per-session `embed.Branding` minted at
  `internal/api/handlers/embed.go:65,130`. The dashboard hardcodes "KilasFlow" in its titles
  and sidebar mark.
- `Config.Embed.SessionTTL` — defined at `internal/config/config.go:428-432`, defaulted at
  `:660`. No reader: the lifetime of an embed token comes from the caller's `ttlSeconds` with
  a 15-minute default and a 30-minute cap (`internal/embed/embed.go:50,53`). (The same field
  name on `Config.Auth` at `:318-321` *is* wired — only the embed one is dead.)

## Acceptance criteria

- [ ] For each key, one of: wired to a real consumer (branding injected into the served
      document; `embed.session_ttl` used as the mint default), or removed from
      `internal/config`, `config.example.yaml` and the generated configuration reference.
- [ ] `scripts/config-reference_test.go` still passes, and the generated reference no longer
      documents a key that does nothing.

## Out of scope

Building a deployment-level branding editor UI. Wiring the default value is enough.
