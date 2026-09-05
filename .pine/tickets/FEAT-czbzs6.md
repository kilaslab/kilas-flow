---
id: FEAT-czbzs6
title: Load node packs from outside the binary
status: todo
priority: medium
labels:
    - packs
    - platform
deps:
    - FEAT-bscygc
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:57:01Z"
updated: "2026-09-05T11:57:01Z"
---

## Scope

The node pack format is finished, data-driven and well reasoned, and there is no way to install one without recompiling the server.

A pack is a JSON document decoded by `nodepack.Decode`, turned into a `node.Definition` plus a `routing.Node` by `nodepack.Load`, and registered by `nodepack.Register`. All three are exported and all three take bytes or structs rather than paths. The registry has been ready for outside contributions since `172c7b6`: `RegisterFrom` accepts `SourcePack` and `SourceSidecar`, refuses `SourceBuiltin`, sets `definition.Source` from the registration path rather than trusting the definition, and rejects any non-builtin claiming the `kilasflow.` prefix as a supply-chain problem. `Source` is serialized into `/api/v1/node-types`, so the editor can already tell a pack node from a built-in one.

And yet the only install path is: add a Go file with a `//go:embed` directive, add a `Register` call to `cmd/kilasflow/main.go`, rebuild the binary, rebuild the image. `packs/telegram/telegram.go` and `packs/waha/waha.go` are exactly that, and a search for `os.ReadDir` or `filepath.Walk` finds nothing outside `internal/interop/n8n/corpus` and `internal/binary`. There is no directory scan, no configuration key, and no runtime registration endpoint — `/api/v1/node-types` is read-only.

So a community node author today writes a `pack.json`, and then their user forks KilasFlow. That is not a community node ecosystem; it is a patch queue.

This ticket is the seam, and every guide in this phase depends on it. A guide teaching authors to write packs that can only be installed by forking would be a guide to a thing nobody can use.

Two constraints are already settled by the code and must be honoured rather than revisited. Loading happens at composition, before the registry is shared: `node.Registry.Register` refuses a duplicate `{type, version}` pair and the registry is documented read-only once the server begins handling work. And a pack cannot name its own executor — `Load` hardcodes `routing.ExecutorID` or `TriggerExecutorID`, "because a pack that could choose its binding could claim any executor the server has registered, including one with privileges no pack should reach". Neither may be loosened to make loading easier.

## Acceptance criteria

- [ ] An operator installs a pack by placing files in a configured directory and restarting, with no rebuild, no fork and no Go toolchain.
- [ ] Loading happens at composition before the registry is shared, and a pack that arrives after the server is serving is not loaded at all rather than partially.
- [ ] Every externally loaded definition is tagged `pack`, and a pack claiming the `kilasflow.` prefix is refused with an error naming the pack and the reason.
- [ ] A malformed, unreadable or duplicate-registering pack fails startup with a message naming the file, and never leaves the server running with a half-registered catalogue.
- [ ] Each pack's bytes are pinned by checksum, and a pack whose contents no longer match its recorded digest is refused rather than loaded.
- [ ] A pack directory that is absent or empty is a normal, silent condition, so the default deployment is unchanged.
- [ ] The trigger, routing, credential-type and dynamic-option registrations a pack carries all work when loaded from disk exactly as they do when embedded, proven against a copy of an existing pack loaded both ways.
- [ ] The configuration key follows the single-word section rule that `envKeyToPath` imposes, so an environment override can actually reach it.

## Implementation Plan

Add a configuration key naming a directory, scan it in `cmd/kilasflow/main.go` at the point `telegram.Register` and `waha.Register` are called today, and feed each file through the existing `Decode` → `Load` → `Register` path. Most of this ticket is not new machinery; it is wiring the machinery that exists to a source that is not `go:embed`.

Decide the on-disk unit deliberately. A single `pack.json` per file is the smallest change and matches what `Decode` takes. A directory per pack — manifest plus icon assets plus a checksum file — is more work and is what the format actually needs, since `IconAsset` carries bytes that an author will not want to base64 into JSON by hand. Recommend the directory, with a named manifest inside it.

Fail startup loudly on any pack error. The tempting alternative is to skip a bad pack and carry on, and it is wrong here: a workflow referencing a node type that silently failed to register becomes unactivatable with no explanation, and an operator who has just installed a pack and sees a running server will not think to check logs. Refusing to start names the problem at the moment the operator caused it.

The checksum requirement is not ceremony. These files become node definitions with outbound HTTP routing and credential bindings; a mutable directory on a server is a place where a file can change without anyone deciding it should. Recommend a lockfile-style record the operator generates with the tooling from V2-p10-16, so the check is against something a human approved rather than against the file's own claim about itself.

Two things to keep out of scope, both deliberately. **Hot reload is out** — `FEAT-48hreg` already settled that hot-loading a pack into a running process stays out of scope, and the registry's immutability is why. **Remote installation is out** — no fetching a pack from a URL or a registry at boot, because that turns a startup path into a network dependency and a supply-chain surface in the same change. Both belong to a later decision if they belong anywhere.

One thing to record for `FEAT-48hreg`: this loader is the install path a WASM pack will reuse. Designing it as "a directory of packs, each with a manifest and a checksum" rather than "a directory of JSON files" costs nothing now and means the WASM ticket adds a module kind rather than a second loader.

## References

- Roadmap plan, p10 section, entry V2-p10-15: `.pine/roadmap.md`.
- `internal/nodepack/nodepack.go` — `Decode` with `DisallowUnknownFields`, `Load`, `Register`, the hardcoded `ExecutorID`, and the opening comment on why a pack is committed JSON rather than a parsed OpenAPI document.
- `internal/node/registry.go` — `RegisterFrom`, `Source`, `SourcePack`, `BuiltinPrefix` and the reserved-namespace refusal.
- `cmd/kilasflow/main.go` — the `telegram.Register` and `waha.Register` calls, and the composition ordering the registry depends on.
- `packs/telegram/telegram.go`, `packs/waha/waha.go` — the `//go:embed` install path this ticket complements.
- `internal/nodepack/trigger.go` — `TriggerExecutorID` and the lifecycle registration a disk-loaded trigger must still perform.
- `internal/config/config.go` — `envKeyToPath` and the single-word section rule the new key must obey.
- `.pine/tickets/FEAT-48hreg.md` — V2-p8-5, whose hot-reload exclusion this ticket matches and whose WASM packs will reuse this loader.
